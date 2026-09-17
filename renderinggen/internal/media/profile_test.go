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

func TestResolveAssemblyReadyProfile(t *testing.T) {
	p, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1920 || p.Height != 1080 || p.FPSNum != 24 || p.FPSDen != 1 {
		t.Fatalf("unexpected geometry: %+v", p)
	}
	if p.Codec != "h264" || p.CodecProfile != "High" || p.PixelFormat != "yuv420p" || p.Container != "mp4" {
		t.Fatalf("unexpected codec block: %+v", p)
	}
	if p.RequireNoAudio {
		t.Fatalf("assembly-ready clips carry audio: %+v", p)
	}
	// V1 mirrors the VeloxEditing contract values (High canonical). The native
	// lane's certified Main is an explicit accepted value, never a wildcard.
	if len(p.AcceptedCodecProfiles) != 1 || p.AcceptedCodecProfiles[0] != "Main" {
		t.Fatalf("accepted profiles = %v, want exactly [Main]", p.AcceptedCodecProfiles)
	}
	if p.VideoLevel != "4.0" || !p.RequireClosedGOP || p.KeyframeInterval != 48 {
		t.Fatalf("assembly invariants not pinned: %+v", p)
	}
	if p.SARNum != 1 || p.SARDen != 1 || p.ColorRange != "tv" || p.ColorSpace != "bt709" || p.ColorTransfer != "bt709" || p.ColorPrimaries != "bt709" {
		t.Fatalf("pixel identity not pinned: %+v", p)
	}
	if !p.RequireStartPTSZero || p.VideoStreams != 1 || p.AudioStreams != 1 {
		t.Fatalf("stream layout not pinned: %+v", p)
	}
	if p.AudioCodec != "aac" || p.AudioProfile != "LC" || p.AudioSampleRate != 48000 {
		t.Fatalf("audio block not pinned: %+v", p)
	}
}

// assemblyProbe builds the certified fact set a native-lane assembly-ready
// artifact produces today (values taken from a real GPU-native render receipt,
// 2026-09-16: profile Main, level 4.0, closed GOP 48, SAR 1/1, bt709/tv,
// start_pts 0).
func assemblyProbe() ProbeResult {
	return ProbeResult{
		Container:          "mov,mp4,m4a,3gp,3g2,mj2",
		DurationUS:         5_000_000,
		Width:              1920,
		Height:             1080,
		FPSNum:             24,
		FPSDen:             1,
		PixelFormat:        "yuv420p",
		VideoCodec:         "h264",
		CodecProfile:       "Main",
		VideoLevel:         "4.0",
		FrameCount:         120,
		FirstFrameKeyframe: true,
		HasVideo:           true,
		VideoStreams:       1,
		SARNum:             1,
		SARDen:             1,
		ColorRange:         "tv",
		ColorSpace:         "bt709",
		ColorTransfer:      "bt709",
		ColorPrimaries:     "bt709",
		StartPTS:           0,
		KeyframeInterval:   48,
		ClosedGOP:          true,
		HasAudio:           true,
		AudioStreams:       1,
		AudioCodec:         "aac",
		AudioProfile:       "LC",
		SampleRate:         48000,
	}
}

func TestAssemblyReadyProfileAcceptsTheNativeLaneProfile(t *testing.T) {
	p, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateProbe(assemblyProbe()); err != nil {
		t.Fatalf("the native lane's certified artifact must pass the contract mirror: %v", err)
	}
	// The canonical value passes too: the contract declares High and a High
	// encoder lane is not a violation.
	probe := assemblyProbe()
	probe.CodecProfile = "High"
	if err := p.ValidateProbe(probe); err != nil {
		t.Fatalf("canonical High must pass: %v", err)
	}
}

func TestAssemblyReadyProfileRejectsUnlistedProfiles(t *testing.T) {
	p, _ := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	for _, certified := range []string{"Baseline", "Constrained Baseline", "", "main"} {
		probe := assemblyProbe()
		probe.CodecProfile = certified
		if err := p.ValidateProbe(probe); err == nil {
			t.Fatalf("certified profile %q must be refused (the equivalence is finite, not a wildcard)", certified)
		}
	}
}

func TestAssemblyReadyProfileRejectsUnprovenGOP(t *testing.T) {
	p, _ := ResolveProfile(ProfileVeloxAssemblyReadyV1)

	open := assemblyProbe()
	open.ClosedGOP = false
	if err := p.ValidateProbe(open); err == nil {
		t.Fatal("an artifact whose closed GOP was measured false must be refused")
	}

	uncertifiable := assemblyProbe()
	uncertifiable.ClosedGOP = false
	uncertifiable.ClosedGOPUncertifiable = true
	if err := p.ValidateProbe(uncertifiable); err == nil {
		t.Fatal("an artifact whose closed GOP could not be observed must be refused, not assumed")
	}

	wrongInterval := assemblyProbe()
	wrongInterval.KeyframeInterval = 60
	if err := p.ValidateProbe(wrongInterval); err == nil {
		t.Fatal("a GOP interval that disagrees with the assembly contract must be refused")
	}
}

func TestAssemblyReadyProfileRejectsPixelAndTimingDrift(t *testing.T) {
	p, _ := ResolveProfile(ProfileVeloxAssemblyReadyV1)

	cases := map[string]func(*ProbeResult){
		"SAR":         func(probe *ProbeResult) { probe.SARNum = 2 },
		"colour":      func(probe *ProbeResult) { probe.ColorSpace = "bt601" },
		"start PTS":   func(probe *ProbeResult) { probe.StartPTS = 1 },
		"audio codec": func(probe *ProbeResult) { probe.AudioCodec = "opus" },
		"audio rate":  func(probe *ProbeResult) { probe.SampleRate = 44100 },
		"audio count": func(probe *ProbeResult) { probe.AudioStreams = 0 },
		"video count": func(probe *ProbeResult) { probe.VideoStreams = 2 },
	}
	for name, mutate := range cases {
		probe := assemblyProbe()
		mutate(&probe)
		if err := p.ValidateProbe(probe); err == nil {
			t.Fatalf("%s drift must be refused", name)
		}
	}
}

// TestAssemblyReadyV2RegistersButFailsClosedOnTheUncertifiedLevel pins the
// deliberate state of V2: it resolves (no more "unknown output profile") and it
// fails closed while the only certified lane emits level 4.0. Accepting 4.0 for
// a V2 plan would advertise a contract nobody verified.
func TestAssemblyReadyV2RegistersButFailsClosedOnTheUncertifiedLevel(t *testing.T) {
	p, err := ResolveProfile(ProfileVeloxAssemblyReadyV2)
	if err != nil {
		t.Fatalf("V2 must be a registered profile: %v", err)
	}
	if p.VideoLevel != "4.1" {
		t.Fatalf("V2 level = %q, want the contract's 4.1", p.VideoLevel)
	}
	if err := p.ValidateProbe(assemblyProbe()); err == nil {
		t.Fatal("V2 must fail closed on a 4.0 artifact until the renderer is certified at 4.1")
	}
	certified := assemblyProbe()
	certified.VideoLevel = "4.1"
	if err := p.ValidateProbe(certified); err != nil {
		t.Fatalf("a 4.1 artifact must satisfy V2: %v", err)
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
