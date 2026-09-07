package processor

import (
	"context"
	"encoding/json"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"os"
	"testing"
	"time"
)

// TestProcessGoldenOverlayJobV1 runs the whole pipeline against the real
// chronon3d_cli binary (skipped when it is not installed) with the canonical
// GoldenOverlayJobV1 workload:
//
//	background.jpg (full 5s)
//	+ "QUESTO CAMBIA TUTTO" (caption_card, f20-60)
//	+ "APPLE"               (active_word_pop, f65-95)
//	+ apple.png             (contain, right, f90-135)
//
// The assets (background, apple overlay, vendored Poppins-Bold font) are the
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

// TestGoldenSemanticOverlayJobV1Compiles verifies the semantic golden lowers
// into the concrete Chronon plan WITHOUT the CLI: the golden must always
// compile (CompileIfSemantic) into the expected layer set with the existing
// preset vocabulary. This guards the golden in environments where
// chronon3d_cli is not installed; TestProcessGoldenSemanticOverlayJobV1 runs
// the full render when it is.
func TestGoldenSemanticOverlayJobV1Compiles(t *testing.T) {
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenSemanticOverlayJobV1), &job); err != nil {
		t.Fatalf("decode GoldenSemanticOverlayJobV1: %v", err)
	}

	compiled, assets, semantic, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		t.Fatalf("compile semantic golden: %v", err)
	}
	if !semantic {
		t.Fatal("golden must be recognized as a semantic plan")
	}
	if len(assets) != 2 {
		t.Fatalf("compiled assets = %d, want 2 (background, apple)", len(assets))
	}
	for _, a := range assets {
		if len(a.Hash) != 64 || a.LogicalPath == "" {
			t.Fatalf("compiled asset not content-addressed: %+v", a)
		}
	}

	var concrete struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
		Canvas  struct {
			DurationFrames int64 `json:"duration_frames"`
		} `json:"canvas"`
		Layers []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Preset    string `json:"preset"`
			Animation *struct {
				Tracks []struct{} `json:"tracks"`
			} `json:"animation"`
		} `json:"layers"`
	}
	compiledBytes, err := compiled.Marshal()
	if err != nil {
		t.Fatalf("encode compiled plan: %v", err)
	}
	if err := json.Unmarshal(compiledBytes, &concrete); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	if concrete.Schema != "chronon.render-plan.v2" || concrete.Version != 2 {
		t.Fatalf("compiled plan = %s/%d", concrete.Schema, concrete.Version)
	}
	if concrete.Canvas.DurationFrames != 150 {
		t.Fatalf("duration = %d frames, want 150 (5s @ 30fps)", concrete.Canvas.DurationFrames)
	}
	byID := map[string]string{}
	for _, layer := range concrete.Layers {
		byID[layer.ID] = layer.Preset
	}
	if byID["important_phrase"] != "" {
		t.Fatalf("important_phrase must not carry executable preset metadata: %q", byID["important_phrase"])
	}
	if byID["important_word"] != "" {
		t.Fatalf("important_word must not carry executable preset metadata: %q", byID["important_word"])
	}
	if byID["image_overlay_image"] != "" {
		t.Fatalf("image_overlay must not carry executable preset metadata: %q", byID["image_overlay_image"])
	}
}

// capturingRenderer wraps the real Chronon client so the test can inspect the
// compiled plan.json the worker wrote before delegating to chronon3d_cli. It
// proves the semantic golden really goes through CompileIfSemantic: the plan
// handed to the renderer must be the concrete chronon.render-plan.v2, never
// the PipelineGen overlay-plan.v1.
type capturingRenderer struct {
	chronon.Renderer
	planJSON []byte
}

func (c *capturingRenderer) Render(ctx context.Context, req chronon.RenderRequest) error {
	data, err := os.ReadFile(req.PlanPath)
	if err != nil {
		return err
	}
	c.planJSON = data
	return c.Renderer.Render(ctx, req)
}
