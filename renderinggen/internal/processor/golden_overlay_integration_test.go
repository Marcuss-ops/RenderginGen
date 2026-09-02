package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
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
	if err := json.Unmarshal(compiled, &concrete); err != nil {
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

// TestProcessGoldenSemanticOverlayJobV1 runs the whole pipeline against the
// real chronon3d_cli binary (skipped when it is not installed) with the
// canonical GoldenSemanticOverlayJobV1 workload — the SAME golden content as
// TestProcessGoldenOverlayJobV1 but expressed in PipelineGen's semantic
// renderinggen.overlay-plan.v1 contract:
//
//	background.jpg (IMAGE_OVERLAY, full 5s, cover)
//	+ "QUESTO CAMBIA TUTTO" (IMPORTANT_PHRASE, caption_card, f20-60)
//	+ "APPLE"               (IMPORTANT_WORD,   active_word_pop, f65-95)
//	+ apple.png             (IMAGE_OVERLAY, image_focus_in, f90-135)
//
// The worker must lower the semantic plan (CompileIfSemantic), materialize the
// content-addressed asset_refs, write the concrete plan.json, render with the
// real CLI and publish the MP4 — one pipeline, no separate renderer.
func TestProcessGoldenSemanticOverlayJobV1(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	store := storage.New(storage.NewMemory(), storage.Options{})
	renderer := &capturingRenderer{Renderer: cli}
	proc := New(t.TempDir(), "software", cli.Version(), "http://store:9000", store, renderer)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)

	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenSemanticOverlayJobV1), &job); err != nil {
		t.Fatalf("decode GoldenSemanticOverlayJobV1: %v", err)
	}
	if job.ID != "golden-semantic-overlay-v1" || job.Schema != queue.JobSchemaV1 {
		t.Fatalf("unexpected golden job envelope: %+v", job)
	}
	seedGoldenAssets(t, store, job.Assets)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	artifact, err := proc.Process(ctx, &job)
	if err != nil {
		t.Fatalf("process semantic golden: %v", err)
	}

	// The artifact ledger row: DB artifact step (hash == objectstore key,
	// probed media facts, semantic counters from the compiled plan).
	rec, ok := ledger.Get(job.ID)
	if !ok {
		t.Fatal("ledger has no record for the golden job")
	}
	if rec.ArtifactHash != artifact.ArtifactHash || rec.StorageKey != artifact.StorageKey || rec.ArtifactHash == "" {
		t.Fatalf("ledger identity mismatch: %+v vs %+v", rec, artifact)
	}
	if rec.Width != 1280 || rec.Height != 720 || rec.DurationUS <= 0 || rec.FrameCount != 150 {
		t.Fatalf("ledger probe facts: %+v", rec)
	}
	if rec.Codec == "" || rec.Container == "" {
		t.Fatalf("ledger missing codec/container: %+v", rec)
	}
	// The golden plan carries phrase + word + 2 image overlays (background
	// and apple), no entities: the counters must reflect PipelineGen's plan.
	if rec.ImportantPhraseCnt != 1 || rec.ImportantWordCnt != 1 || rec.ImageCount != 2 || rec.EntityCount != 0 {
		t.Fatalf("ledger semantic counters: %+v", rec)
	}
	if rec.InputBytes <= 0 || rec.OutputBytes != artifact.SizeBytes {
		t.Fatalf("ledger byte counts: %+v", rec)
	}
	if rec.OverlayCompileUS <= 0 || rec.SHA256US <= 0 || rec.ObjectStoreUploadUS <= 0 || rec.TotalUS <= 0 {
		t.Fatalf("ledger phase metrics: %+v", rec)
	}

	if artifact.Kind != "segment" || artifact.ContentType != "video/mp4" {
		t.Fatalf("artifact type: kind=%q content_type=%q", artifact.Kind, artifact.ContentType)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash || artifact.SizeBytes <= 0 {
		t.Fatalf("artifact identity: %+v", artifact)
	}
	if artifact.Backend != "software" {
		t.Fatalf("artifact backend = %q", artifact.Backend)
	}

	// The plan the renderer received must be the CONCRETE Chronon plan: the
	// semantic golden proves the full CompileIfSemantic -> plan.json path.
	if len(renderer.planJSON) == 0 {
		t.Fatal("renderer never received a plan.json")
	}
	var concrete struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
		Canvas  struct {
			Width          int   `json:"width"`
			Height         int   `json:"height"`
			FPSNum         int   `json:"fps_num"`
			FPSDen         int   `json:"fps_den"`
			DurationFrames int64 `json:"duration_frames"`
		} `json:"canvas"`
		Layers []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Text      string `json:"text"`
			Preset    string `json:"preset"`
			Animation *struct {
				Tracks []struct{} `json:"tracks"`
			} `json:"animation"`
			Asset string `json:"asset"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(renderer.planJSON, &concrete); err != nil {
		t.Fatalf("compiled plan.json does not decode: %v", err)
	}
	if concrete.Schema != "chronon.render-plan" || concrete.Version != 2 {
		t.Fatalf("compiled plan is not chronon.render-plan.v2: %+v", concrete)
	}
	if concrete.Canvas.Width != 1280 || concrete.Canvas.Height != 720 || concrete.Canvas.FPSNum != 30 || concrete.Canvas.FPSDen != 1 || concrete.Canvas.DurationFrames != 150 {
		t.Fatalf("compiled canvas (want 1280x720@30, 150 frames): %+v", concrete.Canvas)
	}
	presets := map[string]string{}
	for _, layer := range concrete.Layers {
		presets[layer.ID] = layer.Preset
	}
	if presets["important_phrase"] != "caption_card" {
		t.Fatalf("important_phrase preset = %q, want caption_card", presets["important_phrase"])
	}
	if presets["important_word"] != "active_word_pop" {
		t.Fatalf("important_word preset = %q, want active_word_pop", presets["important_word"])
	}
	if presets["image_overlay_image"] != "image_focus_in" {
		t.Fatalf("image_overlay preset = %q, want image_focus_in", presets["image_overlay_image"])
	}

	// The rendered MP4 was published to the artifact store.
	stored, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		t.Fatalf("get published artifact: %v", err)
	}
	if len(stored) == 0 || int64(len(stored)) != artifact.SizeBytes {
		t.Fatalf("published artifact size %d != artifact size %d", len(stored), artifact.SizeBytes)
	}
}

// seedGoldenAssets asserts each asset hash in the job matches the deterministic
// fixture file on disk (golden immutability) and seeds the bytes into the
// artifact store under that hash, so materialize resolves them from L3.
func seedGoldenAssets(t *testing.T, store *storage.Client, assets []queue.AssetRef) {
	t.Helper()
	for _, a := range assets {
		// logical_path is assets/<file>; preserve the subdirectory in the
		// fixture tree (notably assets/fonts/Poppins-Bold.ttf). Using only
		// filepath.Base would silently look for fonts at the wrong level and
		// make the real Chronon render fail with a missing logical asset.
		logical := filepath.Clean(a.LogicalPath)
		logical = strings.TrimPrefix(logical, "assets"+string(filepath.Separator))
		path := filepath.Join("..", "..", "..", "testdata", "golden", logical)
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
