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
	// Assembly-ready assets must carry exactly the canonical High profile.
	if len(p.AcceptedCodecProfiles) != 0 {
		t.Fatalf("accepted profiles = %v, want none", p.AcceptedCodecProfiles)
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
	// The dimensions whose certified artifacts DISAGREE with the VeloxEditing
	// contract stay unpinned here until that disagreement is resolved: pinning
	// either side would hard-code a decision nobody made. See
	// TestMeasuredArtifactDivergencesStayVisible for the measurements.
	if p.VideoTimeBaseNum != 0 || p.VideoTimeBaseDen != 0 {
		t.Fatalf("V1 must not pin a video timebase (measured 1/12288, contract declares 1/90000): %+v", p)
	}
	if p.AudioTimeBaseNum != 0 || p.AudioTimeBaseDen != 0 {
		t.Fatalf("V1 must not pin an audio timebase: %+v", p)
	}
	if p.AudioChannels != 0 || p.AudioChannelLayout != "" || p.AudioBitrate != "" {
		t.Fatalf("V1 must not pin the source-inherited audio layout: %+v", p)
	}
}

// TestPinRationalZeroMeansNotPinned pins the struct-wide convention the
// optional dimensions rely on: 0/0 means "this profile does not check the
// dimension", while a PINNED dimension whose certified value is missing fails
// (a missing fact must never pass as a match).
func TestPinRationalZeroMeansNotPinned(t *testing.T) {
	if pinned, ok := pinRational(0, 0, 1, 12288); pinned || !ok {
		t.Fatalf("0/0 must be unpinned and always ok, got pinned=%t ok=%t", pinned, ok)
	}
	if pinned, ok := pinRational(1, 12288, 1, 12288); !pinned || !ok {
		t.Fatalf("equal pairs must pass, got pinned=%t ok=%t", pinned, ok)
	}
	if pinned, ok := pinRational(1, 48000, 1, 12288); !pinned || ok {
		t.Fatalf("a different pair must fail, got pinned=%t ok=%t", pinned, ok)
	}
	if pinned, ok := pinRational(1, 12288, 0, 0); !pinned || ok {
		t.Fatalf("a pinned dimension with no certified value must fail, got pinned=%t ok=%t", pinned, ok)
	}
	if pinned, ok := pinRational(2, 4, 1, 2); !pinned || !ok {
		t.Fatalf("cross-multiplication must accept equivalent fractions, got pinned=%t ok=%t", pinned, ok)
	}
}

// TestProfilePinsTimebaseAndAudioLayoutWhenDeclared proves the newly registered
// dimensions are real gates and not decorative fields: a profile that DOES
// declare them refuses a drifting artifact, and their absence on the
// assembly-ready profiles leaves an unconstrained artifact passing (the state
// recorded by TestMeasuredArtifactDivergencesStayVisible).
func TestProfilePinsTimebaseAndAudioLayoutWhenDeclared(t *testing.T) {
	pinned := OutputProfile{
		ID: "test-timebase-audio-layout", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Codec: "h264", CodecProfile: "Main", PixelFormat: "yuv420p", Container: "mp4",
		VideoTimeBaseNum: 1, VideoTimeBaseDen: 12288,
		AudioTimeBaseNum: 1, AudioTimeBaseDen: 48000,
		AudioChannels: 2, AudioChannelLayout: "stereo", AudioBitrate: "128576",
	}
	probe := assemblyProbe()
	probe.CodecProfile = pinned.CodecProfile
	probe.VideoTimeBaseNum, probe.VideoTimeBaseDen = 1, 12288
	probe.AudioTimeBaseNum, probe.AudioTimeBaseDen = 1, 48000
	probe.Channels, probe.ChannelLayout, probe.AudioBitrate = 2, "stereo", "128576"
	if err := pinned.ValidateProbe(probe); err != nil {
		t.Fatalf("a declared dimension must accept its certified value: %v", err)
	}

	cases := map[string]func(*ProbeResult){
		"video timebase": func(p *ProbeResult) { p.VideoTimeBaseNum, p.VideoTimeBaseDen = 1, 90000 },
		"audio timebase": func(p *ProbeResult) { p.AudioTimeBaseNum, p.AudioTimeBaseDen = 1, 44100 },
		"channels":       func(p *ProbeResult) { p.Channels = 1 },
		"channel layout": func(p *ProbeResult) { p.ChannelLayout = "mono" },
		"audio bitrate":  func(p *ProbeResult) { p.AudioBitrate = "58524" },
	}
	for name, mutate := range cases {
		drift := probe
		mutate(&drift)
		if err := pinned.ValidateProbe(drift); err == nil {
			t.Fatalf("declared %s drift must be refused", name)
		}
	}

	// The assembly-ready profile declares none of them, so the same drifting
	// fact passes there — the divergence is deliberate, not an oversight.
	v1, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	unconstrained := assemblyProbe()
	unconstrained.VideoTimeBaseNum, unconstrained.VideoTimeBaseDen = 1, 90000
	unconstrained.Channels, unconstrained.ChannelLayout, unconstrained.AudioBitrate = 1, "mono", "58524"
	if err := v1.ValidateProbe(unconstrained); err != nil {
		t.Fatalf("V1 pins neither the timebase nor the source-inherited audio layout: %v", err)
	}
}

