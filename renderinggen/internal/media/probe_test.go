package media

import "testing"

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
