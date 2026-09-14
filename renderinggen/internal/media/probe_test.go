package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// goldenFixture resolves a repository fixture next to the module root and skips
// when it is not checked out.
func goldenFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "golden", name)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture %s unavailable: %v", name, err)
	}
	return path
}

// TestProbeFileCertifiesFirstFrameKeyframeFromOneInvocation pins the merged
// probe: ONE ffprobe run certifies the structural facts AND the first-frame
// keyframe check of the video stream. The keyframe verdict must therefore be
// present (the fixture's first video frame is an IDR) without a second process
// reading the same file, which is what this test would fail on if the frames
// section were dropped from the merged command.
func TestProbeFileCertifiesFirstFrameKeyframeFromOneInvocation(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not installed: %v", err)
	}
	path := goldenFixture(t, "background.mp4")

	result, err := ProbeFile(context.Background(), path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !result.HasVideo || result.VideoStreams != 1 {
		t.Fatalf("video presence = %t / streams = %d, want one video stream", result.HasVideo, result.VideoStreams)
	}
	if result.Width != 1280 || result.Height != 720 {
		t.Fatalf("canvas = %dx%d, want 1280x720", result.Width, result.Height)
	}
	if result.VideoCodec == "" || result.FrameCount <= 0 || result.VideoTimeBaseDen <= 0 {
		t.Fatalf("structural facts incomplete: codec=%q frames=%d timebase=%d/%d", result.VideoCodec, result.FrameCount, result.VideoTimeBaseNum, result.VideoTimeBaseDen)
	}
	if !result.FirstFrameKeyframe {
		t.Fatal("the fixture's first video frame is an IDR; FirstFrameKeyframe=false means the merged probe no longer reports the frames section")
	}
}

// TestProbeFileKeyframeSurvivesASecondStream pins the stream attribution: an
// audio-bearing artifact must still certify the VIDEO stream's first frame, so
// the frames window cannot be attributed to whichever stream ffprobe happens to
// report first.
func TestProbeFileKeyframeSurvivesASecondStream(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not installed: %v", err)
	}
	path := goldenFixture(t, "Pale-Olive.mp4")

	result, err := ProbeFile(context.Background(), path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result.AudioStreams != 1 || !result.HasAudio {
		t.Fatalf("audio facts = %d streams / has=%t, want one audio stream", result.AudioStreams, result.HasAudio)
	}
	if !result.FirstFrameKeyframe {
		t.Fatal("a second (audio) stream must not change the video stream's first-frame verdict")
	}
}

func validProbe() ProbeResult {
	return ProbeResult{
		Container: "webm", DurationUS: 1_000_000, Width: 1920, Height: 1080,
		FPSNum: 30, FPSDen: 1, PixelFormat: "yuva420p", VideoCodec: "vp9",
	}
}

func TestValidateOverlayAcceptsVideoOnlyArtifact(t *testing.T) {
	if err := validProbe().ValidateOverlay(1920, 1080, 30, 1); err != nil {
		t.Fatalf("validate overlay: %v", err)
	}
}

func TestValidateOverlayRejectsAudioStream(t *testing.T) {
	p := validProbe()
	p.AudioStreams = 1
	if err := p.ValidateOverlay(1920, 1080, 30, 1); err == nil {
		t.Fatal("expected audio-bearing overlay to be rejected")
	}
}

func TestValidateOverlayRejectsWrongCanvasAndIncompleteMedia(t *testing.T) {
	p := validProbe()
	if err := p.ValidateOverlay(1280, 720, 30, 1); err == nil {
		t.Fatal("expected wrong canvas to be rejected")
	}
	p = validProbe()
	p.VideoCodec = ""
	if err := p.ValidateOverlay(1920, 1080, 30, 1); err == nil {
		t.Fatal("expected incomplete media metadata to be rejected")
	}
}

// TestValidateOverlayUncertifiableFPSIsContractError pins the fps gate: when
// the probed r_frame_rate could not be certified (0/0 or unparseable), a
// declared fps contract must FAIL — the historical behavior skipped the fps
// check exactly when the probe could not certify it, silently exempting the
// artifact from the overlay media contract.
func TestValidateOverlayUncertifiableFPSIsContractError(t *testing.T) {
	p := validProbe()
	p.FPSUncertifiable = true
	p.FPSNum, p.FPSDen = 0, 0
	if err := p.ValidateOverlay(1920, 1080, 30, 1); err == nil {
		t.Fatal("uncertifiable fps must fail a declared fps contract")
	}
	// Without a declared fps contract the same probe passes the fps gate.
	if err := p.ValidateOverlay(1920, 1080, 0, 0); err != nil {
		t.Fatalf("no fps contract should not be exempted by an fps error: %v", err)
	}
}
