package media

import "fmt"

// OutputProfile is the stable media contract selected by a render plan.
// Encoder implementation details are intentionally not part of the ID.
type OutputProfile struct {
	ID              string
	Width           int
	Height          int
	FPSNum          int
	FPSDen          int
	Codec           string
	CodecProfile    string
	PixelFormat     string
	Container       string
	RequireNoAudio  bool
	RequireKeyframe bool
}

const (
	ProfileVeloxH2641080p30V1 = "velox-h264-1080p30-v1"
	ProfileVeloxH264720p30V1  = "velox-h264-720p30-v1"
)

var profiles = map[string]OutputProfile{
	ProfileVeloxH2641080p30V1: {ID: ProfileVeloxH2641080p30V1, Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Codec: "h264", CodecProfile: "High", PixelFormat: "yuv420p", Container: "mp4", RequireNoAudio: true, RequireKeyframe: true},
	ProfileVeloxH264720p30V1:  {ID: ProfileVeloxH264720p30V1, Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Codec: "h264", CodecProfile: "High", PixelFormat: "yuv420p", Container: "mp4", RequireNoAudio: true, RequireKeyframe: true},
}

func ResolveProfile(id string) (OutputProfile, error) {
	p, ok := profiles[id]
	if !ok {
		return OutputProfile{}, fmt.Errorf("media: unknown output profile %q", id)
	}
	return p, nil
}

func (p OutputProfile) ValidateProbe(probe ProbeResult) error {
	if probe.Container != p.Container && probe.Container != "mov,mp4,m4a,3gp,3g2,mj2" {
		return fmt.Errorf("profile %s: container %q, want mp4", p.ID, probe.Container)
	}
	if probe.VideoCodec != p.Codec || probe.Width != p.Width || probe.Height != p.Height {
		return fmt.Errorf("profile %s: video=%s %dx%d, want %s %dx%d", p.ID, probe.VideoCodec, probe.Width, probe.Height, p.Codec, p.Width, p.Height)
	}
	if probe.FPSNum != p.FPSNum || probe.FPSDen != p.FPSDen || probe.PixelFormat != p.PixelFormat {
		return fmt.Errorf("profile %s: fps=%d/%d pix_fmt=%s, want %d/%d %s", p.ID, probe.FPSNum, probe.FPSDen, probe.PixelFormat, p.FPSNum, p.FPSDen, p.PixelFormat)
	}
	if probe.CodecProfile != p.CodecProfile {
		return fmt.Errorf("profile %s: codec profile %q, want %q", p.ID, probe.CodecProfile, p.CodecProfile)
	}
	if p.RequireNoAudio && probe.AudioStreams != 0 {
		return fmt.Errorf("profile %s: audio streams=%d, want 0", p.ID, probe.AudioStreams)
	}
	if p.RequireKeyframe && !probe.FirstFrameKeyframe {
		return fmt.Errorf("profile %s: first frame is not a keyframe", p.ID)
	}
	if probe.DurationUS <= 0 || probe.FrameCount <= 0 {
		return fmt.Errorf("profile %s: duration/frame count must be positive", p.ID)
	}
	return nil
}
