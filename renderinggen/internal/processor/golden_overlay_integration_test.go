package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestProcessGoldenOverlayJobV1 runs the whole pipeline against the real
// chronon3d_cli binary (skipped when it is not installed) with the canonical
// GoldenOverlayJobV1 workload:
//
//	background.jpg (full 5s)
//	+ "QUESTO CAMBIA TUTTO" (title_centered, f20-60)
//	+ "APPLE"               (kinetic_word, f65-95)
//	+ apple.png             (contain, right, f90-135)
//
// The assets (background, apple overlay, vendored DejaVuSans font) are the
// deterministic fixtures under ../../../testdata/golden and are pre-seeded
// into the artifact store by their content hashes, exactly as the object
// store would hold them for a real queue job. The test asserts the golden's
// immutability (fixture hashes must match the payload) and the full chain:
// validate -> materialize -> plan.json -> render -> publish.
func TestProcessGoldenOverlayJobV1(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	store := storage.New(storage.NewMemory(), storage.Options{})
	proc := New(t.TempDir(), "software", cli.Version(), "http://store:9000", store, cli)

	// Decode the canonical golden job and re-seed its assets by hash, as the
	// object store would hold them for a real queue submission.
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenOverlayJobV1), &job); err != nil {
		t.Fatalf("decode GoldenOverlayJobV1: %v", err)
	}
	if job.ID != "golden-overlay-v1" || job.Schema != queue.JobSchemaV1 {
		t.Fatalf("unexpected golden job envelope: %+v", job)
	}
	seedGoldenAssets(t, store, job.Assets)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	artifact, err := proc.Process(ctx, &job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	if artifact.Kind != "segment" || artifact.ContentType != "video/mp4" {
		t.Fatalf("artifact type: kind=%q content_type=%q", artifact.Kind, artifact.ContentType)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash {
		t.Fatalf("artifact hash: %+v", artifact)
	}
	if artifact.SizeBytes <= 0 {
		t.Fatalf("artifact size = %d", artifact.SizeBytes)
	}
	if artifact.Backend != "software" {
		t.Fatalf("artifact backend = %q", artifact.Backend)
	}

	// The rendered 5s mp4 was published to the artifact store.
	stored, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		t.Fatalf("get published artifact: %v", err)
	}
	if len(stored) == 0 {
		t.Fatalf("published artifact is empty")
	}
	if int64(len(stored)) != artifact.SizeBytes {
		t.Fatalf("published size %d != artifact size %d", len(stored), artifact.SizeBytes)
	}
}

// seedGoldenAssets asserts each asset hash in the job matches the deterministic
// fixture file on disk (golden immutability) and seeds the bytes into the
// artifact store under that hash, so materialize resolves them from L3.
func seedGoldenAssets(t *testing.T, store *storage.Client, assets []queue.AssetRef) {
	t.Helper()
	for _, a := range assets {
		// logical_path is assets/<file>; the fixture lives next to the
		// canonical payload in testdata/golden/<file>.
		name := filepath.Base(a.LogicalPath)
		path := filepath.Join("..", "..", "..", "testdata", "golden", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden fixture %s: %v", path, err)
		}
		if got := storage.Hash(data); got != a.Hash {
			t.Fatalf("golden immutability violated: %s sha256=%s but payload hash=%s (re-run infra/e2e/gen-golden-assets.py and update both copies)", path, got, a.Hash)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := store.Put(ctx, a.Hash, data); err != nil {
			cancel()
			t.Fatalf("seed asset %s: %v", a.Hash, err)
		}
		cancel()
	}
}
