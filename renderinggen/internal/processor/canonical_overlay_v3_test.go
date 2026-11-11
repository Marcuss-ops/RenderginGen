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

// TestCanonicalizeOverlayV3EmitsThePinnedStream is the byte-level emission
// gate for V3. It starts from a real, deliberately NON-canonical MP4 (x264
// ultrafast, 1/12288, B-frames) and proves the canonicalization pass produces
// the exact stream facts the assembly contract pins: 1/90000, canonical avcC,
// 48 frames, High 4.0, yuv420p, tv/bt709 and PTS 0. It also proves the receipt
// written beside the final bytes carries the FINAL hash, not the pre-pass one.
func TestCanonicalizeOverlayV3EmitsThePinnedStream(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx := context.Background()
	output := filepath.Join(t.TempDir(), "overlay.mp4")
	generate := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-frames:v", "48",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-bf", "2", "-g", "24",
		"-video_track_timescale", "12288",
		output,
	)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate non-canonical fixture: %v: %s", err, out)
	}
	before, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	if before.VideoTimeBaseNum != 1 || before.VideoTimeBaseDen != 12288 {
		t.Fatalf("fixture timebase = %d/%d, want 1/12288", before.VideoTimeBaseNum, before.VideoTimeBaseDen)
	}

	changed, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("canonicalizeOverlayMP4: %v", err)
	}
	if !changed {
		t.Fatal("non-canonical fixture was not re-encoded")
	}

	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	after, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe canonical output: %v", err)
	}
	if err := profile.ValidateProbe(after); err != nil {
		t.Fatalf("canonical output does not satisfy V3: %v", err)
	}
	if after.VideoTimeBaseNum != 1 || after.VideoTimeBaseDen != 90000 {
		t.Fatalf("canonical timebase = %d/%d, want 1/90000", after.VideoTimeBaseNum, after.VideoTimeBaseDen)
	}
	if after.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("canonical avcC = %s, want %s", after.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
	if after.FrameCount != 48 {
		t.Fatalf("canonical frame count = %d, want 48", after.FrameCount)
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
	if receipt.Media.VideoTimeBaseNum != 1 || receipt.Media.VideoTimeBaseDen != 90000 {
		t.Fatalf("receipt timebase = %d/%d, want 1/90000", receipt.Media.VideoTimeBaseNum, receipt.Media.VideoTimeBaseDen)
	}

	// Idempotence matters for the single-segment retry: canonicalizing twice
	// must not rewrite the bytes (or invalidate the receipt).
	changed, err = canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3)
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
