package hashio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
