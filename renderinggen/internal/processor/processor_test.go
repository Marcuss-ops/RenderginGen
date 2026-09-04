package processor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// videoHash is the real SHA-256 of "video-bytes": the semantic compiler
// validates the source ref's hash format fail-closed (asset registry), and
// materialization verifies object bytes against it.
const videoHash = "79fd615a866fe7f9eb4da8d9c41ab57e3bd48056df42fd2c13e4d461a87afbe3"

type fakeRenderer struct {
	req   chronon.RenderRequest
	err   error
	write func(path string) error
	calls int
}

func TestHasVisualOverlayDistinguishesVideoOnlyFromAuthoredComposition(t *testing.T) {
	if hasVisualOverlay([]byte(`{"layers":[{"type":"video"}]}`)) {
		t.Fatal("video-only plan must use direct-yuv")
	}
	if !hasVisualOverlay([]byte(`{"layers":[{"type":"text"}]}`)) {
		t.Fatal("text-only plan must use native composition")
	}
	if !hasVisualOverlay([]byte(`{"layers":[{"type":"image"}]}`)) {
		t.Fatal("image-only plan must use native composition")
	}
}

func (f *fakeRenderer) Render(_ context.Context, req chronon.RenderRequest) error {
	f.calls++
	f.req = req
	if f.err != nil {
		return f.err
	}
	if f.write != nil {
		return f.write(req.OutputPath)
	}
	return nil
}

func newProcessor(t *testing.T) (*Processor, *storage.Client, *fakeRenderer) {
	t.Helper()
	store := storage.New(storage.NewMemory(), storage.Options{})
	renderer := &fakeRenderer{}
	proc := New(t.TempDir(), "software", "0.1.0", "http://store:9000", store, renderer)
	return proc, store, renderer
}

func validJob() *queue.Job {
	return &queue.Job{
		ID:      "video-983",
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		// The worker only executes the semantic overlay-plan contract lowered
		// by the compiler; the historical concrete-plan pass-through is gone.
		RenderPlan: json.RawMessage(`{
		  "schema_version":"renderinggen.overlay-plan.v1",
		  "plan_id":"video-983","video_id":"video-983",
		  "width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":1000,
		  "source":{"asset_id":"video-983","sha256":"` + videoHash + `"}
		}`),
		Assets: []queue.AssetRef{
			{Hash: videoHash, LogicalPath: "videos/base.mp4"},
		},
	}
}

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

func TestPrepareCompilesAndStoresPlanWithoutInvokingChronon(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	job := validJob()
	job.ID = "overlay-prepare-1"

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("prepare invoked Chronon %d times", renderer.calls)
	}
	if artifact.Kind != "overlay_prepare" || artifact.ContentType != "application/json" {
		t.Fatalf("prepare artifact = %+v", artifact)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash || artifact.SizeBytes <= 0 {
		t.Fatalf("prepare artifact identity incomplete: %+v", artifact)
	}
	data, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("get prepared plan: %v", err)
	}
	if int64(len(data)) != artifact.SizeBytes {
		t.Fatalf("prepared plan size = %d, artifact says %d", len(data), artifact.SizeBytes)
	}
}

func TestPrepareAcceptsPipelineGenOverlayIntentWarmup(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	job := validJob()
	job.ID = "overlay-prepare-intents"
	job.Assets = nil
	job.JobType = queue.JobTypeOverlayPrepare
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare intent warmup: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("prepare invoked Chronon %d times", renderer.calls)
	}
	if artifact.Kind != "overlay_prepare" || artifact.SizeBytes <= 0 {
		t.Fatalf("unexpected prepare artifact: %+v", artifact)
	}
	if _, err := store.Get(context.Background(), artifact.StorageKey); err != nil {
		t.Fatalf("prepared intent document was not stored: %v", err)
	}
}

// TestProcessExecutesSemanticOverlayPlan verifies the full semantic path in
// one worker run: CompileIfSemantic lowers the PipelineGen overlay-plan.v1
// into the concrete chronon.render-plan.v1, the content-addressed asset_refs
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
	if concrete.Layers[1].ID != "img-1_image" || concrete.Layers[1].Preset != "" {
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

// TestPublishUpdatesLedgerDriveMetric verifies a publication retry updates
// drive_upload_us in the ledger without re-rendering (RENDERED ->
// PUBLISH_RETRY -> PUBLISHED, never a Chronon re-render).
func TestPublishUpdatesLedgerDriveMetric(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	rec, _ := ledger.Get(validJob().ID)
	if rec.DriveUploadUS != 0 {
		t.Fatalf("drive_upload_us before publish = %d, want 0", rec.DriveUploadUS)
	}

	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))
	if _, err := proc.Publish(context.Background(), validJob().ID, artifact); err != nil {
		t.Fatalf("publish: %v", err)
	}
	rec, _ = ledger.Get(validJob().ID)
	if rec.DriveUploadUS <= 0 {
		t.Fatalf("drive_upload_us after publish = %d, want > 0", rec.DriveUploadUS)
	}
	if rec.ArtifactHash != artifact.ArtifactHash {
		t.Fatalf("drive update must not touch artifact identity: %+v", rec)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1 (no re-render on publish retry)", renderer.calls)
	}
}

