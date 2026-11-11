package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOverlayVideoOnlyV3RejectsARealNonCanonicalMP4 is the byte-level half of
// the V3 contract. It generates a real H.264 MP4 at the muxer timebase the
// production overlay batches actually carry (1/12288) and proves the profile
// rejects it. A profile that only declares 1/90000 in Go would pass here; this
// one cannot.
func TestOverlayVideoOnlyV3RejectsARealNonCanonicalMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "measured-overlay.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=1920x1080:r=24/1:d=1",
		"-frames:v", "24", "-an", "-sn", "-dn",
		"-c:v", "libx264", "-preset", "veryfast", "-bf", "0", "-g", "48",
		"-pix_fmt", "yuv420p", "-profile:v", "high", "-level:v", "4.0",
		"-video_track_timescale", "12288", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot generate the fixture: %v: %s", err, output)
	}
	probe, err := ProbeFile(context.Background(), path)
	if err != nil {
		t.Fatalf("probe real MP4: %v", err)
	}
	if probe.VideoTimeBaseNum != 1 || probe.VideoTimeBaseDen != 12288 {
		t.Fatalf("fixture timebase = %d/%d, want 1/12288", probe.VideoTimeBaseNum, probe.VideoTimeBaseDen)
	}
	profile, err := ResolveProfile(ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateProbe(probe); err == nil {
		t.Fatal("V3 must reject the real 1/12288 muxer stream")
	}
}
