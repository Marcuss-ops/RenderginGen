package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// stagingMem is a memory backend WITH the streaming staging extension, so the
// tail-upload lane can be exercised against a store that computes the digest
// from the stream exactly like the real object store.
type stagingMem struct {
	*storage.Memory

	mu        sync.Mutex
	finalized map[string]string // session -> asserted digest
	uploads   int
	failPut   bool
}

func newStagingMem() *stagingMem {
	return &stagingMem{Memory: storage.NewMemory(), finalized: map[string]string{}}
}

func (m *stagingMem) StoreStaged(ctx context.Context, session string, r io.Reader) (string, int64, error) {
	m.mu.Lock()
	m.uploads++
	fail := m.failPut
	m.mu.Unlock()
	if fail {
		return "", 0, errors.New("staging backend unavailable")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	m.mu.Lock()
	m.finalized[session] = digest
	m.mu.Unlock()
	if err := m.Memory.Store(ctx, digest, data); err != nil {
		return "", 0, err
	}
	return digest, int64(len(data)), nil
}

// appendSlowly simulates the mux: bytes arrive over time while the upload is
// already streaming. The first Read of the upload must happen before the file
// is complete — that overlap is the entire point of the lane.
func appendSlowly(t *testing.T, path string, chunks [][]byte) {
	t.Helper()
	for i, chunk := range chunks {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Give the tail a chance to consume the first chunk while more
			// bytes are still coming.
			time.Sleep(30 * time.Millisecond)
		}
	}
}

// TestTailUploadStreamsWhileTheProducerAppends: the upload runs CONCURRENTLY
// with the append loop and installs the bytes under the stream digest — which
// must equal the sha256 of the finished file (local_sha == objectstore_sha).
func TestTailUploadStreamsWhileTheProducerAppends(t *testing.T) {
	backend := newStagingMem()
	proc := &Processor{store: storage.New(backend, storage.Options{})}
	path := filepath.Join(t.TempDir(), "out.mp4")

	upload := proc.startTailUpload(context.Background(), "job/overlap", path)
	appendSlowly(t, path, [][]byte{[]byte("frag-"), []byte("mented-"), []byte("bytes")})
	upload.Finish()

	digest, size, err := upload.Wait()
	if err != nil {
		t.Fatalf("staged upload: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("stream digest = %s, want sha256(file) = %x", digest, sum)
	}
	if size != int64(len(data)) {
		t.Fatalf("streamed size = %d, want %d", size, len(data))
	}
	backend.mu.Lock()
	finalized := len(backend.finalized)
	backend.mu.Unlock()
	if finalized != 1 {
		t.Fatalf("finalized sessions = %d, want exactly 1", finalized)
	}
	if got, err := backend.Memory.Fetch(context.Background(), digest); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("store bytes under %s = %q, %v; want the file's bytes", digest, got, err)
	}
}

// TestTailUploadAbortDiscardsPendingBytes: a render that fails after the
// upload started must leave NOTHING in the store — the stream errors out and
// no digest is ever finalized.
func TestTailUploadAbortDiscardsPendingBytes(t *testing.T) {
	backend := newStagingMem()
	proc := &Processor{store: storage.New(backend, storage.Options{})}
	path := filepath.Join(t.TempDir(), "out.mp4")

	upload := proc.startTailUpload(context.Background(), "job-abort", path)
	appendSlowly(t, path, [][]byte{[]byte("partial")})
	upload.Abort() // render failed: discard

	if _, _, err := upload.Wait(); err == nil {
		t.Fatal("aborted upload must report an error, not a digest")
	}
	backend.mu.Lock()
	n := backend.uploads
	finalized := len(backend.finalized)
	backend.mu.Unlock()
	if finalized != 0 {
		t.Fatalf("aborted upload finalized %d sessions, want 0", finalized)
	}
	if n == 0 {
		t.Fatal("the upload never reached the backend; the test proves nothing")
	}
}

// TestTailUploadFallbackToPutReaderOnStagedFailure: when the staging surface
// fails, the store phase must fall back to the classic hash-then-PUT path —
// an optimization failing must not fail a render whose bytes are intact.
func TestTailUploadFallbackToPutReaderOnStagedFailure(t *testing.T) {
	backend := newStagingMem()
	backend.failPut = true
	p := &Processor{store: storage.New(backend, storage.Options{})}
	path := filepath.Join(t.TempDir(), "out.mp4")
	data := []byte("classic-fallback-bytes")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	upload := p.startTailUpload(context.Background(), "job-fallback", path)
	upload.Finish()

	// storeArtifact consumes the tail: with the staged lane broken it must
	// resolve the identity itself and PutReader the bytes.
	sum := sha256.Sum256(data)
	artifact, err := p.storeArtifact(context.Background(), "job-fallback", path,
		receiptOutcome{Identity: artifactIdentity{SHA256: hex.EncodeToString(sum[:]), Source: "test", Proven: true}},
		upload, nil, map[string]float64{}, time.Now(), nil, overlay.Stats{}, 0, false, false)
	if err != nil {
		t.Fatalf("storeArtifact with failed staging: %v", err)
	}
	if artifact.ArtifactHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("artifact hash = %s, want %x", artifact.ArtifactHash, sum)
	}
	got, err := backend.Memory.Fetch(context.Background(), artifact.ArtifactHash)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("store bytes = %q, %v; want the file's bytes via PutReader", got, err)
	}
}

// TestTailUploadDigestMismatchFailsClosed: the stream digest disagreeing with
// the receipt identity is corruption; the pipeline must refuse the artifact.
func TestTailUploadDigestMismatchFailsClosed(t *testing.T) {
	backend := newStagingMem()
	p := &Processor{store: storage.New(backend, storage.Options{})}
	path := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(path, []byte("real bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	upload := p.startTailUpload(context.Background(), "job-mismatch", path)
	upload.Finish()

	wrong := sha256.Sum256([]byte("different bytes"))
	_, err := p.storeArtifact(context.Background(), "job-mismatch", path,
		receiptOutcome{Identity: artifactIdentity{SHA256: hex.EncodeToString(wrong[:]), Source: "receipt", Proven: true}},
		upload, nil, map[string]float64{}, time.Now(), nil, overlay.Stats{}, 0, false, false)
	if err == nil {
		t.Fatal("storeArtifact accepted a staged digest that contradicts the receipt identity")
	}
}

// TestStagingSessionIDMatchesStoreGrammar: the session becomes a file name in
// the store's staging workspace; it must fit `[A-Za-z0-9_-]{1,128}` whatever
// the job id contains, and be unique per call (retry isolation).
func TestStagingSessionIDMatchesStoreGrammar(t *testing.T) {
	a := stagingSessionID("plan:final:scene/1")
	b := stagingSessionID("plan:final:scene/1")
	for _, id := range []string{a, b} {
		if len(id) == 0 || len(id) > 128 {
			t.Fatalf("session %q length %d out of grammar", id, len(id))
		}
		for _, r := range id {
			ok := r == '-' || r == '_' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				t.Fatalf("session %q contains %q, outside [A-Za-z0-9_-]", id, r)
			}
		}
	}
	if a == b {
		t.Fatalf("two sessions for the same job collided: %q", a)
	}
	if stagingSessionID("") == "" {
		t.Fatal("an empty job id must still produce a valid session")
	}
}
