package store

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

// failingReader delivers one chunk and then fails, reproducing a producer that
// dies mid-stream. Every write path must leave the store exactly as it was.
type failingReader struct {
	data []byte
	err  error
	sent bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

// TestFailedPutReaderLeavesExistingObjectIntact pins the atomic-replace rule
// for a failed republish: the stream is written to a temp file and only
// renamed after verification, so an error mid-stream must leave the previous
// object readable and leave no temp file behind. The same failure at a fresh
// address must install nothing.
func TestFailedPutReaderLeavesExistingObjectIntact(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("original object bytes")
	key := digestOf(payload)
	if err := s.Put(key, payload); err != nil {
		t.Fatal(err)
	}

	if err := s.PutReader(key, &failingReader{data: []byte("partial"), err: errors.New("producer failed")}, -1); err == nil {
		t.Fatal("PutReader succeeded despite a reader error")
	}
	reader, size, err := s.Open(key)
	if err != nil {
		t.Fatalf("the previous object was damaged by the failed republish: %v", err)
	}
	got, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(got, payload) || size != int64(len(payload)) {
		t.Fatalf("object after failed republish = %d bytes %q, err=%v", size, got, err)
	}
	assertNoTempFiles(t, s, key)

	freshKey := digestOf([]byte("never installed"))
	if err := s.PutReader(freshKey, &failingReader{data: []byte("partial"), err: errors.New("producer failed")}, -1); err == nil {
		t.Fatal("PutReader installed a fresh address despite a reader error")
	}
	if _, _, err := s.Open(freshKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed fresh PutReader left an object: %v", err)
	}
	assertNoTempFiles(t, s, freshKey)
}

// TestConcurrentPutReadersNeverExposePartialBytes pins the visibility side of
// the content-address contract: a reader that opens an address while it is
// being republished must see either the complete old object or the complete
// new one, never a truncated/partial file. The write path's temp+rename is what
// provides this; an in-place truncate+write would pass the single-writer tests
// and fail here.
func TestConcurrentPutReadersNeverExposePartialBytes(t *testing.T) {
	s := New(t.TempDir())
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	key := digestOf(payload)
	if err := s.Put(key, payload); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var writeErr error
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			if err := s.Put(key, payload); err != nil {
				writeErr = err
				return
			}
		}
	}()

	check := func() {
		t.Helper()
		reader, size, err := s.Open(key)
		if err != nil {
			t.Fatalf("reader could not open an existing address during republish: %v", err)
		}
		data, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			t.Fatalf("read during republish: %v", readErr)
		}
		if size != int64(len(payload)) || !bytes.Equal(data, payload) {
			t.Fatalf("reader observed a partial object: size=%d, %d bytes", size, len(data))
		}
	}

	check() // baseline, before the writer starts
	for {
		select {
		case <-done:
			if writeErr != nil {
				t.Fatalf("republish failed: %v", writeErr)
			}
			check() // and once after the last rename
			assertNoTempFiles(t, s, key)
			return
		default:
			check()
		}
	}
}

// TestFailedStagingWriteKeepsPreviousAttemptFinalizable pins the retry rule of
// the streaming workspace: a failed attempt must not destroy the pending bytes
// of the last successful write, because the caller may still finalize them.
func TestFailedStagingWriteKeepsPreviousAttemptFinalizable(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("previous attempt bytes")
	digest, _, err := s.StagingWrite("retry-session", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.StagingWrite("retry-session", &failingReader{data: []byte("partial"), err: errors.New("producer failed")}); err == nil {
		t.Fatal("StagingWrite succeeded despite a reader error")
	}
	if err := s.StagingFinalize("retry-session", digest); err != nil {
		t.Fatalf("the failed retry destroyed the previous pending attempt: %v", err)
	}
	reader, size, err := s.Open(digest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(got, payload) || size != int64(len(payload)) {
		t.Fatalf("installed bytes = %d %q, err=%v", size, got, err)
	}
}

// TestStagingAbortDiscardsPendingAndSidecar pins abort's full cleanup: both
// names of the attempt disappear, so a later finalize is a clean not-found and
// no orphaned workspace file survives.
func TestStagingAbortDiscardsPendingAndSidecar(t *testing.T) {
	s := New(t.TempDir())
	digest, _, err := s.StagingWrite("abort-session", bytes.NewReader([]byte("discard me")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StagingAbort("abort-session"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := s.StagingFinalize("abort-session", digest); !errors.Is(err, ErrStagingNotFound) {
		t.Fatalf("finalize after abort = %v, want ErrStagingNotFound", err)
	}
	for _, p := range []string{s.stagingPath("abort-session"), s.stagingDigestPath("abort-session")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("abort left %s behind (stat err = %v)", p, err)
		}
	}
}

// TestMemoryBackendRejectsDeclaredLengthMismatch closes the parity gap with the
// disk adapter: Content-Length is a claim, and a stream whose byte count
// disagrees with it must not become visible.
func TestMemoryBackendRejectsDeclaredLengthMismatch(t *testing.T) {
	m := NewMemory()
	payload := []byte("hello")
	key := digestOf(payload)
	if err := m.PutReader(key, bytes.NewReader(payload), int64(len(payload))+1); err == nil {
		t.Fatal("PutReader accepted a declared length that disagrees with the stream")
	}
	if _, _, _, err := m.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("length mismatch installed an object: %v", err)
	}
}

// TestMemoryGetSnapshotsBytes pins the ownership promise of Memory.Get: the
// reader it returns is backed by a copy, so a concurrent writer replacing the
// stored bytes cannot change an in-flight download.
func TestMemoryGetSnapshotsBytes(t *testing.T) {
	m := NewMemory()
	original := []byte("snapshot bytes")
	key := digestOf(original)
	if err := m.Put(key, original); err != nil {
		t.Fatal(err)
	}
	reader, _, _, err := m.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// Replace the stored object behind the open reader, the way a concurrent
	// restore would.
	m.mu.Lock()
	modTime := m.objects[key].modTime
	m.objects[key] = memoryObject{data: []byte("REPLACED bytes"), modTime: modTime}
	m.mu.Unlock()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("open reader observed replaced bytes: %q, want %q", got, original)
	}
}
