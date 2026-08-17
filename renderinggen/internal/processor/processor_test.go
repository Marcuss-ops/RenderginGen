package processor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

type fakeRenderer struct {
	req   chronon.RenderRequest
	err   error
	write func(path string) error
	calls int
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
		ID:         "video-983",
		Schema:     queue.JobSchemaV1,
		Version:    queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{"schema":"chronon.render-plan","version":1}`),
		Assets: []queue.AssetRef{
			{Hash: "hash-video", LogicalPath: "videos/base.mp4"},
		},
	}
}

func TestProcessFullPipeline(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), "hash-video", []byte("video-bytes")); err != nil {
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
	if renderer.req.Backend != "software" {
		t.Fatalf("backend = %q", renderer.req.Backend)
	}
	if renderer.req.PlanPath == "" || renderer.req.AssetsRoot == "" || renderer.req.OutputPath == "" {
		t.Fatalf("render request paths empty: %+v", renderer.req)
	}

	// plan.json was written to disk.
	if string(capturedPlan) != `{"schema":"chronon.render-plan","version":1}` {
		t.Fatalf("plan.json = %q", capturedPlan)
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
	job := validJob()
	job.ID = "overlay-prepare-1"
	job.Assets = nil

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
      "width":1280,"height":720,"fps":30,
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

func TestProcessRejectsSemanticOverlayPlan(t *testing.T) {
	proc, _, _ := newProcessor(t)
	job := &queue.Job{
		ID: "semantic-job", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"semantic-job","video_id":"video-1",
          "width":1280,"height":720,"fps":30,
          "items":[{"id":"phrase-1","template_id":"IMPORTANT_PHRASE","text":"Hello world","start_ms":0,"end_ms":1000}]
        }`),
	}
	if _, err := proc.Process(context.Background(), job); err == nil {
		t.Fatal("semantic plan must be rejected: RenderingGen executes concrete plans, PipelineGen compiles them")
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
	if err := store.Put(context.Background(), "hash-video", []byte("video-bytes")); err != nil {
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
	if err := store.Put(context.Background(), "hash-video", []byte("video-bytes")); err != nil {
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

func TestProcessMissingOutput(t *testing.T) {
	proc, store, _ := newProcessor(t)
	if err := store.Put(context.Background(), "hash-video", []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	if _, err := proc.Process(context.Background(), validJob()); err == nil {
		t.Fatal("expected error when renderer does not produce output")
	}
}
