package processor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/testenv"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// writeNonCanonicalOverlayMP4 writes a real V3-shaped fixture whose stream is
// deliberately NOT canonical: x264 ultrafast, B-frames, 1/12288 timebase. The
// canonical pass must detect it and rewrite it; no probe shortcut may accept it.
func writeNonCanonicalOverlayMP4(t *testing.T, path string) {
	t.Helper()
	writeNonCanonicalMP4(t, path, 48)
}

func writeNonCanonicalMP4(t *testing.T, path string, frames int) {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-frames:v", fmt.Sprintf("%d", frames),
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-bf", "2", "-g", "24",
		"-video_track_timescale", "12288",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate non-canonical fixture: %v: %s", err, out)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read probe counter %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// TestFinalizeJobRunsCanonicalPassAfterTheGpuLane pins the placement of the
// canonical x264 pass: RunGPU must leave the rendered bytes untouched and
// write no canonical receipt, while the post-pool FinalizeJob emits the
// canonical stream and only then reads the receipt/probes. Re-encoding inside
// RunGPU held a GPU lane for CPU work and is the defect this change removes.
func TestFinalizeJobRunsCanonicalPassAfterTheGpuLane(t *testing.T) {
	testenv.RequireTools(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	proc, store, _ := newProcessor(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "overlay.mp4")
	writeNonCanonicalOverlayMP4(t, output)
	before, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}

	jobID := "postlane-canonical"
	ws, err := workspace.New(proc.jobsRoot, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	prepared := &PreparedJob{
		Job:       &queue.Job{ID: jobID, JobType: queue.JobTypeOverlayRender},
		Workspace: ws,
		Plan: &overlay.Plan{
			Canvas: overlay.Canvas{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationFrames: 48},
			Output: overlay.Output{ProfileID: media.ProfileVeloxOverlayVideoOnlyV3},
			Layers: []overlay.Layer{{ID: "caption", Type: "text", Text: "post lane"}},
		},
		OutputPath: output,
		Metrics:    map[string]float64{},
		totalStart: time.Now(),
	}

	if err := proc.RunGPU(ctx, prepared); err != nil {
		t.Fatalf("RunGPU: %v", err)
	}
	afterGPU, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}
	if afterGPU != before {
		t.Fatalf("RunGPU rewrote the artifact (%s -> %s): canonicalization must wait for the post pool", before, afterGPU)
	}
	if _, err := os.Stat(output + chronon.MediaReceiptSuffix); !os.IsNotExist(err) {
		t.Fatalf("RunGPU wrote a canonical receipt; canonicalization must run in FinalizeJob, stat err=%v", err)
	}

	artifact, err := proc.FinalizeJob(ctx, prepared)
	if err != nil {
		t.Fatalf("FinalizeJob: %v", err)
	}
	afterFinalize, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}
	if afterFinalize == before {
		t.Fatal("FinalizeJob left the non-canonical bytes untouched")
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe finalized output: %v", err)
	}
	if err := profile.ValidateProbe(probe); err != nil {
		t.Fatalf("finalized output does not satisfy V3: %v", err)
	}
	if probe.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("final avcC = %q, want %q", probe.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
	receipt, err := chronon.ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("read canonical receipt: %v", err)
	}
	if receipt.Media.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("canonical receipt avcC = %q, want %q", receipt.Media.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
	if artifact.ArtifactHash == "" {
		t.Fatal("finalize returned an artifact without a content address")
	}
	if _, err := store.Get(ctx, artifact.ArtifactHash); err != nil {
		t.Fatalf("finalized artifact was not stored: %v", err)
	}
}

// TestFinalizeJobCanonicalizesEverySegmentBeforeVerification pins the batch
// half of the placement: the fused mirror and EACH per-item segment are
// canonicalized before finalizeSegments verifies them, so no segment is
// rejected by the profile gate and no per-segment retry is spent re-rendering
// bytes that only needed the canonical x264 pass.
func TestFinalizeJobCanonicalizesEverySegmentBeforeVerification(t *testing.T) {
	testenv.RequireTools(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	proc, store, _ := newProcessor(t)
	dir := t.TempDir()
	mirror := filepath.Join(dir, "mirror.mp4")
	first := segmentOutputPath(dir, 0)
	second := segmentOutputPath(dir, 1)
	writeNonCanonicalMP4(t, mirror, 48)
	writeNonCanonicalMP4(t, first, 24)
	writeNonCanonicalMP4(t, second, 24)
	prepared := &PreparedJob{
		Job: &queue.Job{ID: "postlane-segments", JobType: queue.JobTypeOverlayRender},
		Plan: &overlay.Plan{
			Canvas: overlay.Canvas{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationFrames: 48},
			Output: overlay.Output{ProfileID: media.ProfileVeloxOverlayVideoOnlyV3},
			Layers: []overlay.Layer{{ID: "caption", Type: "text", Text: "segments"}},
		},
		OutputPath: mirror,
		Segments: []chronon.OutputSegment{
			{OutputPath: first, StartFrame: 0, EndFrame: 24},
			{OutputPath: second, StartFrame: 24, EndFrame: 48},
		},
		SegmentItemIDs: []string{"first", "second"},
		Metrics:        map[string]float64{},
		totalStart:     time.Now(),
	}

	artifact, err := proc.FinalizeJob(ctx, prepared)
	if err != nil {
		t.Fatalf("FinalizeJob: %v", err)
	}
	if artifact.OutputFacts == nil || len(artifact.OutputFacts.BatchSegments) != 2 {
		t.Fatalf("batch lost its per-segment facts: %+v", artifact.OutputFacts)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{mirror, first, second} {
		probe, err := media.ProbeFile(ctx, path)
		if err != nil {
			t.Fatalf("probe %s: %v", path, err)
		}
		if err := profile.ValidateProbe(probe); err != nil {
			t.Fatalf("segment output %s is not canonical: %v", path, err)
		}
		if probe.CodecConfigSHA256 != profile.CodecConfigSHA256 {
			t.Fatalf("segment output %s avcC = %q, want %q", path, probe.CodecConfigSHA256, profile.CodecConfigSHA256)
		}
	}
	for _, segment := range artifact.OutputFacts.BatchSegments {
		if _, err := store.Get(ctx, segment.SHA256); err != nil {
			t.Fatalf("canonical segment %s was not stored: %v", segment.ItemID, err)
		}
	}
}

// TestCanonicalReceiptFingerprintRemovesTheFinalReprobe pins the third-probe
// removal. A canonical receipt now carries the avcC fingerprint the pass
// actually verified, so finalize can validate a pinned profile without
// spawning ffprobe again. The fallback (an older receipt without the
// fingerprint) must still pay exactly one probe, which is the cost this test
// measures against zero.
func TestCanonicalReceiptFingerprintRemovesTheFinalReprobe(t *testing.T) {
	testenv.RequireTools(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	output := filepath.Join(t.TempDir(), "overlay.mp4")
	writeNonCanonicalOverlayMP4(t, output)

	realFFprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	countPath := filepath.Join(t.TempDir(), "ffprobe-calls.txt")
	wrapper := filepath.Join(t.TempDir(), "ffprobe-counting.sh")
	script := fmt.Sprintf("#!/bin/sh\nprintf 'call\\n' >> %q\nexec %q \"$@\"\n", countPath, realFFprobe)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	media.Configure(media.Binaries{FFprobe: wrapper})
	t.Cleanup(func() { media.Configure(media.Binaries{}) })

	if _, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3); err != nil {
		t.Fatalf("canonicalizeOutputMP4: %v", err)
	}
	canonicalProbeProcesses := countLines(t, countPath)
	if canonicalProbeProcesses == 0 {
		t.Fatal("the canonical pass did not invoke ffprobe; the counter is not wired")
	}

	receipt, err := chronon.ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("read canonical receipt: %v", err)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	probed := probeResultFromReceipt(receipt)
	if probed.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("receipt probe avcC = %q, want %q", probed.CodecConfigSHA256, profile.CodecConfigSHA256)
	}

	beforeFinalize := countLines(t, countPath)
	if err := ensureCodecConfigProbe(ctx, output, &probed, profile); err != nil {
		t.Fatalf("ensureCodecConfigProbe with receipt fingerprint: %v", err)
	}
	withFingerprint := countLines(t, countPath) - beforeFinalize
	if withFingerprint != 0 {
		t.Fatalf("finalize spawned %d ffprobe process(es) despite the receipt fingerprint, want 0", withFingerprint)
	}

	older := receipt
	older.Media.CodecConfigSHA256 = ""
	olderProbe := probeResultFromReceipt(older)
	beforeFallback := countLines(t, countPath)
	if err := ensureCodecConfigProbe(ctx, output, &olderProbe, profile); err != nil {
		t.Fatalf("ensureCodecConfigProbe fallback: %v", err)
	}
	fallbackProbes := countLines(t, countPath) - beforeFallback
	if fallbackProbes == 0 {
		t.Fatal("a receipt without the fingerprint must still probe the bytes")
	}
	t.Logf("ffprobe processes: canonical pass=%d, finalize with fingerprint=%d, finalize fallback=%d",
		canonicalProbeProcesses, withFingerprint, fallbackProbes)
}

// TestCanonicalizeOutputPathsRunsSegmentsInParallel measures the post-pool
// batch path: four independent artifacts canonicalized sequentially versus on
// the bounded pool. It asserts correctness only; the measured times are logged
// because a wall-clock assertion on a shared CI host would be noise.
func TestCanonicalizeOutputPathsRunsSegmentsInParallel(t *testing.T) {
	testenv.RequireTools(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	proc, _, _ := newProcessor(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	writeNonCanonicalOverlayMP4(t, source)

	const count = 4
	sequentialPaths := make([]string, count)
	parallelPaths := make([]string, count)
	for i := 0; i < count; i++ {
		sequentialPaths[i] = filepath.Join(dir, fmt.Sprintf("sequential-%03d.mp4", i))
		parallelPaths[i] = filepath.Join(dir, fmt.Sprintf("parallel-%03d.mp4", i))
		copyFile(t, source, sequentialPaths[i])
		copyFile(t, source, parallelPaths[i])
	}

	start := time.Now()
	for _, path := range sequentialPaths {
		if _, err := canonicalizeOutputMP4(ctx, path, media.ProfileVeloxOverlayVideoOnlyV3); err != nil {
			t.Fatalf("sequential canonicalize %s: %v", path, err)
		}
	}
	sequential := time.Since(start)

	metrics := map[string]float64{}
	start = time.Now()
	changed, err := proc.canonicalizeOutputPaths(ctx, "parallel-canonical", parallelPaths, metrics, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("parallel canonicalize: %v", err)
	}
	parallel := time.Since(start)
	if !changed {
		t.Fatal("parallel pass reported no changes for non-canonical inputs")
	}
	if metrics[metricnames.CanonicalizeOverlayMS] <= 0 {
		t.Fatalf("parallel pass did not record %s: %v", metricnames.CanonicalizeOverlayMS, metrics)
	}

	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range parallelPaths {
		probe, err := media.ProbeFile(ctx, path)
		if err != nil {
			t.Fatalf("probe %s: %v", path, err)
		}
		if err := profile.ValidateProbe(probe); err != nil {
			t.Fatalf("parallel output %s is not canonical: %v", path, err)
		}
		if probe.CodecConfigSHA256 != profile.CodecConfigSHA256 {
			t.Fatalf("parallel output %s avcC = %q, want %q", path, probe.CodecConfigSHA256, profile.CodecConfigSHA256)
		}
	}
	t.Logf("canonicalized %d clips: sequential=%s parallel=%s speedup=%.2fx",
		count, sequential.Round(time.Millisecond), parallel.Round(time.Millisecond),
		float64(sequential)/float64(parallel))
}
