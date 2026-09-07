// Package processor test helpers and core pipeline behavior. The full-pipeline
// tests live in pipeline_full_test.go, prepare/self-heal tests in
// prepare_selfheal_test.go and the mirror/publish invariants in
// publish_invariants_test.go.
package processor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
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
	if planHasVisualOverlay(&overlay.Plan{Layers: []overlay.Layer{{Type: "video"}}}) {
		t.Fatal("video-only plan must use direct-yuv")
	}
	if planHasVisualOverlay(&overlay.Plan{Layers: []overlay.Layer{{Type: "video"}, {Type: "video"}}}) {
		t.Fatal("multi-video plan must use direct video composition")
	}
	if !planHasVisualOverlay(&overlay.Plan{Layers: []overlay.Layer{{Type: "text"}}}) {
		t.Fatal("text-only plan must use native composition")
	}
	if !planHasVisualOverlay(&overlay.Plan{Layers: []overlay.Layer{{Type: "image"}}}) {
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

// failingRecorder simulates an artifact mirror that is down: PostgreSQL queue
// completion stays authoritative, so a Record failure must never fail the
// render (see TestProcessMirrorFailureKeepsRenderCompleted).
type failingRecorder struct{}

func (failingRecorder) Record(context.Context, artifactdb.ArtifactRecord) error {
	return errors.New("artifact mirror unavailable")
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
		{"missing schema", mutate(func(j *queue.Job) { j.Schema = "" })},
		{"bad schema", mutate(func(j *queue.Job) { j.Schema = "other" })},
		{"missing version", mutate(func(j *queue.Job) { j.Version = 0 })},
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

func TestProcessMissingOutput(t *testing.T) {
	proc, store, _ := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	if _, err := proc.Process(context.Background(), validJob()); err == nil {
		t.Fatal("expected error when renderer does not produce output")
	}
}