func TestProcessValidation(t *testing.T) {
	proc, _, _ := newProcessor(t)

	mutate := func(fn func(*queue.Job)) *queue.Job {
		j := validJob()
		fn(j)
		return j
	}

	cases := []struct {
		name string
		job  *queue.Job
	}{
		{"nil", nil},
		{"missing id", mutate(func(j *queue.Job) { j.ID = "" })},
		{"bad schema", mutate(func(j *queue.Job) { j.Schema = "other" })},
		{"bad version", mutate(func(j *queue.Job) { j.Version = 2 })},
		{"empty render plan", mutate(func(j *queue.Job) { j.RenderPlan = nil })},
		{"invalid render plan", mutate(func(j *queue.Job) { j.RenderPlan = json.RawMessage("{") })},
		{"asset missing hash", mutate(func(j *queue.Job) { j.Assets[0].Hash = "" })},
		{"asset missing logical path", mutate(func(j *queue.Job) { j.Assets[0].LogicalPath = "" })},
	}
	for _, tc := range cases {
		if _, err := proc.Process(context.Background(), tc.job); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

func TestProcessRenderError(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.err = errors.New("render failed")

	if _, err := proc.Process(context.Background(), validJob()); err == nil {
		t.Fatal("expected render error")
	}
	if _, err := os.Stat(renderer.req.AssetsRoot); !os.IsNotExist(err) {
		t.Fatalf("workspace not cleaned up after render error, stat err = %v", err)
	}
}

func TestRenderThenPublishRetrySkipsRender(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	proc.SetPublisher(drive.NewMock(t.TempDir(), 1)) // first upload fails

	// Render succeeds and stores the artifact in the object store.
	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if artifact.StorageKey == "" || artifact.ArtifactHash == "" {
		t.Fatalf("artifact not stored: %+v", artifact)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1", renderer.calls)
	}

	// First publication fails (simulated Drive failure).
	if _, err := proc.Publish(context.Background(), validJob().ID, artifact); err == nil {
		t.Fatal("expected publish error on first upload")
	}

	// The publication retry succeeds and never touches the renderer.
	published, err := proc.Publish(context.Background(), validJob().ID, artifact)
	if err != nil {
		t.Fatalf("publish retry: %v", err)
	}
	if published.DriveFileID == "" || published.DriveLink == "" {
		t.Fatalf("drive fields not set after retry: %+v", published)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer re-ran on publish retry: calls = %d, want 1", renderer.calls)
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// corruptBackend is an L3 object store that returns corrupted bytes for every
// fetch, simulating a real corruption beneath the cache (the cache never
// overwrites a content-addressed key, so a test must corrupt at the backend).
type corruptBackend struct{}

func (corruptBackend) Fetch(_ context.Context, key string) ([]byte, error) {
	return []byte("corrupted-bytes"), nil
}
func (corruptBackend) Store(_ context.Context, key string, data []byte) error { return nil } // badSizePublisher reports an incorrect upload size, so publication fails
// without relying on a provider-specific hash response.
type badSizePublisher struct{}

func (badSizePublisher) Publish(_ context.Context, req drive.PublishRequest) (drive.Result, error) {
	return drive.Result{
		FileID:      "lying-file",
		WebViewLink: "https://drive.example.com/file/d/lying-file",
		SizeBytes:   fileSize(req.Path) + 1,
	}, nil
}

// TestPublishEnforcesStoreDBInvariant proves the store_sha == db_sha leg of
// the chain: if the object store returns bytes that do not hash to the
// artifact hash the worker recorded (corruption), Publish must fail BEFORE
// anything is uploaded to Drive.
func TestPublishEnforcesStoreDBInvariant(t *testing.T) {
	// A backend that returns corrupted bytes for every fetch: the store cache
	// never overwrites a content-addressed key, so the corruption must be
	// simulated at the L3 backend where a real object store could drift. The
	// artifact key is NOT in the L1/L2 cache (it was never Put through this
	// client), so Get falls through to the corrupted backend.
	store := storage.New(corruptBackend{}, storage.Options{})
	proc := New(t.TempDir(), "software", "0.1.0", "http://store:9000", store, &fakeRenderer{})
	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))

	// A claimed rendered job whose stored bytes no longer match the recorded
	// hash (the object store drifted from what the worker hashed).
	artifact := queue.Artifact{
		Kind:         "segment",
		StorageKey:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactHash: storage.Hash([]byte("expected-bytes")),
		ContentType:  "video/mp4",
	}
	if _, err := proc.Publish(context.Background(), "job-1", artifact); err == nil {
		t.Fatal("publish must fail when store bytes do not match db_sha")
	}
}

// TestPublishEnforcesDriveInvariant proves the provider identity leg of the
// chain: a publisher that reports an incorrect uploaded size fails the job.
func TestPublishEnforcesDriveInvariant(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	proc.SetPublisher(badSizePublisher{})

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := proc.Publish(context.Background(), validJob().ID, artifact); err == nil {
		t.Fatal("publish must fail when drive_sha != db_sha")
	}
}

// TestPublishSHAChainInvariant walks the whole chain end to end — render,
// store, ledger and Drive — and proves local_sha == store_sha == db_sha ==
// drive_sha: the hashes recorded in the artifact, the ledger, and the Drive
// result are all the same, and the bytes Drive received are the bytes the
// worker hashed.
func TestPublishSHAChainInvariant(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	driveDir := t.TempDir()
	publisher := drive.NewMock(driveDir, 0)
	proc.SetPublisher(publisher)
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	published, err := proc.Publish(context.Background(), validJob().ID, artifact)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// local_sha == store_sha: the object store holds the hashed bytes.
	localSHA := storage.Hash([]byte("output-bytes"))
	stored, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("get stored: %v", err)
	}
	if got := storage.Hash(stored); got != localSHA {
		t.Fatalf("store_sha = %s, want %s", got, localSHA)
	}
	// db_sha: the ledger record carries the same hash.
	rec, ok := ledger.Get(validJob().ID)
	if !ok {
		t.Fatal("ledger record missing")
	}
	if rec.ArtifactHash != localSHA || rec.StorageKey != artifact.StorageKey {
		t.Fatalf("db_sha mismatch: rec=%+v", rec)
	}
	// drive_sha: the publisher reported the same hash and wrote the bytes.
	if published.DriveFileID == "" {
		t.Fatalf("drive fields missing: %+v", published)
	}
	driveFiles, err := filepath.Glob(filepath.Join(driveDir, artifact.ArtifactHash, "*.mp4"))
	if err != nil || len(driveFiles) != 1 {
		t.Fatalf("drive files = %v (err %v), want exactly one", driveFiles, err)
	}
	written, err := os.ReadFile(driveFiles[0])
	if err != nil {
		t.Fatalf("read drive file: %v", err)
	}
	if got := storage.Hash(written); got != localSHA {
		t.Fatalf("drive file sha = %s, want %s", got, localSHA)
	}
}

// TestPrepareSelfHealsMissingEntityImageFromURL certifies Gate 4's real gap:
// PipelineGen materializes a catalog entity image to Drive and enqueues
// overlay.prepare BEFORE the bytes are staged in the RenderingGen object
// store. The worker must download the asset from its source URL, verify the
// SHA-256, stage it into L3 and still complete the prepare job — never fail
// with "object not found" on the first attempt.
func TestPrepareSelfHealsMissingEntityImageFromURL(t *testing.T) {
	proc, store, _ := newProcessor(t)

	imageBytes := []byte("michael-jordan-catalog-image-jpeg")
	imageHash := storage.Hash(imageBytes)

	// A real HTTP source for the catalog image; nothing is staged in L3.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(imageBytes)
	}))
	defer srv.Close()

	job := validJob()
	job.ID = "overlay-prepare-self-heal"
	job.JobType = queue.JobTypeOverlayPrepare
	job.Assets = []queue.AssetRef{{Hash: imageHash, LogicalPath: srv.URL + "/michael-jordan.jpg"}}
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare must self-heal the missing asset: %v", err)
	}
	if artifact.Kind != "overlay_prepare" {
		t.Fatalf("prepare artifact = %+v", artifact)
	}

	// The downloaded bytes were verified and staged into L3 under their hash.
	staged, err := store.Get(context.Background(), imageHash)
	if err != nil {
		t.Fatalf("self-healed asset was not staged into L3: %v", err)
	}
	if string(staged) != string(imageBytes) {
		t.Fatalf("staged asset = %q, want %q", staged, imageBytes)
	}
}

// TestPrepareSelfHealRejectsHashMismatch pins the fail-closed boundary: a URL
// whose bytes do not hash to the declared asset hash must fail resolution, and
// must never stage the wrong bytes into L3.
func TestPrepareSelfHealRejectsHashMismatch(t *testing.T) {
	proc, store, _ := newProcessor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong-bytes"))
	}))
	defer srv.Close()

	wantHash := storage.Hash([]byte("the-real-bytes"))
	job := validJob()
	job.ID = "overlay-prepare-self-heal-mismatch"
	job.JobType = queue.JobTypeOverlayPrepare
	job.Assets = []queue.AssetRef{{Hash: wantHash, LogicalPath: srv.URL + "/wrong.jpg"}}
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	if _, err := proc.Prepare(context.Background(), job); err == nil {
		t.Fatal("prepare must fail when the URL bytes do not match the declared hash")
	}
	if _, err := store.Get(context.Background(), wantHash); err == nil {
		t.Fatal("wrong bytes must never be staged into L3")
	}
}

func TestProcessMissingOutput(t *testing.T) {
	proc, store, _ := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	if _, err := proc.Process(context.Background(), validJob()); err == nil {
		t.Fatal("expected error when renderer does not produce output")
	}
}
