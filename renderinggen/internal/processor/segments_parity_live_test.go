package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
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

// TestAudioAwareChunkParentMatchesSingleJob exercises the real parent
// finalizer and daemon mux, not a mock assembler. Video-only children cover
// exact GOP-aligned windows; the anchor must copy the original audio once.
func TestAudioAwareChunkParentMatchesSingleJob(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	binary := liveChrononBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const frames, per, width, height = 360, 180, 320, 180
	dir := t.TempDir()
	source := filepath.Join(dir, "source.m4a")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
		"sine=frequency=997:sample_rate=48000:duration=12", "-c:a", "aac", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create audio fixture: %v (%s)", err, out)
	}
	plan := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(plan, []byte(liveSegmentedPlan(frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase: filepath.Join(t.TempDir(), "audio.sock"), Count: 1, LanesPerDaemon: 1,
		Binary: binary, AssetsRoot: dir, Backend: "vulkan", Stdout: os.Stderr, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Shutdown(context.Background())
	requirements := chronon.ExecutionRequirements{
		Backend: "vulkan", GPURequired: true, CompositionRequired: true, CPUFallbackAllowed: false,
	}
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	sourceHash, _, err := hashio.File(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutFile(ctx, sourceHash, source); err != nil {
		t.Fatal(err)
	}
	q := &finalizerQueue{claimResult: &queue.Job{
		ID: "parent", Lease: time.Hour, RenderPlan: anchorPlanJSON(sourceHash, true),
	}}
	for i := 0; i < frames/per; i++ {
		path := filepath.Join(dir, fmt.Sprintf("child-%d.mp4", i))
		if err := pool.Render(ctx, chronon.RenderRequest{
			PlanPath: plan, AssetsRoot: dir, OutputPath: path,
			RangeEnabled: true, FirstFrame: int64(i * per), LastFrame: int64((i+1)*per - 1),
			TotalFrames: per, Requirements: requirements,
		}); err != nil {
			t.Fatalf("render child %d: %v", i, err)
		}
		facts, err := media.ProbeFile(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if facts.AudioStreams != 0 || facts.FrameCount != per || !facts.ClosedGOP || !facts.FirstFrameKeyframe {
			t.Fatalf("child %d is not silent, exact-window and copy-safe: %+v", i, facts)
		}
		hash, _, err := hashio.File(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutFile(ctx, hash, path); err != nil {
			t.Fatal(err)
		}
		q.children = append(q.children, &queue.Job{
			ID: fmt.Sprintf("child-%d", i), ChunkIndex: i, State: queue.StateCompleted,
			FrameRange: &queue.FrameRange{Start: int64(i * per), End: int64((i + 1) * per)},
			Artifact:   copySafeChunkArtifact(hash),
		})
	}
	finalizer := NewParentFinalizer(q, store, chronon.NewIPCClient(pool.Sockets()[0]), nil, "test", t.TempDir())
	finalized, artifact, err := finalizer.FinalizeFromChildren(ctx, "parent")
	if err != nil || !finalized {
		t.Fatalf("finalize real family: finalized=%v err=%v", finalized, err)
	}
	parent, _, err := store.LocalPath(ctx, artifact.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(dir, "single-job.mp4")
	if err := pool.Render(ctx, chronon.RenderRequest{
		PlanPath: plan, AssetsRoot: dir, OutputPath: reference,
		AudioSourcePath: source, AudioTargetSampleRate: 48000, TotalFrames: frames, Requirements: requirements,
	}); err != nil {
		t.Fatalf("single-job reference: %v", err)
	}
	got, err := media.ProbeFile(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	want, err := media.ProbeFile(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	if got.AudioStreams != 1 || want.AudioStreams != 1 || got.FrameCount != frames || want.FrameCount != frames {
		t.Fatalf("parent/single-job stream counts differ: parent=%+v reference=%+v", got, want)
	}
	// Compare every sealed output-contract dimension, including GOP cadence.
	// Each six-second child spans three of the encoder's two-second GOPs.
	if !reflect.DeepEqual(outputFactsFromProbe(&got), outputFactsFromProbe(&want)) || got.DurationUS != want.DurationUS {
		t.Fatalf("parent/single-job output facts differ: parent=%+v reference=%+v", got, want)
	}
	parentFrames, referenceFrames := rawFrames(t, parent, width, height), rawFrames(t, reference, width, height)
	if len(parentFrames) != frames || len(referenceFrames) != frames {
		t.Fatalf("decoded frame counts = %d/%d, want %d", len(parentFrames), len(referenceFrames), frames)
	}
	for i := range referenceFrames {
		if !bytes.Equal(parentFrames[i], referenceFrames[i]) {
			t.Fatalf("parent frame %d differs from single-job reference", i)
		}
	}
	// Single-job mux deliberately excludes negative-PTS AAC priming packets.
	// Compare packet payloads after that same origin selection, not a decode
	// (which could conceal duplicated or transcoded compressed packets).
	var audio []byte
	for _, path := range []string{source, parent, reference} {
		cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0",
			"-show_packets", "-show_entries", "packet=pts,data_hash", "-show_data_hash", "sha256", "-of", "json", path)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hash copied audio from %s: %v", path, err)
		}
		var document struct {
			Packets []struct {
				PTS  *int64 `json:"pts"`
				Hash string `json:"data_hash"`
			} `json:"packets"`
		}
		if err := json.Unmarshal(out, &document); err != nil {
			t.Fatal(err)
		}
		out = nil
		for _, packet := range document.Packets {
			if packet.PTS == nil || *packet.PTS < 0 {
				continue
			}
			if packet.Hash == "" {
				t.Fatal("audio packet payload hash missing")
			}
			out = append(out, packet.Hash...)
			out = append(out, '\n')
		}
		if len(out) == 0 {
			t.Fatal("copied audio must not be empty")
		}
		if audio == nil {
			audio = out
		} else if !bytes.Equal(audio, out) {
			t.Fatalf("audio in %s differs from source packets (duplicated, trimmed or transcoded)", path)
		}
	}
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
