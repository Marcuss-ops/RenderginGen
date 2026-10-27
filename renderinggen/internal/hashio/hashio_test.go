package hashio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCopyHashesAndCopies pins the shared primitive: the digest is the SHA-256
// of the source stream and the byte count is exact, so callers can rely on one
// implementation for the content-addressed invariant (local == L3 == db).
func TestCopyHashesAndCopies(t *testing.T) {
	want := "hello, content-addressed world"
	var dst bytes.Buffer

	digest, size, err := Copy(strings.NewReader(want), &dst)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", size, len(want))
	}
	if dst.String() != want {
		t.Fatalf("copied bytes = %q, want %q", dst.String(), want)
	}
	sum := sha256.Sum256([]byte(want))
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s, want %s", digest, hex.EncodeToString(sum[:]))
	}
}

// readerOnly hides io.WriterTo from an underlying reader so Copy must go
// through its pooled scratch buffer (io.CopyBuffer's fast paths skip the
// buffer when the source implements WriterTo, e.g. strings.Reader).
type readerOnly struct{ r io.Reader }

func (r readerOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

// TestCopyLargeStreamAcrossBufferChunks pins correctness of the pooled
// scratch buffer across MANY buffer refills: a 1MiB stream (16x the 64KiB
// pool buffer) must still yield the exact digest and byte count. The pooling
// is invisible to the contract — same bytes, same digest — and this test is
// what keeps it that way.
func TestCopyLargeStreamAcrossBufferChunks(t *testing.T) {
	want := strings.Repeat("content-addressed-payload;", 40*1024) // ~1MiB
	var dst bytes.Buffer
	digest, size, err := Copy(readerOnly{strings.NewReader(want)}, &dst)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", size, len(want))
	}
	if dst.String() != want {
		t.Fatal("copied bytes drifted from the source over multiple buffer chunks")
	}
	sum := sha256.Sum256([]byte(want))
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s, want %s", digest, hex.EncodeToString(sum[:]))
	}
}

// BenchmarkCopyStream reports the per-copy allocation profile of the shared
// streaming primitive. The scratch buffer is pooled (see copyBufPool), so a
// streamed copy must NOT carry an io.Copy-sized allocation per call: run with
//
//	go test -run '^$' -bench BenchmarkCopyStream -benchmem ./internal/hashio/
//
// and watch allocs/op stay flat when the payload grows past one buffer.
func BenchmarkCopyStream(b *testing.B) {
	payload := strings.Repeat("x", 8*1024*1024)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Copy(readerOnly{strings.NewReader(payload)}, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

// TestReaderMatchesCopy verifies Reader is Copy to io.Discard: same digest and
// byte count, no buffering surprises.
func TestReaderMatchesCopy(t *testing.T) {
	want := strings.Repeat("abc123", 4096)
	digest, size, err := Reader(strings.NewReader(want))
	if err != nil {
		t.Fatalf("Reader: %v", err)
	}
	if size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", size, len(want))
	}
	sum := sha256.Sum256([]byte(want))
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s, want %s", digest, hex.EncodeToString(sum[:]))
	}
}

func TestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blob.bin")
	payload := []byte("file-backed content address")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := File(path)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	sum := sha256.Sum256(payload)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s, want %s", digest, hex.EncodeToString(sum[:]))
	}
}