// assemblyProbe builds the canonical assembly-ready fact set (H.264 High,
// level 4.0, closed GOP 48, SAR 1/1, bt709/tv,
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
		CodecProfile:       "High",
		CodecConfigSHA256:  "7201ce80a2b2b32a0c1a89501851099f267c1767d59ff799db744f72032aae5a",
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

func TestAssemblyReadyProfileRequiresCanonicalHighProfile(t *testing.T) {
	p, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateProbe(assemblyProbe()); err != nil {
		t.Fatalf("canonical High must pass: %v", err)
	}
	probe := assemblyProbe()
	probe.CodecProfile = "Main"
	if err := p.ValidateProbe(probe); err == nil {
		t.Fatal("Main must be refused when the shared contract requires High")
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

// overlayVideoOnlyProbe is the measured ffprobe fact set of the final job's
// opaque overlay artifact (`b1486eecefc5f09c…`, r64/r65, 1607 frames,
// 66,958333 s, inspected 2026-11-02). Every field is a fact copied from the
// artifact, not an assumption: the test that consumes it is what makes the
// profile a certificate instead of a claim.
func overlayVideoOnlyProbe() ProbeResult {
	return ProbeResult{
		Container:          "mov,mp4,m4a,3gp,3g2,mj2",
		DurationUS:         66_958_333,
		Width:              1920,
		Height:             1080,
		FPSNum:             24,
		FPSDen:             1,
		PixelFormat:        "yuv420p",
		VideoCodec:         "h264",
		CodecProfile:       "High",
		VideoLevel:         "4.0",
		FrameCount:         1607,
		FirstFrameKeyframe: true,
		HasVideo:           true,
		VideoStreams:       1,
		SARNum:             1,
		SARDen:             1,
		ColorRange:         "tv",
		ColorSpace:         "bt709",
		// The encoder tags the transfer as sRGB; V1 declares bt709. The
		// overlay profile must not pin either value (see overlayVideoOnlyProfile).
		ColorTransfer:    "iec61966-2-1",
		ColorPrimaries:   "bt709",
		StartPTS:         0,
		KeyframeInterval: 48,
		ClosedGOP:        true,
		HasAudio:         false,
		AudioStreams:     0,
	}
}

// TestOverlayVideoOnlyProfileCertifiesTheMeasuredArtifact pins both halves of
// the decision: the video-only profile accepts the real video-only artifact,
// and V1 still refuses it (V1 requires one audio stream), so the two profiles
// cannot collapse into one permissive gate.
func TestOverlayVideoOnlyProfileCertifiesTheMeasuredArtifact(t *testing.T) {
	profile, err := ResolveProfile(ProfileVeloxOverlayVideoOnlyV1)
	if err != nil {
		t.Fatalf("overlay profile must be registered: %v", err)
	}
	if !profile.RequireNoAudio || profile.AudioStreams != 0 {
		t.Fatalf("the overlay profile must require a silent artifact: %+v", profile)
	}
	if profile.VideoLevel != "4.0" || profile.KeyframeInterval != 48 {
		t.Fatalf("video-only profile lost the copy facts: %+v", profile)
	}
	// Closed GOP is certified by the CONSUMER's census, which sees the whole
	// artifact's packet table. The producer cannot require it: see
	// TestOverlayVideoOnlyProfileCertifiesShortBatchWindows.
	if profile.RequireClosedGOP {
		t.Fatalf("the overlay profile must not require a closed-GOP proof: short batches cannot provide one: %+v", profile)
	}
	if err := profile.ValidateProbe(overlayVideoOnlyProbe()); err != nil {
		t.Fatalf("the measured overlay artifact must be certified: %v", err)
	}

	assembly, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := assembly.ValidateProbe(overlayVideoOnlyProbe()); err == nil {
		t.Fatal("V1 must keep refusing a video-only artifact: assigning it would be a false certificate")
	}
}

// TestOverlayVideoOnlyProfileFailsClosedOnAudioAndCopyDrift keeps the profile
// honest about the facts it does claim: an audio track, a different GOP or a
// non-zero start PTS is not this artifact's contract.
func TestOverlayVideoOnlyProfileFailsClosedOnAudioAndCopyDrift(t *testing.T) {
	profile, err := ResolveProfile(ProfileVeloxOverlayVideoOnlyV1)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ProbeResult){
		"audio stream":  func(probe *ProbeResult) { probe.AudioStreams = 1; probe.HasAudio = true },
		"GOP drift":     func(probe *ProbeResult) { probe.KeyframeInterval = 60 },
		"start PTS":     func(probe *ProbeResult) { probe.StartPTS = 1 },
		"colour space":  func(probe *ProbeResult) { probe.ColorSpace = "bt601" },
		"level":         func(probe *ProbeResult) { probe.VideoLevel = "4.1" },
		"frame count 0": func(probe *ProbeResult) { probe.FrameCount = 0 },
	}
	for name, mutate := range cases {
		probe := overlayVideoOnlyProbe()
		mutate(&probe)
		if err := profile.ValidateProbe(probe); err == nil {
			t.Fatalf("%s drift must be refused", name)
		}
	}

	// Colour transfer stays UNPINNED on purpose: the measured artifact reports
	// the sRGB tag in a bt709 pipeline, and pinning either value would encode a
	// decision nobody made.
	for _, transfer := range []string{"bt709", "iec61966-2-1", ""} {
		probe := overlayVideoOnlyProbe()
		probe.ColorTransfer = transfer
		if err := profile.ValidateProbe(probe); err != nil {
			t.Fatalf("colour transfer %q must stay unpinned: %v", transfer, err)
		}
	}

	// Closed GOP stays UNPINNED on purpose: the proof needs two full cadence
	// intervals, and a fused batch window can be shorter than one. An open-GOP
	// probe that still shows the 48-frame cadence is therefore certified here —
	// the consumer census, not the producer, refuses to copy it.
	open := overlayVideoOnlyProbe()
	open.ClosedGOP = false
	open.ClosedGOPUncertifiable = true
	if err := profile.ValidateProbe(open); err != nil {
		t.Fatalf("closed GOP must stay unpinned at finalize: %v", err)
	}
}

// TestOverlayVideoOnlyProfileCertifiesShortBatchWindows is the regression test
// for the r79 failure (`closed GOP not certified`). The batch set of the r78
// payload is made of windows from 0.54 s to 8 s (13 to 192 frames; measured
// from the batch manifests), so most artifacts carry at most two keyframes and
// CANNOT prove a cadence. Requiring the proof at finalize rejected real,
// correct batches. The profile must certify them and still fail closed on the
// facts a short window can prove (first frame IDR, start PTS, geometry, audio,
// and an observable cadence drift).
func TestOverlayVideoOnlyProfileCertifiesShortBatchWindows(t *testing.T) {
	profile, err := ResolveProfile(ProfileVeloxOverlayVideoOnlyV1)
	if err != nil {
		t.Fatal(err)
	}

	short := overlayVideoOnlyProbe()
	short.DurationUS = 791_666 // 19 frames at 24 fps
	short.FrameCount = 19
	short.KeyframeInterval = 0 // no cadence observable in a single GOP
	short.ClosedGOP = false
	short.ClosedGOPUncertifiable = true
	if err := profile.ValidateProbe(short); err != nil {
		t.Fatalf("a short batch window must certify: %v", err)
	}

	notIDR := short
	notIDR.FirstFrameKeyframe = false
	if err := profile.ValidateProbe(notIDR); err == nil {
		t.Fatal("a short window whose first frame is not IDR must be refused")
	}

	drifted := short
	drifted.KeyframeInterval = 60
	if err := profile.ValidateProbe(drifted); err == nil {
		t.Fatal("an observable cadence drift must still be refused")
	}
}
