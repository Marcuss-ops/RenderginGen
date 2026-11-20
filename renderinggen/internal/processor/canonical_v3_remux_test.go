package processor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// TestCanonicalizeV3RemuxesNativeTimebaseWithoutReencode pins the fast path for
// the native V3 lane: the renderer already emits the accepted avcC, closed GOP
// 48, an IDR first frame and PTS 0, but the MP4 muxer writes a 1/12288 track
// timebase instead of the certified 1/90000. Rewriting the track by packet copy
// must satisfy V3 without changing any decoded frame; the old pass answered
// this one container fact with a full x264 encode (~13.6 s per 720-frame
// child).
func TestCanonicalizeV3RemuxesNativeTimebaseWithoutReencode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	generate := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-frames:v", "48",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-bf", "2", "-g", "24",
		source,
	)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, out)
	}
	// First pass produces the canonical x264 avcC and V3 facts.
	if _, err := canonicalizeOutputMP4(ctx, source, media.ProfileVeloxOverlayVideoOnlyV3); err != nil {
		t.Fatalf("canonicalize fixture: %v", err)
	}

	// The native lane's only divergence is the muxer track timescale. Copying
	// the packets into a 1/12288 track reproduces that fact while preserving
	// the avcC, the closed GOP and PTS 0.
	lowTimebase := filepath.Join(dir, "native-timebase.mp4")
	remux := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", source, "-map", "0:v:0", "-an", "-c", "copy",
		"-video_track_timescale", "12288", lowTimebase,
	)
	if out, err := remux.CombinedOutput(); err != nil {
		t.Fatalf("rewrite fixture timebase: %v: %s", err, out)
	}
	before, err := media.ProbeFile(ctx, lowTimebase)
	if err != nil {
		t.Fatalf("probe native-timebase fixture: %v", err)
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateProbe(before); err == nil {
		t.Fatal("fixture must fail V3 before the remux (1/12288 track timebase)")
	}
	if before.VideoTimeBaseNum != 1 || before.VideoTimeBaseDen != 12288 {
		t.Fatalf("fixture timebase = %d/%d, want 1/12288", before.VideoTimeBaseNum, before.VideoTimeBaseDen)
	}
	if before.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		t.Fatalf("fixture avcC = %q, want the canonical %q", before.CodecConfigSHA256, profile.CodecConfigSHA256)
	}
	framesBefore := videoFrameMD5(t, lowTimebase)

	changed, err := canonicalizeOutputMP4(ctx, lowTimebase, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("canonicalize native-timebase artifact: %v", err)
	}
	if !changed {
		t.Fatal("native artifact with a 1/12288 track timebase was not canonicalized")
	}
	after, err := media.ProbeFile(ctx, lowTimebase)
	if err != nil {
		t.Fatalf("probe remuxed artifact: %v", err)
	}
	if err := profile.ValidateProbe(after); err != nil {
		t.Fatalf("remuxed artifact does not satisfy V3: %v", err)
	}
	if after.StartPTS != 0 {
		t.Fatalf("start PTS = %d, want 0", after.StartPTS)
	}
	if after.VideoTimeBaseNum != 1 || after.VideoTimeBaseDen != 90000 {
		t.Fatalf("timebase = %d/%d, want 1/90000", after.VideoTimeBaseNum, after.VideoTimeBaseDen)
	}
	if after.CodecConfigSHA256 != before.CodecConfigSHA256 {
		t.Fatalf("remux changed the avcC: %q -> %q", before.CodecConfigSHA256, after.CodecConfigSHA256)
	}
	if framesAfter := videoFrameMD5(t, lowTimebase); framesAfter != framesBefore {
		t.Fatal("remux changed the decoded video; the x264 path ran instead of packet copy")
	}

	digest, size, err := hashio.File(lowTimebase)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := chronon.ReadMediaReceipt(lowTimebase)
	if err != nil {
		t.Fatalf("read canonical receipt: %v", err)
	}
	if receipt.Output.SHA256 != digest || receipt.Output.Bytes != size {
		t.Fatalf("receipt describes %s/%d, bytes are %s/%d", receipt.Output.SHA256, receipt.Output.Bytes, digest, size)
	}

	changed, err = canonicalizeOutputMP4(ctx, lowTimebase, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("second canonicalization: %v", err)
	}
	if changed {
		t.Fatal("already-canonical remuxed artifact was rewritten")
	}
}

func videoFrameMD5(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", path, "-map", "0:v:0", "-f", "framemd5", "-",
	).Output()
	if err != nil {
		t.Fatalf("framemd5 %s: %v", path, err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
