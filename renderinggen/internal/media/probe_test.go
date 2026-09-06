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
