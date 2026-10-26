package processor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// segments_parity_live_test.go is the LIVE rung of the P4 per-segment parity
// gate (docs/operations/TICKET-BATCH-SUPERJOB.md, "Parità pixel/audio per
// segmento contro render indipendenti"): segment k cut from ONE segmented
// session must decode to exactly the frames the non-fused single-item path
// renders for item k's window.
//
// The offline writer tests prove the ROUTING (a segment carries its window's
// frames, nothing mixed or skipped, mirrored against the full timeline in the
// same session). This test compares against INDEPENDENT encoder sessions — the
// reference runs the ticket names — so any cross-session difference in the
// encoded output is measured here instead of assumed away.

// rawFrames decodes every video frame of an MP4 to raw yuv420p planes and
// returns them as consecutive frame blobs (width*height*3/2 bytes each).
func rawFrames(t *testing.T, path string, width, height int) [][]byte {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-f", "rawvideo", "-pix_fmt", "yuv420p", "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, stderr.String())
	}
	frameSize := width * height * 3 / 2
	raw := out.Bytes()
	if len(raw) == 0 || len(raw)%frameSize != 0 {
		t.Fatalf("decode %s produced %d bytes, not a whole number of %dx%d yuv420p frames", path, len(raw), width, height)
	}
	frames := make([][]byte, 0, len(raw)/frameSize)
	for off := 0; off < len(raw); off += frameSize {
		frames = append(frames, raw[off:off+frameSize])
	}
	return frames
}

// TestSegmentedSessionMatchesIndependentSingleItemRenders compares the
// segmented session's per-item MP4s against independent single-item renders of
// the SAME windows, decoded frame for frame: this is the "render indipendenti"
// reference comparison of the P4 parity contract, on the live native encoder
// lane (NVENC). The ticket's full gate runs this comparison over the golden
// mix; this is the engine-level rung of the same comparison.
//
// Gated like every live chronon test (CHRONON_BINARY / CHRONON_HOME).
func TestSegmentedSessionMatchesIndependentSingleItemRenders(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	binary := liveChrononBinary(t)

	const frames, items = 12, 3
	const width, height = 320, 180
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

	// A: one segmented session — N per-item MP4s cut from ONE encoder session.
	if err := pool.Render(ctx, chronon.RenderRequest{
		PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: mirror,
		OutputSegments: segments, TotalFrames: frames,
		Requirements: requirements,
	}); err != nil {
		t.Fatalf("segmented session: %v", err)
	}

	// B: the non-fused reference — one independent single-item session per
	// window, exactly the historical path these segments replace.
	references := make([]string, items)
	for i, segment := range segments {
		references[i] = filepath.Join(dir, "output", fmt.Sprintf("reference-%03d.mp4", i))
		if err := pool.Render(ctx, chronon.RenderRequest{
			PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: references[i],
			RangeEnabled: true, FirstFrame: segment.StartFrame, LastFrame: segment.EndFrame - 1,
			TotalFrames:  segment.EndFrame - segment.StartFrame,
			Requirements: requirements,
		}); err != nil {
			t.Fatalf("independent reference %d: %v", i, err)
		}
	}

	// Frame-for-frame parity between the two paths.
	for i, segment := range segments {
		got := rawFrames(t, segment.OutputPath, width, height)
		want := rawFrames(t, references[i], width, height)
		if len(got) != int(per) || len(want) != int(per) {
			t.Fatalf("segment %d decoded %d frames, reference %d: both must be the window's %d",
				i, len(got), i, int(per))
		}
		for f := 0; f < len(want); f++ {
			if !bytes.Equal(got[f], want[f]) {
				t.Errorf("segment %d frame %d differs from its independent render (window [%d,%d)): the segmented session and the single-item path must produce the same pixels",
					i, f, segment.StartFrame, segment.EndFrame)
			}
		}
	}
}
