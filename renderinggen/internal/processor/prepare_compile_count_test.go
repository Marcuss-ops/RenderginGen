package processor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// countingResolveCompiler records every resolve the processor asks for and
// keeps the returned handle, so the test can prove the handle was lowered
// exactly once. CompileSemantic is the one-shot compile (resolve + lower); a
// prepare path that still called it would be counted separately.
type countingResolveCompiler struct {
	resolves     int
	oneShot      int
	lastResolved *overlay.PreparedResolve
}

func (c *countingResolveCompiler) CompileSemantic(raw []byte) (overlay.CompileResult, error) {
	c.oneShot++
	return overlay.CompileSemantic(raw)
}

func (c *countingResolveCompiler) ResolveSemanticForPrepare(raw []byte) (*overlay.PreparedResolve, error) {
	resolved, err := overlay.ResolveSemanticForPrepare(raw, overlay.CompileOptions{})
	if err != nil {
		return nil, err
	}
	c.resolves++
	c.lastResolved = resolved
	return resolved, nil
}

// TestPrepareJobCompilesOnceForAnOrdinaryJob pins the single-compilation
// contract: one resolve, one lower, and the lowered plan carries no transient
// geometry marker (the lower consumed the probed sizes, it did not run blind).
func TestPrepareJobCompilesOnceForAnOrdinaryJob(t *testing.T) {
	proc, store, _ := newProcessorWith(t, Options{})
	assetBytes := testImageJPEG(t, 1600, 900)
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	counter := &countingResolveCompiler{}
	proc.compiler = counter

	job := &queue.Job{
		ID: "compile-once-1", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"compile-once-1","video_id":"compile-once-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[{"id":"portrait","kind":"entity_image","template_id":"image_popup",
            "preset_id":"image_focus_in","image_preset_id":"image_focus_in",
            "entity_caption":"Ada Lovelace","caption_layout":"below",
            "start_ms":0,"end_ms":3000,"params":{"width":480,"height":480},
            "asset_refs":[{"asset_id":"portrait","sha256":"` + assetHash + `","url":"https://store.example/objects/portrait.jpg","media_type":"image/jpeg"}]}]
        }`),
	}
	prepared, err := proc.PrepareJob(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = prepared.Workspace.Cleanup() }()

	if counter.resolves != 1 {
		t.Fatalf("full_compile_count: resolve calls = %d, want 1", counter.resolves)
	}
	if got := prepared.Metrics[metricnames.OverlayFullCompileCount]; got != 1 {
		t.Fatalf("%s = %v, want 1 (one resolve + one lower)", metricnames.OverlayFullCompileCount, got)
	}
	if counter.oneShot != 0 {
		t.Fatalf("prepare path used the one-shot compile %d time(s), want 0", counter.oneShot)
	}
	if counter.lastResolved == nil || !counter.lastResolved.Lowered() {
		t.Fatalf("the single resolved plan was not lowered: %+v", counter.lastResolved)
	}
	// Lowering is single-shot; a second pass would append the item layers to
	// the same shell, so the handle must refuse it instead of double-compiling.
	if _, err := counter.lastResolved.LowerAndSeal(nil); err == nil {
		t.Fatal("the resolved plan was lowered twice; the single-pass boundary is gone")
	}
	for _, layer := range prepared.Plan.Layers {
		if layer.EntityImage || layer.DeriveWidthFromAsset {
			t.Fatalf("label %q still awaits source geometry: full_compile_count was not exactly 1", layer.ID)
		}
		if layer.Type == "image" && (layer.BoxWidth != 480 || layer.BoxHeight != 270) {
			t.Fatalf("image layer %q = %dx%d, want the probed 16:9 480x270 card", layer.ID, layer.BoxWidth, layer.BoxHeight)
		}
	}
}

// TestPrepareJobDoesNotCompileAPreparedPackage pins the zero-compilation case:
// a job carrying render.prepare's immutable package reuses its plan verbatim,
// so it pays no resolve (and therefore no lower) at all.
func TestPrepareJobDoesNotCompileAPreparedPackage(t *testing.T) {
	ctx := context.Background()
	proc, store, _ := newProcessorWith(t, Options{NativeOutputProfiles: true})
	if err := store.Put(ctx, videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("seed source asset: %v", err)
	}
	prepareJob := validJob()
	prepareJob.ID = "compile-zero-prepare"
	prepareJob.JobType = queue.JobTypeRenderPrepare
	prepareJob.RenderPlan = withOutputProfile(t, prepareJob.RenderPlan)
	artifact, err := proc.PrepareRenderPackage(ctx, prepareJob)
	if err != nil {
		t.Fatalf("prepare immutable package: %v", err)
	}

	counter := &countingResolveCompiler{}
	proc.compiler = counter

	child := validJob()
	child.ID = "compile-zero-chunk"
	child.JobType = queue.JobTypeRenderSegment
	child.RenderPlan = withOutputProfile(t, child.RenderPlan)
	child.PreparedRenderPackage = &queue.PreparedRenderPackageRef{
		PackageID: artifact.ArtifactHash, PlanSHA256: preparedRenderPlanSHA256(child.RenderPlan),
		AssetManifestSHA: preparedRenderAssetManifestSHA(child.Assets),
		CompilerVersion:  artifact.ChrononVersion, ProfileID: artifact.ProfileID, TotalFrames: 30,
	}
	child.FrameRange = &queue.FrameRange{Start: 0, End: 30}
	prepared, err := proc.PrepareJob(ctx, child)
	if err != nil {
		t.Fatalf("prepare chunk from immutable package: %v", err)
	}
	defer func() { _ = prepared.Workspace.Cleanup() }()

	if counter.resolves != 0 || counter.oneShot != 0 {
		t.Fatalf("full_compile_count = %d resolve + %d one-shot, want 0 (package plan is reused)", counter.resolves, counter.oneShot)
	}
	if got, ok := prepared.Metrics[metricnames.OverlayFullCompileCount]; !ok || got != 0 {
		t.Fatalf("%s = %v (present=%t), want an explicit 0 for a reused package", metricnames.OverlayFullCompileCount, got, ok)
	}
	rawPackage, err := store.Get(ctx, artifact.ArtifactHash)
	if err != nil {
		t.Fatalf("fetch package object: %v", err)
	}
	var pkg PreparedRenderPackageV1
	if err := json.Unmarshal(rawPackage, &pkg); err != nil {
		t.Fatalf("decode stored package: %v", err)
	}
	chunkPlan, err := prepared.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal reused plan: %v", err)
	}
	if string(chunkPlan) != string(pkg.Plan) {
		t.Fatal("chunk plan differs from the immutable prepared plan")
	}
}
