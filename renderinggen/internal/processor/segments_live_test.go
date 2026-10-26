package processor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// segments_live_test.go is the LIVE rung of the P4 per-segment retry contract
// (docs/operations/TICKET-BATCH-SUPERJOB.md, gate "Retry indotto nel percorso
// live N-MP4, con prova che gli altri segmenti non vengano rielaborati").
//
// The offline tests (segments_test.go) pin the retry MECHANICS with synthetic
// MP4s; this file proves the induced failure on the real data plane: one warm
// daemon, one segmented session producing N real NVENC MP4s with their own
// receipts, one deliberately broken segment, and then the worker's own
// verification cycle — which must repair ONLY the broken item through the same
// daemon boundary, leaving every already-verified sibling untouched.

// liveSegmentedPlan is the asset-free smoke plan at `frames` frames — the same
// plan shape the daemon segment tests render, kept local so this package's
// live tests do not depend on another package's fixtures.
func liveSegmentedPlan(frames int) string {
	return fmt.Sprintf(`{
  "schema": "chronon.render-plan.v2",
  "version": 2,
  "job_id": "segmented-live-retry",
  "canvas": { "width": 320, "height": 180, "fps_num": 30, "fps_den": 1, "duration_frames": %d },
  "layers": [
    { "id": "background", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] }
  ],
  "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
}`, frames)
}

// liveChrononBinary resolves the chronon3d_cli the live segment tests execute,
// skipping visibly when no binary is installed (the offline suites stay the
// gate for the mechanics).
func liveChrononBinary(t *testing.T) string {
	t.Helper()
	if override := os.Getenv("CHRONON_BINARY"); override != "" {
		return override
	}
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	binary := (&chronon.Client{Home: home}).Binary()
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("chronon3d_cli not available at %s (set CHRONON_HOME or CHRONON_BINARY): %v", binary, err)
	}
	return binary
}

// recordingRenderer counts the renders that reach the live lane and delegates
// every one of them to the daemon pool, so "the other segments were not
// reprocessed" is a fact about the real transport, not about a fake.
type recordingRenderer struct {
	inner chronon.Renderer
	reqs  []chronon.RenderRequest
}

func (r *recordingRenderer) Render(ctx context.Context, req chronon.RenderRequest) error {
	r.reqs = append(r.reqs, req)
	return r.inner.Render(ctx, req)
}

// TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment runs the P4
// per-segment retry contract on the real data plane:
//
//  1. one warm daemon session renders the plan into N per-item MP4s plus the
//     whole-timeline mirror (the batch's fused clip), each item with its own
//     receipt;
//  2. ONE segment is then broken on disk (bytes replaced after the receipt was
//     written — a crash between verify and publish), leaving its siblings
//     valid;
//  3. the worker's own verification cycle (finalizeSegments) must re-render
//     ONLY the broken item's window through the same daemon, verify and store
//     every segment, and never touch the bytes of the segments that already
//     verified.
//
// Gated like every live chronon test (CHRONON_BINARY / CHRONON_HOME); without
// a usable binary it skips visibly.
func TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not available: %v", err)
	}
	binary := liveChrononBinary(t)

	const frames, items, failed = 12, 3, 1
	dir := t.TempDir()
	assetsRoot := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(liveSegmentedPlan(frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(dir, "output", "result.mp4")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	per := int64(frames / items)
	segments := make([]chronon.OutputSegment, 0, items)
	for i := 0; i < items; i++ {
		segments = append(segments, chronon.OutputSegment{
			OutputPath: filepath.Join(dir, "output", fmt.Sprintf("segment-%03d.mp4", i)),
			StartFrame: int64(i) * per,
			EndFrame:   int64(i+1) * per,
		})
	}
	// The production P4 shape: the strict GPU-native lane, the only lane that
	// owns the segmented mux writer.
	requirements := chronon.ExecutionRequirements{
		Backend:             "vulkan",
		GPURequired:         true,
		CompositionRequired: true,
		CPUFallbackAllowed:  false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	base := filepath.Join(t.TempDir(), "chronon.sock")
	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase: base, Count: 1, LanesPerDaemon: 1, Binary: binary,
		AssetsRoot: assetsRoot, Backend: "vulkan", GPUDevice: 0,
		Stdout: os.Stderr, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("start daemon pool: %v", err)
	}
	defer pool.Shutdown(context.Background())

	// 1. One live segmented session: N per-item MP4s + the mirror, real receipts.
	if err := pool.Render(ctx, chronon.RenderRequest{
		PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
		OutputSegments: segments, TotalFrames: frames,
		Report: true, ReceiptVerify: chronon.ReceiptVerifyFast,
		Requirements: requirements,
	}); err != nil {
		t.Fatalf("live segmented session: %v", err)
	}
	before := make([]string, items)
	for i := range segments {
		digest, _, err := hashio.File(segments[i].OutputPath)
		if err != nil {
			t.Fatalf("hash segment %d: %v", i, err)
		}
		before[i] = digest
	}

	// 2. Break exactly one item's bytes. Its receipt still claims the
	// pre-corruption file, which is precisely the failure a crash between
	// verify and publish leaves behind.
	if err := os.WriteFile(segments[failed].OutputPath, []byte("broken segment bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	proc, store, _ := newProcessor(t)
	recorder := &recordingRenderer{inner: pool}
	proc.renderer = recorder
	prepared := &PreparedJob{
		Job:        &queue.Job{ID: "live-induced-retry"},
		Plan:       &overlay.Plan{Canvas: overlay.Canvas{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, DurationFrames: frames}},
		OutputPath: mirror,
		Metrics:    map[string]float64{},
		Segments:   segments,
		SegmentItemIDs: []string{
			"item-a", "item-b", "item-c",
		},
		baseRenderRequest: chronon.RenderRequest{
			PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
			TotalFrames:   frames,
			Report:        true,
			ReceiptVerify: chronon.ReceiptVerifyFast,
			Requirements:  requirements,
		},
	}

	// 3. The worker's own per-segment verification cycle.
	entries, err := proc.finalizeSegments(ctx, prepared)
	if err != nil {
		t.Fatalf("finalize segments: %v", err)
	}
	if len(entries) != items {
		t.Fatalf("entries = %d, want %d", len(entries), items)
	}

	// The induced retry must be EXACTLY one render of the failed window.
	// Any second render is the sibling-reprocessing failure the contract
	// forbids ("un segmento fallito non invalida la sessione né gli altri
	// segmenti").
	if len(recorder.reqs) != 1 {
		t.Fatalf("renders reaching the daemon = %d, want exactly 1 (only the failed segment may be re-rendered): %+v",
			len(recorder.reqs), recorder.reqs)
	}
	retry := recorder.reqs[0]
	if retry.OutputPath != segments[failed].OutputPath ||
		!retry.RangeEnabled || retry.FirstFrame != segments[failed].StartFrame ||
		retry.LastFrame != segments[failed].EndFrame-1 || retry.TotalFrames != per {
		t.Fatalf("retry request = %q range=%v [%d,%d] of %d, want %q [%d,%d) of %d",
			retry.OutputPath, retry.RangeEnabled, retry.FirstFrame, retry.LastFrame, retry.TotalFrames,
			segments[failed].OutputPath, segments[failed].StartFrame, segments[failed].EndFrame, per)
	}
	if len(retry.OutputSegments) != 0 {
		t.Fatalf("retry must not carry the session's per-item windows: %+v", retry.OutputSegments)
	}
	if retry.PlanPath != planPath {
		t.Fatalf("retry must reuse the session's plan, got %q", retry.PlanPath)
	}

	// Proof the siblings were not reprocessed: their bytes are the session's
	// bytes, untouched by the retry.
	for i := range segments {
		digest, _, err := hashio.File(segments[i].OutputPath)
		if err != nil {
			t.Fatalf("hash segment %d after retry: %v", i, err)
		}
		if i == failed {
			if digest == before[i] {
				t.Errorf("broken segment %d still carries the corrupted bytes: the single-segment retry did not repair it", i)
			}
			continue
		}
		if digest != before[i] {
			t.Errorf("sibling segment %d was re-rendered (%s -> %s): only the failed segment may be reprocessed", i, before[i], digest)
		}
	}

	// Every segment is verified and stored under its own content address, and
	// the live NVENC segments each start on a keyframe (the segmented mux
	// writer fails closed otherwise).
	for i, entry := range entries {
		if entry.ItemID != prepared.SegmentItemIDs[i] {
			t.Fatalf("entry %d item = %q, want %q", i, entry.ItemID, prepared.SegmentItemIDs[i])
		}
		if want := i == failed; entry.Retried != want {
			t.Fatalf("entry %d retried = %v, want %v", i, entry.Retried, want)
		}
		if entry.FrameCount != int(per) {
			t.Fatalf("entry %d frames = %d, want its window's %d", i, entry.FrameCount, per)
		}
		if !entry.FirstFrameKeyframe {
			t.Errorf("entry %d does not start on a keyframe: a segment must open with an IDR", i)
		}
		digest, size, err := hashio.File(segments[i].OutputPath)
		if err != nil {
			t.Fatalf("hash entry %d: %v", i, err)
		}
		if entry.SHA256 != digest || entry.SizeBytes != size {
			t.Fatalf("entry %d identity = %s/%d, want %s/%d", i, entry.SHA256, entry.SizeBytes, digest, size)
		}
		if _, err := store.Get(ctx, entry.SHA256); err != nil {
			t.Fatalf("stored segment %d: %v", i, err)
		}
	}
}
