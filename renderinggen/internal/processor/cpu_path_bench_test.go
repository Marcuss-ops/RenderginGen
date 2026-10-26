package processor

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// These benchmarks size the CPU-bound Go halves of a render job so the
// "is the Go worker on the critical path?" question is answered with numbers
// against the GPU render wall (seconds). They are deterministic and do not
// need chronon3d_cli: the renderer is the in-process fake.

// benchSemanticPlan returns the canonical golden semantic plan, which is the
// same workload class the throughput gate renders (phrases, words, image
// overlays with content-addressed assets).
func benchSemanticPlan(tb testing.TB) json.RawMessage {
	tb.Helper()
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenSemanticOverlayJobV1), &job); err != nil {
		tb.Fatalf("decode GoldenSemanticOverlayJobV1: %v", err)
	}
	return job.RenderPlan
}

// BenchmarkOverlayCompileSemantic measures the pure semantic lowering
// (overlay-plan.v1 -> chronon.render-plan.v2): the overlay_compile phase's
// compiler core, without workspace or asset I/O.
func BenchmarkOverlayCompileSemantic(b *testing.B) {
	plan := benchSemanticPlan(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := overlay.CompileSemantic(plan); err != nil {
			b.Fatalf("compile: %v", err)
		}
	}
}

// BenchmarkHashioSHA256 measures the streaming SHA-256 pass on a 256 MiB
// payload — the size class of a rendered MP4 being content-addressed by
// storeArtifact when Chronon's receipt cannot prove the digest.
func BenchmarkHashioSHA256(b *testing.B) {
	const size = 256 << 20
	path := filepath.Join(b.TempDir(), "payload.bin")
	buf := make([]byte, 1<<20)
	if _, err := rand.Read(buf); err != nil {
		b.Fatalf("seed rand: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatalf("create payload: %v", err)
	}
	for written := 0; written < size; written += len(buf) {
		if _, err := f.Write(buf); err != nil {
			b.Fatalf("write payload: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		b.Fatalf("close payload: %v", err)
	}
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := hashio.File(path); err != nil {
			b.Fatalf("hash: %v", err)
		}
	}
}

// BenchmarkPrepareJob measures the whole CPU half of the pipeline the worker
// runs before a GPU lane is touched: validate + semantic compile + asset
// materialization (memory store) + plan/package marshal.
func BenchmarkPrepareJob(b *testing.B) {
	proc, store, _ := newProcessorWith(b, Options{})
	assetBytes := testImagePNG(b, 1600, 900)
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		b.Fatalf("put asset: %v", err)
	}
	job := &queue.Job{
		ID:      "bench-prepare",
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"bench-prepare","video_id":"video-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"Hello world","start_ms":0,"end_ms":1000},
            {"id":"img-1","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":1000,"end_ms":2000,
             "asset_refs":[{"asset_id":"apple","sha256":"` + assetHash + `","url":"https://store.example/objects/apple.png","media_type":"image/png"}]}
          ]
        }`),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prepared, err := proc.PrepareJob(context.Background(), job)
		if err != nil {
			b.Fatalf("prepare: %v", err)
		}
		if prepared != nil && prepared.Workspace != nil {
			_ = prepared.Workspace.Cleanup()
		}
	}
}
