package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/testenv"
	"os"
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
	testenv.RequireTools(t, "ffprobe")
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
	testenv.RequireTools(t, "ffprobe")
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

// TestHashFFprobeExtradataHashesDecodedBytes pins the identity shared with the
// Go producer and the Rust assembler: ffprobe's offset-prefixed, ASCII-annotated
// hex dump must be decoded before hashing. Hashing the text would make every
// correct input fail the avcC gate.
// TestProbeFileWithKnownFrameCountSkipsTheCountScan pins the optimization that
// removes a full -count_frames decode from finalized overlays: the render plan
// already certified the frame count, so a fragmented MP4 that omits nb_frames
// must use that number instead of scanning the whole file. The fake ffprobe
// records any invocation carrying -count_frames: a known count must leave the
// marker absent, while the same file probed without one must create it — which
// proves the fallback is real and the test exercises the branch.
func TestProbeFileWithKnownFrameCountSkipsTheCountScan(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "count-scan-ran")
	script := filepath.Join(dir, "ffprobe")
	body := `#!/bin/sh
case "$*" in
  *-count_frames*)
    : > "` + marker + `"
    echo 120
    exit 0
    ;;
  *-show_packets*)
    printf '%s' '{"packets":[]}'
    exit 0
    ;;
 esac
printf '%s' '{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","profile":"High","width":1920,"height":1080,"pix_fmt":"yuv420p","r_frame_rate":"24/1","time_base":"1/24","sample_aspect_ratio":"1:1","nb_frames":"0"}],"format":{"format_name":"mp4","duration":"5.000000"},"frames":[{"key_frame":1,"stream_index":0}]}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	Configure(Binaries{FFprobe: script})
	t.Cleanup(func() { Configure(Binaries{}) })

	got, err := ProbeFileWithKnownFrameCount(context.Background(), "ignored.mp4", 120)
	if err != nil {
		t.Fatalf("probe with known count: %v", err)
	}
	if got.FrameCount != 120 {
		t.Fatalf("frame count = %d, want the certified 120", got.FrameCount)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a certified frame count must not spawn the -count_frames scan")
	}

	// Control: without the known count the same document forces the fallback.
	if _, err := ProbeFile(context.Background(), "ignored.mp4"); err != nil {
		t.Fatalf("probe without known count: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("without a known count the -count_frames fallback must run")
	}
}

func TestHashFFprobeExtradataHashesDecodedBytes(t *testing.T) {
	dump := "\n00000000: 0164 0028  .d.(\n"
	sum := sha256.Sum256([]byte{0x01, 0x64, 0x00, 0x28})
	want := hex.EncodeToString(sum[:])
	if got := hashFFprobeExtradata(dump); got != want {
		t.Fatalf("extradata hash = %q, want %q", got, want)
	}
	if got := hashFFprobeExtradata(""); got != "" {
		t.Fatalf("empty dump hash = %q, want empty", got)
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
