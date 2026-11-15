package processor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// TestCanonicalizeOutputMP4AcceptsRecertifiedNVENCWithoutReencode is the
// end-to-end gate for the opt-in NVENC path. The fixture has the V3 stream
// shape (High 4.0, 24 fps, 1/90000, no audio) but a different SPS/PPS because
// it uses two reference frames. It must still be rejected by default and
// re-encoded to the canonical x264 avcC; once its exact hash is allowlisted,
// the same bytes must be accepted untouched, with no receipt and no encode.
func TestCanonicalizeOutputMP4AcceptsRecertifiedNVENCWithoutReencode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	ctx := context.Background()
	output := filepath.Join(t.TempDir(), "nvenc-shaped.mp4")
	generate := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-frames:v", "72",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-refs", "2",
		"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p",
		"-g", "48", "-bf", "0", "-sc_threshold", "0",
		"-x264-params", "scenecut=0:open-gop=0:keyint=48:min-keyint=48",
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
		"-video_track_timescale", "90000", "-movflags", "+faststart",
		"-an",
		output,
	)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate native-shaped fixture: %v: %s", err, out)
	}

	before, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateProbe(before); err == nil {
		t.Fatal("fixture unexpectedly satisfies the canonical V3 avcC")
	} else if !strings.Contains(err.Error(), "SPS/PPS") {
		t.Fatalf("fixture fails on a fact other than the avcC: %v", err)
	}
	beforeHash, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3, before.CodecConfigSHA256)
	if err != nil {
		t.Fatalf("allowlisted canonicalization: %v", err)
	}
	if changed {
		t.Fatal("allowlisted native bytes were re-encoded")
	}
	afterAllowlisted, _, err := hashio.File(output)
	if err != nil {
		t.Fatal(err)
	}
	if afterAllowlisted != beforeHash {
		t.Fatalf("allowlisted pass changed bytes: %s -> %s", beforeHash, afterAllowlisted)
	}
	if _, err := os.Stat(output + chronon.MediaReceiptSuffix); !os.IsNotExist(err) {
		t.Fatalf("allowlisted pass wrote a canonical receipt; stat err=%v", err)
	}

	changed, err = canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("default canonicalization: %v", err)
	}
	if !changed {
		t.Fatal("default pass did not re-encode the non-canonical avcC")
	}
	after, err := media.ProbeFile(ctx, output)
	if err != nil {
		t.Fatalf("probe canonical output: %v", err)
	}
	if err := profile.ValidateProbe(after); err != nil {
		t.Fatalf("canonical output does not satisfy V3: %v", err)
	}
	if after.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("canonical avcC = %q, want %q", after.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
}

func TestProcessorOutputProfileCarriesRecertifiedAVCC(t *testing.T) {
	hash := strings.Repeat("a", 64)
	proc, _, _ := newProcessorWith(t, Options{AcceptedCodecConfigSHA256: []string{hash}})
	profile, err := proc.outputProfile(media.ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.AcceptedCodecConfigSHA256) != 1 || profile.AcceptedCodecConfigSHA256[0] != hash {
		t.Fatalf("accepted avcC = %v, want [%s]", profile.AcceptedCodecConfigSHA256, hash)
	}
}
