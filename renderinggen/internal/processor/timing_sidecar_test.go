package processor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// writeSidecar builds a processor with a memory-backed store plus a sidecar file
// holding exactly data.
func writeSidecar(t *testing.T, data []byte) (*Processor, *storage.Client, string) {
	t.Helper()
	proc, store, _ := newProcessor(t)
	path := filepath.Join(t.TempDir(), "out.mp4.timing.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return proc, store, path
}

// TestPutTimingSidecarStoresExactlyTheFileBytes pins the one-read fast path: the
// content address recorded by the worker must be the sha256 of the file's bytes
// and the store must hold those same bytes verbatim, so replacing the historical
// hash-then-reopen pair with a single read cannot change what is published.
func TestPutTimingSidecarStoresExactlyTheFileBytes(t *testing.T) {
	raw := []byte(`{"schema":"chronon3d.render-timing.v1","frames":[1,2,3]}`)
	proc, store, path := writeSidecar(t, raw)

	hash, size, err := proc.putTimingSidecar(context.Background(), path)
	if err != nil {
		t.Fatalf("putTimingSidecar: %v", err)
	}
	want := storage.Hash(raw)
	if size != int64(len(raw)) {
		t.Fatalf("size = %d, want %d", size, len(raw))
	}
	if hash != want {
		t.Fatalf("hash = %q, want the sha256 of the file bytes %q", hash, want)
	}
	stored, err := store.Get(context.Background(), hash)
	if err != nil {
		t.Fatalf("get stored sidecar: %v", err)
	}
	if !bytes.Equal(stored, raw) {
		t.Fatalf("stored bytes = %q, want %q", stored, raw)
	}
}

// TestPutTimingSidecarEmptyFileIsANoOp covers the "there is nothing to publish"
// branch: no address, no size and nothing written to the store.
func TestPutTimingSidecarEmptyFileIsANoOp(t *testing.T) {
	proc, store, path := writeSidecar(t, nil)

	hash, size, err := proc.putTimingSidecar(context.Background(), path)
	if err != nil {
		t.Fatalf("an empty sidecar must be a no-op, got %v", err)
	}
	if hash != "" || size != 0 {
		t.Fatalf("hash/size = %q/%d, want empty/0", hash, size)
	}
	if _, err := store.Get(context.Background(), storage.Hash(nil)); err == nil {
		t.Fatal("an empty sidecar must not be written to the store")
	}
}

// TestPutTimingSidecarStreamsOversizedDocuments covers the memory bound: above
// the inline cap the document still lands byte-exact under its true content
// address, it just takes the constant-memory streaming path instead of being
// read into memory first.
func TestPutTimingSidecarStreamsOversizedDocuments(t *testing.T) {
	raw := bytes.Repeat([]byte("abcdefgh"), (timingSidecarInlineMaxBytes/8)+64)
	proc, store, path := writeSidecar(t, raw)

	hash, size, err := proc.putTimingSidecar(context.Background(), path)
	if err != nil {
		t.Fatalf("putTimingSidecar (oversized): %v", err)
	}
	want := storage.Hash(raw)
	if size != int64(len(raw)) || hash != want {
		t.Fatalf("oversized hash/size = %q/%d, want %q/%d", hash, size, want, len(raw))
	}
	stored, err := store.Get(context.Background(), hash)
	if err != nil {
		t.Fatalf("get oversized sidecar: %v", err)
	}
	if !bytes.Equal(stored, raw) {
		t.Fatal("oversized stored bytes differ from the file")
	}
}

// TestPutTimingSidecarMissingFileIsAnError keeps the fail-open contract
// observable: the caller turns this error into a logged no-op, so it must be
// reported rather than silently swallowed here.
func TestPutTimingSidecarMissingFileIsAnError(t *testing.T) {
	proc, _, _ := newProcessor(t)
	if _, _, err := proc.putTimingSidecar(context.Background(), filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing sidecar must report an error to the fail-open caller")
	}
}
