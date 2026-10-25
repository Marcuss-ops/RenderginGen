package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGrowingFileReaderDeliversAppendsThenEOF pins the delivery contract: every
// byte appended before Finish is delivered, and EOF is reported only after
// Finish — never on a temporarily-empty file.
func TestGrowingFileReaderDeliversAppendsThenEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := NewGrowingFileReader(path)

	buf := make([]byte, 64)
	n, err := g.Read(buf)
	if err != nil || string(buf[:n]) != "first" {
		t.Fatalf("first read = %q, %v; want \"first\", nil", buf[:n], err)
	}

	// Append more before Finish: the tail must pick it up without EOF in
	// between.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("+second"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	g.Finish()

	// Drain: the appended bytes first, then a real EOF.
	var got []byte
	for {
		n, err := g.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("drain read: %v", err)
		}
	}
	if string(got) != "+second" {
		t.Fatalf("drained = %q, want \"+second\"", got)
	}
}

// TestGrowingFileReaderFinishDuringBlockedRead: a Read blocked on an empty
// file wakes on Finish and reports EOF (the file existed but had nothing left).
func TestGrowingFileReaderFinishDuringBlockedRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	g := NewGrowingFileReader(path)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		for {
			_, err := g.Read(buf)
			if err != nil {
				done <- err
				return
			}
		}
	}()

	select {
	case err := <-done:
		t.Fatalf("Read ended before Finish with %v; the tail must block while the producer may still append", err)
	case <-time.After(50 * time.Millisecond):
	}
	g.Finish()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("after Finish: err = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not wake after Finish")
	}
}

// TestGrowingFileReaderAbortFailsTheStream: Abort ends the stream with the
// given error and is idempotent.
func TestGrowingFileReaderAbortFailsTheStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := NewGrowingFileReader(path)
	g.Abort(nil)
	buf := make([]byte, 4)
	if _, err := g.Read(buf); !errors.Is(err, ErrGrowingFileAborted) {
		t.Fatalf("Read after Abort = %v, want ErrGrowingFileAborted", err)
	}
	g.Abort(nil) // idempotent
	g.Finish()   // no-op after Abort
	if _, err := g.Read(buf); !errors.Is(err, ErrGrowingFileAborted) {
		t.Fatalf("Read after Abort+Finish = %v, want ErrGrowingFileAborted", err)
	}
}

// TestGrowingFileReaderAbortAfterFinishIsNoOp pins the commit rule the render
// lane relies on: once Finish commits the stream, the caller's deferred
// cleanup Abort must not retroactively fail bytes already delivered.
func TestGrowingFileReaderAbortAfterFinishIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	if err := os.WriteFile(path, []byte("committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := NewGrowingFileReader(path)
	g.Finish()
	g.Abort(nil)

	got, err := io.ReadAll(g)
	if err != nil {
		t.Fatalf("read committed stream: %v", err)
	}
	if string(got) != "committed" {
		t.Fatalf("stream = %q, want \"committed\"", got)
	}
}

// TestGrowingFileReaderMissingFileFailsAfterFinish: a producer that signalled
// completion without ever creating the file is an error, not an empty upload —
// a zero-byte object under the empty-bytes digest would be a silent wrongness.
func TestGrowingFileReaderMissingFileFailsAfterFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never.bin")
	g := NewGrowingFileReader(path)
	g.Finish()
	buf := make([]byte, 4)
	if _, err := g.Read(buf); !errors.Is(err, ErrGrowingFileMissing) {
		t.Fatalf("Read = %v, want ErrGrowingFileMissing", err)
	}
}
