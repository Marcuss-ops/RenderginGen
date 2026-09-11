// Full-pipeline behavior tests: the whole Process run from a claimed job to a
// published artifact, including plan lowering, asset materialization, plan.json
// on disk and the artifact ledger.
package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

func TestProcessFullPipeline(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	// The renderer runs before the workspace is cleaned up, so capture the
	// plan.json and materialized asset content here (they are gone by the
	// time Process returns).
	var capturedPlan, capturedAsset []byte
	renderer.write = func(path string) error {
		var err error
		capturedPlan, err = os.ReadFile(renderer.req.PlanPath)
		if err != nil {
			return err
		}
		capturedAsset, err = os.ReadFile(filepath.Join(renderer.req.AssetsRoot, "assets", "videos", "base.mp4"))
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Process(context.Background(), validJob())
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	// Artifact metadata.
	wantHash := storage.Hash([]byte("output-bytes"))
	if artifact.ArtifactHash != wantHash || artifact.StorageKey != wantHash {
		t.Fatalf("hash mismatch: sha256=%q storage_key=%q want %q", artifact.ArtifactHash, artifact.StorageKey, wantHash)
	}
	if artifact.SizeBytes != int64(len("output-bytes")) {
		t.Fatalf("size = %d", artifact.SizeBytes)
	}
	if artifact.ContentType != "video/mp4" || artifact.Kind != "segment" {
		t.Fatalf("artifact type: mime=%q kind=%q", artifact.ContentType, artifact.Kind)
	}
	if artifact.ArtifactURL != "http://store:9000/objects/"+wantHash {
		t.Fatalf("artifact url = %q", artifact.ArtifactURL)
	}
	if artifact.Backend != "software" || artifact.ChrononVersion != "0.1.0" {
		t.Fatalf("artifact provenance: backend=%q chronon=%q", artifact.Backend, artifact.ChrononVersion)
	}

	// Renderer received the right contract.
	if renderer.req.Requirements.GPURequired {
		t.Fatalf("unexpected GPU requirement for software test: %+v", renderer.req.Requirements)
	}
	if renderer.req.PlanPath == "" || renderer.req.AssetsRoot == "" || renderer.req.OutputPath == "" {
		t.Fatalf("render request paths empty: %+v", renderer.req)
	}

	// plan.json is the compiled concrete Chronon plan, never the semantic
	// contract the job submitted.
	var submitted struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(capturedPlan, &submitted); err != nil {
		t.Fatalf("plan.json is not valid JSON: %v", err)
	}
	if submitted.Schema != "chronon.render-plan.v2" {
		t.Fatalf("plan.json schema = %q, want chronon.render-plan.v2", submitted.Schema)
	}

	// Assets were materialized at their logical path.
	if string(capturedAsset) != "video-bytes" {
		t.Fatalf("materialized asset = %q", capturedAsset)
	}

	// Output bytes were published to the artifact store.
	stored, err := store.Get(context.Background(), wantHash)
	if err != nil {
		t.Fatalf("get published artifact: %v", err)
	}
	if string(stored) != "output-bytes" {
		t.Fatalf("stored artifact = %q", stored)
	}

	// Workspace was cleaned up.
	if _, err := os.Stat(renderer.req.AssetsRoot); !os.IsNotExist(err) {
		t.Fatalf("workspace not cleaned up, stat err = %v", err)
	}
}

// TestProcessPreservesRawTimingSidecarReference verifies the deep-profile
// sidecar survives as a job diagnostic: the raw `<output>.timing.json`
// (including the unbounded frame_times_ms array) is preserved verbatim in the
// object store under its content address, the queue artifact carries only the
// small storage-key/url/sha reference, and the worker-local ledger mirrors it.
// The bounded ChrononTelemetry copy stays the ledger telemetry — the array is
// never inlined anywhere.
func TestProcessPreservesRawTimingSidecarReference(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	const rawTiming = `{
	  "exclusive_wall_timeline":{"startup_ms":565.0,"prepare_ms":1120.0,"render_loop_ms":2932.0,"process_wall_ms":5304.0},
	  "frame_times_ms":[{"frame":0,"wall_duration_ms":0.59},{"frame":1,"wall_duration_ms":0.62}]
	}`
	// The bounded telemetry summary sidecar (observability ownership, Phase
	// 10) is the ONLY document the worker ingests: schema-typed, bounded, no
	// per-frame arrays.
	const boundedSummary = `{
	  "schema": "chronon3d.render-telemetry-summary.v1",
	  "version": 1,
	  "summary": {"render_loop_fps": 60.0},
	  "job": {"process_wall_ms": 5304.0, "gpu": {"nvenc_frames": 150}},
	  "outcome": {"status": "ok"}
	}`
	renderer.write = func(path string) error {
		if err := os.WriteFile(path, []byte("output-bytes"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(path+".timing.json", []byte(rawTiming), 0o644); err != nil {
			return err
		}
		return os.WriteFile(path+".telemetry-summary.json", []byte(boundedSummary), 0o644)
	}

	artifact, err := proc.Process(context.Background(), validJob())
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	// The artifact carries a small content-addressed reference, never the
	// per-frame array itself.
	wantTimingHash := storage.Hash([]byte(rawTiming))
	if artifact.ChrononTimingStorageKey != wantTimingHash || artifact.ChrononTimingSHA256 != wantTimingHash {
		t.Fatalf("timing storage key/sha = %q/%q, want %q", artifact.ChrononTimingStorageKey, artifact.ChrononTimingSHA256, wantTimingHash)
	}
	if artifact.ChrononTimingSizeBytes != int64(len(rawTiming)) || artifact.ChrononTimingContentType != "application/vnd.chronon.timing+json" {
		t.Fatalf("timing size/type = %d/%q", artifact.ChrononTimingSizeBytes, artifact.ChrononTimingContentType)
	}
	if artifact.ChrononTimingURL != "http://store:9000/objects/"+wantTimingHash {
		t.Fatalf("timing url = %q", artifact.ChrononTimingURL)
	}
	if artifact.Metrics["chronon_timing_preserved"] != 1 || artifact.Metrics["chronon_timing_bytes"] != float64(len(rawTiming)) {
		t.Fatalf("timing preservation metrics missing: %+v", artifact.Metrics)
	}
	// The ledger telemetry is the BOUNDED summary document verbatim — never
	// the raw per-frame profile and never a mutated copy of it.
	if string(artifact.ChrononTelemetry) != boundedSummary {
		t.Fatalf("bounded ledger telemetry = %s, want the summary document verbatim", artifact.ChrononTelemetry)
	}
	if strings.Contains(string(artifact.ChrononTelemetry), "frame_times_ms") {
		t.Fatalf("bounded ledger telemetry must never inline the per-frame array: %s", artifact.ChrononTelemetry)
	}
	if artifact.Metrics["chronon_summary_render_loop_fps"] != 60.0 {
		t.Fatalf("documented summary metric missing: %+v", artifact.Metrics)
	}

	// The raw bytes (WITH the per-frame array) are fetchable from the store by
	// content address.
	storedTiming, err := store.Get(context.Background(), wantTimingHash)
	if err != nil {
		t.Fatalf("get preserved timing sidecar: %v", err)
	}
	if string(storedTiming) != rawTiming {
		t.Fatalf("preserved timing bytes = %q, want the raw sidecar verbatim", storedTiming)
	}

	// The worker-local ledger mirrors the reference.
	rec, ok := ledger.Get(validJob().ID)
	if !ok {
		t.Fatal("ledger has no record for the job")
	}
	if rec.ChrononTimingStorageKey != wantTimingHash || rec.ChrononTimingSHA256 != wantTimingHash || rec.ChrononTimingURL == "" {
		t.Fatalf("ledger timing reference mismatch: %+v", rec)
	}
	if rec.ChrononTimingSizeBytes != int64(len(rawTiming)) {
		t.Fatalf("ledger timing size = %d", rec.ChrononTimingSizeBytes)
	}
}

// TestProcessExecutesSemanticOverlayPlan verifies the full semantic path in
// one worker run: CompileIfSemantic lowers the PipelineGen overlay-plan.v1
// into the concrete chronon render-plan, the content-addressed asset_refs
// are materialized at their compiled logical paths, plan.json on disk is the
// CONCRETE plan (never the semantic one), Chronon renders, and the MP4 is
// published to the artifact store. One pipeline, no separate semantic
// renderer.
func TestProcessExecutesSemanticOverlayPlan(t *testing.T) {
	proc, store, renderer := newProcessor(t)

	// The semantic plan carries a content-addressed asset ref (sha256 of the
	// fixture bytes) exactly as PipelineGen emits it.
	assetBytes := []byte("apple-image-bytes")
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	job := &queue.Job{
		ID: "semantic-job", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"semantic-job","video_id":"video-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"Hello world","start_ms":0,"end_ms":1000},
            {"id":"img-1","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":1000,"end_ms":2000,
             "asset_refs":[{"asset_id":"apple","sha256":"` + assetHash + `","url":"https://store.example/objects/apple.png","media_type":"image/png"}]}
          ]
        }`),
	}

	var capturedPlan, capturedAsset []byte
	renderer.write = func(path string) error {
		var err error
		capturedPlan, err = os.ReadFile(renderer.req.PlanPath)
		if err != nil {
			return err
		}
		capturedAsset, err = os.ReadFile(filepath.Join(renderer.req.AssetsRoot, "assets", "semantic", "apple.png"))
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Process(context.Background(), job)
	if err != nil {
		t.Fatalf("process semantic plan: %v", err)
	}
	if artifact.Kind != "segment" || artifact.ContentType != "video/mp4" || artifact.ArtifactHash == "" {
		t.Fatalf("artifact: %+v", artifact)
	}

	// plan.json is the CONCRETE Chronon plan, never the semantic contract.
	if string(capturedPlan) == "" || !strings.Contains(string(capturedPlan), `"schema":"chronon.render-plan.v2"`) {
		t.Fatalf("plan.json is not the compiled concrete plan: %s", capturedPlan)
	}
	if strings.Contains(string(capturedPlan), "renderinggen.overlay-plan.v1") {
		t.Fatalf("plan.json still carries the semantic schema: %s", capturedPlan)
	}
	var concrete struct {
		Layers []struct {
			ID     string `json:"id"`
			Preset string `json:"preset"`
			Asset  string `json:"asset"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(capturedPlan, &concrete); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	if len(concrete.Layers) != 2 {
		t.Fatalf("compiled layers = %d, want 2", len(concrete.Layers))
	}
	if concrete.Layers[0].Preset != "" {
		t.Fatalf("phrase must not carry executable preset metadata: %q", concrete.Layers[0].Preset)
	}
	if concrete.Layers[1].ID != "img-1:image" || concrete.Layers[1].Preset != "" {
		t.Fatalf("image layer = %+v", concrete.Layers[1])
	}

	// The content-addressed asset was materialized at its compiled path.
	if string(capturedAsset) != string(assetBytes) {
		t.Fatalf("materialized asset = %q, want %q", capturedAsset, assetBytes)
	}

	// Output bytes were published to the artifact store.
	stored, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("get published artifact: %v", err)
	}
	if string(stored) != "output-bytes" {
		t.Fatalf("stored artifact = %q", stored)
	}
}

// TestProcessRecordsArtifactLedger verifies the DB artifact step: after the
// object store accepts the bytes, one ArtifactRecord is written with the
// content hash, the semantic counters from the compiled plan (entity/phrase/
// word/image counts + preset_id), the per-phase microsecond metrics and the
// input/output byte counts. The ledger hash must equal the object-store key
// (local_sha == objectstore_sha == db_sha invariant).
func TestProcessRecordsArtifactLedger(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)

	assetBytes := []byte("apple-image-bytes")
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	job := &queue.Job{
		ID: "ledger-job", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"ledger-job","video_id":"video-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"Hello world","start_ms":0,"end_ms":1000},
            {"id":"word-1","template_id":"IMPORTANT_WORD","preset_id":"active_word_pop","text":"APPLE","start_ms":1000,"end_ms":2000},
            {"id":"img-1","template_id":"IMAGE_OVERLAY","start_ms":2000,"end_ms":3000,"preset_id":"image_focus_in",
             "asset_refs":[{"asset_id":"apple","sha256":"` + assetHash + `","url":"https://store.example/objects/apple.png","media_type":"image/png"}]}
          ]
        }`),
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Process(context.Background(), job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	rec, ok := ledger.Get(job.ID)
	if !ok {
		t.Fatal("ledger has no record for the job")
	}
	if rec.ArtifactHash != artifact.ArtifactHash || rec.StorageKey != artifact.StorageKey {
		t.Fatalf("ledger identity mismatch: record=%+v artifact=%+v", rec, artifact)
	}
	if rec.ArtifactHash != storage.Hash([]byte("output-bytes")) {
		t.Fatalf("db_sha = %q, want sha256(output-bytes)", rec.ArtifactHash)
	}
	if rec.OutputBytes != int64(len("output-bytes")) {
		t.Fatalf("output_bytes = %d", rec.OutputBytes)
	}
	// Semantic counters from the compiled plan (section "DB metrics").
	if rec.EntityCount != 0 || rec.ImportantPhraseCnt != 1 || rec.ImportantWordCnt != 1 || rec.ImageCount != 1 {
		t.Fatalf("semantic counters: %+v", rec)
	}
	// PresetID records the first item's preset (phrase-1 = caption_card).
	if rec.PresetID != "caption_card" {
		t.Fatalf("preset_id = %q, want caption_card (first item's preset)", rec.PresetID)
	}
	// Input bytes: the content-addressed asset was materialized.
	if rec.InputBytes != int64(len(assetBytes)) {
		t.Fatalf("input_bytes = %d, want %d", rec.InputBytes, len(assetBytes))
	}
	// Per-phase metrics must be positive and consistent.
	if rec.OverlayCompileUS <= 0 || rec.AssetMaterializeUS <= 0 || rec.ChrononRenderUS <= 0 {
		t.Fatalf("phase metrics not recorded: %+v", rec)
	}
	if rec.SHA256US <= 0 || rec.ObjectStoreUploadUS <= 0 || rec.TotalUS <= 0 {
		t.Fatalf("store metrics not recorded: %+v", rec)
	}
	if rec.TotalUS < rec.ChrononRenderUS {
		t.Fatalf("total_us %d < chronon_render_us %d", rec.TotalUS, rec.ChrononRenderUS)
	}
}
