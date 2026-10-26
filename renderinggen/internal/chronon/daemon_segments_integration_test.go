package chronon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// segmentedPlan is the asset-free smoke plan at `frames` frames. Longer than
// the two-frame receipt smoke: a segment window needs enough frames for a
// real per-item cut and for the per-item service cost to mean anything.
func segmentedPlan(frames int) string {
	return fmt.Sprintf(`{
  "schema": "chronon.render-plan.v2",
  "version": 2,
  "job_id": "segmented-smoke",
  "canvas": { "width": 320, "height": 180, "fps_num": 30, "fps_den": 1, "duration_frames": %d },
  "layers": [
    { "id": "background", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] }
  ],
  "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
}`, frames)
}

// segmentedSmokeSession writes the plan and returns the per-item windows that
// tile [0, frames) into `items` equal segments plus the mirror output path.
func segmentedSmokeSession(t *testing.T, frames, items int) (planPath, assetsRoot, mirror string, segments []OutputSegment) {
	t.Helper()
	dir := t.TempDir()
	assetsRoot = filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath = filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(segmentedPlan(frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	mirror = filepath.Join(dir, "output", "result.mp4")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	if frames%items != 0 {
		t.Fatalf("smoke session wants %d frames divisible into %d items", frames, items)
	}
	per := int64(frames / items)
	for i := 0; i < items; i++ {
		segments = append(segments, OutputSegment{
			OutputPath: filepath.Join(dir, "output", fmt.Sprintf("segment-%03d.mp4", i)),
			StartFrame: int64(i) * per,
			EndFrame:   int64(i+1) * per,
		})
	}
	return planPath, assetsRoot, mirror, segments
}

// strictNativeRequirements is the production P4 shape: the strict GPU-native
// contract, the only lane that owns the segmented mux writer. Go stamps
// {nvenc, native, require_gpu_native} and the daemon's own resolution agrees
// (no pipe_composition under strict GPU), so both authorities select one lane.
func strictNativeRequirements() ExecutionRequirements {
	return ExecutionRequirements{
		Backend:             "vulkan",
		GPURequired:         true,
		CompositionRequired: true,
		CPUFallbackAllowed:  false,
	}
}

// TestDaemonSegmentedRequestRefusesThePipeLane pins the live boundary failure
// the engine used to accept silently: a request whose lane resolves to the
// external FFmpeg pipe encoder (or to no lane at all, which the daemon
// resolves to pipe) cannot carry per-item windows. Before the refusal existed,
// this exact request rendered one mirror and ZERO segments while reporting
// success — the caller paid N single-item re-renders believing it ran one
// batch session. The refusal must happen before any render starts.
func TestDaemonSegmentedRequestRefusesThePipeLane(t *testing.T) {
	planPath, assetsRoot, mirror, segments := segmentedSmokeSession(t, 12, 3)
	for _, tc := range []struct {
		name         string
		requirements ExecutionRequirements
	}{
		{"software rendering resolves to the pipe encoder", ExecutionRequirements{Backend: "software", CompositionRequired: true}},
		{"no encoder lane declared", ExecutionRequirements{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRenderRequest(RenderRequest{
				PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
				OutputSegments: segments, TotalFrames: 12,
				Requirements: tc.requirements,
			})
			if err == nil || !strings.Contains(err.Error(), "native encoder lane") {
				t.Fatalf("pipe-lane segmented request accepted (or wrong error): %v", err)
			}
			// The mirror path must not exist: nothing was rendered for a
			// request the boundary refused.
			if _, statErr := os.Stat(mirror); statErr == nil {
				t.Fatalf("refused request left an output at %s", mirror)
			}
		})
	}
}

// TestDaemonRendersOneSessionIntoIndependentSegmentMP4s is the LIVE P4 data
// plane: one warm daemon, one render session on the native encoder lane, N
// per-item MP4s plus the whole-timeline mirror, each with its own receipt and
// its own declared frame window. It is the rung between the offline
// writer/finalizer tests (which prove the mechanics) and the golden live run
// (which needs the full PipelineGen stack): this proves the deployed engine +
// daemon boundary produce the N-MP4 contract end to end.
//
// Gated like every live chronon test: CHRONON_BINARY / CHRONON_HOME, skipped
// visibly when no binary is installed.
func TestDaemonRendersOneSessionIntoIndependentSegmentMP4s(t *testing.T) {
	binary := daemonBinary(t)
	const frames, items = 12, 3
	planPath, assetsRoot, mirror, segments := segmentedSmokeSession(t, frames, items)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	base := filepath.Join(t.TempDir(), "chronon.sock")
	pool, err := StartDaemonPool(ctx, DaemonOptions{
		SocketBase: base, Count: 1, LanesPerDaemon: 1, Binary: binary,
		AssetsRoot: assetsRoot, Backend: "vulkan", GPUDevice: 0,
		Stdout: os.Stderr, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("start daemon pool: %v", err)
	}
	defer pool.Shutdown(context.Background())

	if !pool.SupportsOutputSegments() {
		t.Fatal("the daemon IPC transport must carry per-item output windows")
	}
	start := time.Now()
	if err := pool.Render(ctx, RenderRequest{
		PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
		OutputSegments: segments, TotalFrames: frames,
		Report: true, ReceiptVerify: "fast",
		Requirements: strictNativeRequirements(),
	}); err != nil {
		t.Fatalf("segmented daemon render: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("segmented session: %d items in %s (%.0f ms/item service)", items, elapsed, float64(elapsed.Milliseconds())/float64(items))

	// The mirror is the batch's own fused clip: published, verified, receipted.
	if st, err := os.Stat(mirror); err != nil || st.Size() == 0 {
		t.Fatalf("mirror %s not published: %v", mirror, err)
	}
	assertMediaReceiptCertifies(t, mirror, "fast")

	// Every segment: published at its final path, its own receipt, its own
	// identity, and exactly the frames of its declared window.
	seen := make(map[string]bool, items)
	for i, segment := range segments {
		st, err := os.Stat(segment.OutputPath)
		if err != nil || st.Size() == 0 {
			t.Fatalf("segment %d (%s) not published: %v", i, segment.OutputPath, err)
		}
		assertMediaReceiptCertifies(t, segment.OutputPath, "fast")
		receipt, err := ReadMediaReceipt(segment.OutputPath)
		if err != nil {
			t.Fatalf("segment %d receipt: %v", i, err)
		}
		if want := segment.EndFrame - segment.StartFrame; receipt.Media.FrameCount != want {
			t.Errorf("segment %d frame_count = %d, want its window [%d,%d) = %d", i, receipt.Media.FrameCount, segment.StartFrame, segment.EndFrame, want)
		}
		if receipt.Output.SHA256 == "" || seen[receipt.Output.SHA256] {
			t.Errorf("segment %d has an empty or NON-independent identity: %q", i, receipt.Output.SHA256)
		}
		seen[receipt.Output.SHA256] = true
	}
	// No staging residue: the published paths are the consumer contract.
	for _, segment := range segments {
		if _, err := os.Stat(segment.OutputPath + ".partial.mp4"); err == nil {
			t.Errorf("staging residue left beside %s", segment.OutputPath)
		}
	}
}

// TestDaemonSegmentedServiceCostIsBelowPerItemSessions measures the P4 cost
// hypothesis on the live engine: one segmented session (one startup, one
// encoder session, N item outputs) against N independent single-item sessions
// of the same content. The ticket's gate is <= 1.1 s/item on the golden mix;
// this measures the engine's own service cost on a synthetic plan, so the
// number it reports is a per-item service measurement of the segmented path,
// NOT a golden-mix throughput certification. The assertion is the direction
// the architecture promises: the segmented session must not cost more per
// item than the sessions it replaces.
func TestDaemonSegmentedServiceCostIsBelowPerItemSessions(t *testing.T) {
	binary := daemonBinary(t)
	const frames, items = 30, 10
	planPath, assetsRoot, mirror, segments := segmentedSmokeSession(t, frames, items)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	base := filepath.Join(t.TempDir(), "chronon.sock")
	pool, err := StartDaemonPool(ctx, DaemonOptions{
		SocketBase: base, Count: 1, LanesPerDaemon: 1, Binary: binary,
		AssetsRoot: assetsRoot, Backend: "vulkan", GPUDevice: 0,
		Stdout: os.Stderr, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("start daemon pool: %v", err)
	}
	defer pool.Shutdown(context.Background())

	// A: one segmented session producing all N items + the mirror.
	segmentedStart := time.Now()
	if err := pool.Render(ctx, RenderRequest{
		PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
		OutputSegments: segments, TotalFrames: frames,
		Report: false, Requirements: strictNativeRequirements(),
	}); err != nil {
		t.Fatalf("segmented session: %v", err)
	}
	segmentedWall := time.Since(segmentedStart)

	// B: N independent sessions, one per item window (the historical path).
	singleStart := time.Now()
	for i, segment := range segments {
		if err := pool.Render(ctx, RenderRequest{
			PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: segment.OutputPath + ".single.mp4",
			RangeEnabled: true, FirstFrame: segment.StartFrame, LastFrame: segment.EndFrame - 1,
			TotalFrames:  segment.EndFrame - segment.StartFrame,
			Requirements: strictNativeRequirements(),
		}); err != nil {
			t.Fatalf("single session %d: %v", i, err)
		}
	}
	singleWall := time.Since(singleStart)

	perItemSegmented := float64(segmentedWall.Milliseconds()) / float64(items)
	perItemSingle := float64(singleWall.Milliseconds()) / float64(items)
	t.Logf("P4 service cost: segmented %s (%.0f ms/item) vs %d single sessions %s (%.0f ms/item) on %d frames/item",
		segmentedWall, perItemSegmented, items, singleWall, perItemSingle, frames/items)
	if perItemSegmented > perItemSingle {
		t.Errorf("segmented session cost %.0f ms/item, MORE than the %.0f ms/item single-session path it replaces", perItemSegmented, perItemSingle)
	}
}
