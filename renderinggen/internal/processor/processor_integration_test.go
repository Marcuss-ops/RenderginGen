package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestProcessEndToEndCLI runs the full pipeline against the real chronon3d_cli
// binary (skipped when it is not installed):
//
//	job -> validate -> workspace -> materialize -> semantic compile
//	-> plan.json -> real render -> result.mp4 -> sha256
//	-> artifact store -> artifact metadata
//
// The job is the canonical asset-free-of-URLs semantic golden
// (GoldenSemanticOverlayJobV1): every fixture is seeded into the in-memory
// store by content hash, so the flow is fully self-contained and proves the
// CLI wiring end to end without network access. Run it with:
//
//	CHRONON_HOME=<build>/chronon/linux-video-release go test -run TestProcessEndToEndCLI ./internal/processor/
func TestProcessEndToEndCLI(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	// Seed the object store with the golden fixtures so the semantic job's
	// assets materialize by hash.
	fixtures := map[string]string{
		"983676516167748b74de6f4771fb384c664fd913acb8b471122ecacf5da5ea6c": filepath.Join("..", "..", "..", "testdata", "golden", "Poppins-Bold.ttf"),
		"52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78": filepath.Join("..", "..", "..", "testdata", "golden", "background.jpg"),
		"ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6": filepath.Join("..", "..", "..", "testdata", "golden", "apple.png"),
		"690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648": filepath.Join("..", "..", "..", "testdata", "golden", "DejaVuSans.ttf"),
	}
	mem := storage.NewMemory()
	for hash, path := range fixtures {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture %s: %v", path, err)
		}
		if err := mem.Store(context.Background(), hash, data); err != nil {
			t.Fatalf("seed store %s: %v", hash, err)
		}
	}
	store := storage.New(mem, storage.Options{})
	proc := New(t.TempDir(), "software", cli.Version(), "http://store:9000", store, cli)

	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenSemanticOverlayJobV1), &job); err != nil {
		t.Fatalf("decode semantic golden job: %v", err)
	}
	job.ID = "e2e-cli-golden-semantic"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	if artifact.ChrononVersion == "" {
		t.Fatalf("artifact has no Chronon provenance (chronon_version empty)")
	}
	if artifact.FrameCount <= 0 {
		t.Fatalf("artifact frame_count = %d", artifact.FrameCount)
	}

	// Chronon timing sidecar metrics must be projected onto the artifact
	// under the chronon_ prefix (summary.render_ms, summary.encode_ms, ...).
	hasChrononMetric := false
	for key := range artifact.Metrics {
		if strings.HasPrefix(key, "chronon_") {
			hasChrononMetric = true
			break
		}
	}
	if !hasChrononMetric {
		t.Fatalf("artifact metrics carry no chronon_* keys: %v", artifact.Metrics)
	}

	// The rendered mp4 was published to the artifact store.
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
