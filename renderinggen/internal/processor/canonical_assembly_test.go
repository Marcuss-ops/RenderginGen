package processor

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// TestCanonicalizeAssemblyV1NormalizesSourceFrameRate reproduces the live
// clip.render failure of 2026-11-11: a 25 fps source was emitted with
// -fps_mode passthrough, so the V1 profile gate rejected the artifact with
// "fps=25/1, want 24/1" AFTER the GPU render. The canonical pass must emit the
// profile cadence whenever the source disagrees with it.
func TestCanonicalizeAssemblyV1NormalizesSourceFrameRate(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx := context.Background()
	output := filepath.Join(t.TempDir(), "clip-25fps.mp4")
	generate := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "5",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2",
		"-shortest",
		output,
	)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate 25 fps fixture: %v: %s", err, out)
	}
	before, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	if before.FPSNum != 25 || before.FPSDen != 1 {
		t.Fatalf("fixture fps = %d/%d, want 25/1", before.FPSNum, before.FPSDen)
	}

	changed, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatalf("canonicalizeOutputMP4: %v", err)
	}
	if !changed {
		t.Fatal("25 fps fixture was not re-encoded")
	}
	after, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe canonical output: %v", err)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateProbe(after); err != nil {
		t.Fatalf("canonical output does not satisfy V1: %v", err)
	}
	if after.FPSNum != profile.FPSNum || after.FPSDen != profile.FPSDen {
		t.Fatalf("canonical fps = %d/%d, want %d/%d", after.FPSNum, after.FPSDen, profile.FPSNum, profile.FPSDen)
	}
}

// TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio is the byte-level
// emission gate for VELOX_ASSEMBLY_READY_V1. It starts from a real,
// deliberately NON-canonical MP4 with audio (x264 ultrafast, 1/12288,
// B-frames) and proves the canonicalization pass emits the pinned avcC
// (7201ce80…) and 1/90000 while preserving the audio track. A clip lane that
// emits only the video recipe would pass the avcC check but lose the audio;
// a clip lane without this pass fails the profile gate outright (the actual
// NVENC defect: avcC 111186fe…).
func TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx := context.Background()
	output := filepath.Join(t.TempDir(), "clip.mp4")
	generate := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-frames:v", "120", "-t", "5",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-bf", "2", "-g", "24",
		"-video_track_timescale", "12288",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2",
		"-shortest",
		output,
	)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate non-canonical fixture: %v: %s", err, out)
	}
	before, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	if before.AudioStreams != 1 {
		t.Fatalf("fixture audio streams = %d, want 1", before.AudioStreams)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateProbe(before); err == nil {
		t.Fatal("non-canonical fixture unexpectedly satisfies VELOX_ASSEMBLY_READY_V1")
	}

	changed, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatalf("canonicalizeOutputMP4: %v", err)
	}
	if !changed {
		t.Fatal("non-canonical fixture was not re-encoded")
	}

	after, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe canonical output: %v", err)
	}
	if err := profile.ValidateProbe(after); err != nil {
		t.Fatalf("canonical output does not satisfy V1: %v", err)
	}
	if after.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("canonical avcC = %s, want %s", after.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
	if after.VideoTimeBaseNum != 1 || after.VideoTimeBaseDen != 90000 {
		t.Fatalf("canonical timebase = %d/%d, want 1/90000", after.VideoTimeBaseNum, after.VideoTimeBaseDen)
	}
	if after.AudioStreams != 1 || after.AudioCodec != "aac" || after.SampleRate != 48000 {
		t.Fatalf("canonical audio = streams:%d codec:%q rate:%d, want 1/aac/48000",
			after.AudioStreams, after.AudioCodec, after.SampleRate)
	}
	if after.FrameCount != 120 {
		t.Fatalf("canonical frame count = %d, want 120", after.FrameCount)
	}

	digest, size, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := chronon.ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("read canonical receipt: %v", err)
	}
	if receipt.Output.SHA256 != digest || receipt.Output.Bytes != size {
		t.Fatalf("receipt describes %s/%d, bytes are %s/%d", receipt.Output.SHA256, receipt.Output.Bytes, digest, size)
	}
	if !receipt.VerificationPassed() {
		t.Fatalf("canonical receipt status = %q, want pass", receipt.Verification.Status)
	}
	if receipt.Media.AudioStreams != 1 {
		t.Fatalf("receipt audio streams = %d, want 1", receipt.Media.AudioStreams)
	}

	changed, err = canonicalizeOutputMP4(ctx, output, media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatalf("second canonicalization: %v", err)
	}
	if changed {
		t.Fatal("already-canonical output was rewritten")
	}
	again, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}
	if again != digest {
		t.Fatalf("idempotent pass changed digest: %s -> %s", digest, again)
	}
}
