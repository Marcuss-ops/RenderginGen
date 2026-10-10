package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// countingL3 counts every central-object-store fetch. The L2 reuse contract is
// only provable by observing the L3 boundary: an L2 hit must not reach here.
type countingL3 struct {
	inner   storage.Backend
	fetches atomic.Int64
}

func (b *countingL3) Fetch(ctx context.Context, key string) ([]byte, error) {
	b.fetches.Add(1)
	return b.inner.Fetch(ctx, key)
}

func (b *countingL3) Store(ctx context.Context, key string, data []byte) error {
	return b.inner.Store(ctx, key, data)
}

// reuseJob is a semantic plan whose single image layer binds one
// content-addressed asset, with the texts differing so the two jobs are not the
// same composition.
func reuseJob(id, assetHash, text string) *queue.Job {
	return &queue.Job{
		ID:      id,
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"` + id + `","video_id":"` + id + `",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"` + text + `","start_ms":0,"end_ms":1000},
            {"id":"img-1","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":1000,"end_ms":2000,
             "asset_refs":[{"asset_id":"apple","sha256":"` + assetHash + `","url":"https://store.example/objects/apple.png","media_type":"image/png"}]}
          ]
        }`),
	}
}

// TestPrepareReusesContentAddressedAssetAcrossJobsFromL2 pins the cache-budget
// contract documented on artifact_store.l2_max_bytes: a repeated asset set is
// served from the content-addressed local mirror instead of being re-fetched
// from the object store on every job.
//
// Two different jobs bind the same sha256; the second job's materialization
// must come from the L2 file the first job promoted, with the L3 fetch counter
// unchanged. This is asserted through the real prepare path (semantic compile →
// asset merge → streaming resolution → workspace install), so a regression in
// the workspace resolver or in the L2-first policy fails here without needing a
// GPU or a Chronon daemon — the reason it does not live in the render
// benchmark, which skips when chronon3d_cli is absent.
func TestPrepareReusesContentAddressedAssetAcrossJobsFromL2(t *testing.T) {
	ctx := context.Background()
	assetBytes := testImagePNG(t, 1600, 900)
	assetHash := storage.Hash(assetBytes)

	// Seed the object store directly (no client.Put, which would warm the
	// caches and make the first job a guaranteed hit), then count the fetches
	// the worker's own cache client performs.
	inner := storage.NewMemory()
	if err := inner.Store(ctx, assetHash, assetBytes); err != nil {
		t.Fatalf("seed L3: %v", err)
	}
	l3 := &countingL3{inner: inner}
	store := storage.New(l3, storage.Options{
		L1MaxBytes: 1 << 20,
		L2Dir:      filepath.Join(t.TempDir(), "l2"),
		L2MaxBytes: 64 << 20,
	})
	proc := NewWithOptions(t.TempDir(), "software", "0.1.0", "http://store:9000", store, &fakeRenderer{}, Options{})

	workspaceAsset := func(prepared *PreparedJob) []byte {
		t.Helper()
		path, err := prepared.Workspace.AssetPath("assets/semantic/apple.png")
		if err != nil {
			t.Fatalf("resolve workspace asset: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workspace asset: %v", err)
		}
		return data
	}

	// Job 1: L2 miss → exactly one L3 fetch, promoted into the content-addressed
	// mirror.
	first, err := proc.PrepareJob(ctx, reuseJob("reuse-1", assetHash, "first"))
	if err != nil {
		t.Fatalf("prepare first job: %v", err)
	}
	defer func() { _ = first.Workspace.Cleanup() }()
	if got := string(workspaceAsset(first)); got != string(assetBytes) {
		t.Fatalf("job 1 materialized %d bytes that do not equal the stored object", len(got))
	}
	if got := l3.fetches.Load(); got != 1 {
		t.Fatalf("job 1 L3 fetches = %d, want exactly 1 (L2 is cold)", got)
	}
	stats := store.Stats()
	if stats.L3Fetches != 1 || stats.L2Hits != 0 {
		t.Fatalf("job 1 stats = %+v, want L3Fetches=1 L2Hits=0", stats)
	}

	// Job 2: the same asset set from a DIFFERENT job. A correct content-addressed
	// mirror serves it from L2 without touching L3 again.
	second, err := proc.PrepareJob(ctx, reuseJob("reuse-2", assetHash, "second"))
	if err != nil {
		t.Fatalf("prepare second job: %v", err)
	}
	defer func() { _ = second.Workspace.Cleanup() }()
	if got := string(workspaceAsset(second)); got != string(assetBytes) {
		t.Fatalf("job 2 materialized bytes differ from the content-addressed object")
	}
	if got := l3.fetches.Load(); got != 1 {
		t.Fatalf("job 2 re-fetched from L3: fetches = %d, want 1 (the repeated asset must be served from L2)", got)
	}
	stats = store.Stats()
	if stats.L3Fetches != 1 {
		t.Fatalf("L3 fetches after two jobs = %d, want 1", stats.L3Fetches)
	}
	if stats.L2Hits != 1 {
		t.Fatalf("L2 hits after two jobs = %d, want 1 (the second job's asset set)", stats.L2Hits)
	}
	if stats.L2PutErrors != 0 || stats.L2ReadErrors != 0 {
		t.Fatalf("the cache degraded instead of serving the repeated asset: %+v", stats)
	}
}
