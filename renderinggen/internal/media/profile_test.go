package media

import "testing"

func TestResolveProfile(t *testing.T) {
	p, err := ResolveProfile(ProfileVeloxH264720p30V1)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1280 || p.Height != 720 || p.FPSNum != 30 || p.FPSDen != 1 || p.Codec != "h264" {
		t.Fatalf("profile=%+v", p)
	}
}

func TestProfileValidateProbe(t *testing.T) {
	p, _ := ResolveProfile(ProfileVeloxH264720p30V1)
	probe := ProbeResult{Container: "mov,mp4,m4a,3gp,3g2,mj2", DurationUS: 2_000_000, Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, PixelFormat: "yuv420p", VideoCodec: "h264", CodecProfile: "High", FrameCount: 60, FirstFrameKeyframe: true}
	if err := p.ValidateProbe(probe); err != nil {
		t.Fatal(err)
	}
	probe.FirstFrameKeyframe = false
	if err := p.ValidateProbe(probe); err == nil {
		t.Fatal("expected non-keyframe rejection")
	}
}

func TestUnknownProfileFailsClosed(t *testing.T) {
	if _, err := ResolveProfile("velox-h264-custom"); err == nil {
		t.Fatal("expected unknown profile error")
	}
}
