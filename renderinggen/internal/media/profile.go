package media

import (
	"fmt"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// OutputProfile is the stable media contract selected by a render plan.
// Encoder implementation details are intentionally not part of the ID.
//
// Every dimension after the mandatory geometry/codec block is OPTIONAL in the
// sense that its zero value means "not pinned by this profile": a profile that
// does not declare a dimension does not validate it. The assembly-ready
// profiles (VELOX_ASSEMBLY_READY_V1/V2) pin the full set of stream facts the
// downstream concat -c copy assembler depends on, so a certified artifact can
// never be assembled on a fact nobody checked.
//
// The assembly-ready values are a MIRROR of the VeloxEditing assembly contract
// (VeloxEditing:refactored/internal/kernel/media/assembly_contract.go) and are
// kept honest by TestAssemblyReadyProfilesMirrorVeloxContract, which reads that
// contract from the workspace siblings and fails on any drift.
type OutputProfile struct {
	ID     string
	Width  int
	Height int
	FPSNum int
	FPSDen int
	Codec  string
	// CodecProfile is the CANONICAL profile value declared by the VeloxEditing
	// assembly contract this profile mirrors, in ffprobe casing ("High").
	CodecProfile string
	// CodecConfigSHA256 pins the exact AVCDecoderConfigurationRecord
	// (SPS/PPS) when this artifact will be joined to other packet-copied MP4s.
	// Empty leaves the codec parameter-set family unpinned.
	CodecConfigSHA256 string
	// AcceptedCodecProfiles is intentionally empty for assembly-ready output.
	// Every newly produced asset must have the same codec profile as the
	// canonical assembly contract; accepting Main alongside High made overlays
	// pass validation with a different H.264 bitstream profile.
	AcceptedCodecProfiles []string
	PixelFormat           string
	Container             string
	RequireNoAudio        bool
	RequireKeyframe       bool
	// VideoLevel is the dotted H.264/H.265 level the artifact must carry
	// (ProbeResult.VideoLevel); empty = not pinned.
	VideoLevel string
	// RequireClosedGOP requires the certified closed-GOP cadence. The copy-only
	// assembler concatenates segments without decoding, so a chunk whose GOP
	// boundaries are not proven closed can never be proven safe to concatenate.
	RequireClosedGOP bool
	// KeyframeInterval is the measured GOP length in frames
	// (ProbeResult.KeyframeInterval); 0 = not pinned.
	KeyframeInterval int
	// SARNum/SARDen is the sample aspect ratio; 0/0 = not pinned.
	SARNum int
	SARDen int
	// VideoTimeBaseNum/Den is the video stream's timebase; 0/0 = not pinned.
	// No assembly-ready profile pins it today: the certified artifact carries
	// 1/12288 while the VeloxEditing contract declares 1/90000, and neither
	// value may be hard-coded before that disagreement is resolved (see
	// TestMeasuredArtifactDivergencesStayVisible). The dimension exists here so
	// that resolving it is a profile change, not new machinery.
	VideoTimeBaseNum int
	VideoTimeBaseDen int
	// AudioTimeBaseNum/Den is the audio stream's timebase; 0/0 = not pinned.
	AudioTimeBaseNum int
	AudioTimeBaseDen int
	// Colour block (empty = not pinned). Each dimension is validated only when
	// ffprobe actually reported it on the artifact.
	ColorRange     string
	ColorSpace     string
	ColorTransfer  string
	ColorPrimaries string
	// RequireStartPTSZero pins the first video packet's start timestamp to 0,
	// as the assembly contract requires.
	RequireStartPTSZero bool
	// VideoStreams/AudioStreams pin the stream counts; 0 = not pinned.
	VideoStreams int
	AudioStreams int
	// Audio block (empty/0 = not pinned).
	AudioCodec      string
	AudioProfile    string
	AudioSampleRate int
	// AudioChannels/AudioChannelLayout/AudioBitrate describe the SOURCE-derived
	// audio layout. No assembly-ready profile pins them: the audio lane is
	// copy-first, so a mono source yields a mono artifact and pinning the
	// contract's 2ch/stereo/128k would fail closed on legal renders (two
	// certified renders disagree — see profile.go's certified-facts block).
	AudioChannels      int
	AudioChannelLayout string
	AudioBitrate       string
}

const (
	ProfileVeloxAssemblyReadyV1 = "VELOX_ASSEMBLY_READY_V1"
	// ProfileVeloxAssemblyReadyV2 is the canonical contract for NEWLY produced
	// clips (VeloxEditing's V2: level 4.1, 192k audio). It is registered so a
	// V2 plan resolves instead of failing as an unknown profile, and it is
	// deliberately STRICT: the native lane currently certifies level 4.0, so a
	// V2 plan fails closed at finalize until the renderer is certified at 4.1.
	// Failing closed is the intended state — accepting 4.0 for a V2 plan would
	// advertise a contract nobody verified.
	ProfileVeloxAssemblyReadyV2 = "VELOX_ASSEMBLY_READY_V2"
	// ProfileVeloxOverlayVideoOnlyV1 is the certification profile for the
	// opaque overlay compositions the final job renders (a packed manifest
	// timeline or a source composition): H.264 High 4.0, 1920x1080@24,
	// yuv420p, closed GOP 48, first frame IDR, start PTS 0 — and NO audio.
	//
	// It exists because VELOX_ASSEMBLY_READY_V1 pins AudioStreams: 1, so
	// declaring V1 on a video-only artifact would be a certificate nobody
	// can honour. The overlay artifact is not an assembly-ready clip; it is
	// a certified input to the assembler, and it says so.
	ProfileVeloxOverlayVideoOnlyV1 = "VELOX_OVERLAY_VIDEO_ONLY_V1"
	// V2 is the new overlay SOURCE lane. It deliberately does not pin the
	// avcC: the final-job fragment producer re-encodes any source whose
	// parameter sets are not canonical. V1 remains registered for old
	// artifacts, but new renders use V2 so cached V1 bytes cannot be reused
	// under the new lane.
	ProfileVeloxOverlayVideoOnlyV2 = "VELOX_OVERLAY_VIDEO_ONLY_V2"
	// ProfileVeloxH2641080p30V1 is the certified copy-ready profile id. It is
	// owned by the wire contract (queue/client) because the artifact's
	// profile_id is what downstream assemblers select on; this registry aliases
	// it so the worker and the queue can never disagree about the value.
	ProfileVeloxH2641080p30V1 = queueclient.CertifiedProfileVeloxH2641080p30V1
	ProfileVeloxH264720p30V1  = "velox-h264-720p30-v1"
)

// assemblyReadyProfile returns the shared VeloxEditing assembly invariants: the
// stream facts the concat -c copy assembler and the consumer contract gate
// depend on, sourced from the VeloxEditing assembly contract (V1/V2 differ only
// in level — see profiles below).
//
// ── Historical artifact facts (MEASURED, 2026-09-17) ──────────────────
//
// The values below record the old Main-profile encoder output that motivated
// the mismatch fix; they are no longer accepted as the canonical profile.
// New output must be H.264 High. Source of truth for each measurement is the
// RenderingGen queue's own artifact record for the certified render —
// curl $RENDERINGGEN_QUEUE_URL/jobs/<render job id> → .artifact.output_facts
// (job_1789645353996117571_87c8a072, VELOX_ASSEMBLY_READY_V1, 1920x1080@24,
// Chronon 0.1.0, backend chronon_vulkan):
//
//	h264 Main 4.0 yuv420p, 1/1 SAR, tv/bt709 progressive, GOP 48 closed,
//	first frame keyframe, start PTS 0, 1 video + 1 audio,
//	AAC-LC 48000 stereo, copy_eligible true,
//	video timebase 1/12288, audio timebase 1/48000, audio bitrate 128576.
//
// Dimensions this profile deliberately does NOT pin, with the measurement that
// keeps them open (see TestMeasuredArtifactDivergencesStayVisible, which fails
// if either side of each divergence changes):
//
//   - video timebase: the historical artifact carries 1/12288 (= fps_den × 512, the
//     mp4 muxer's convention) while the VeloxEditing contract declares
//     1/90000. The CONSUMER gate resolves this by an explicit tolerance
//     (cliprender/contract.go's video-timebase dimension accepts the known
//     muxer timebases 12288/90000/24000/15360/12800), so the renderer
//     deliberately pins nothing: the tolerance belongs to the assembler, and a
//     producer-side pin would be a second, stricter rule over the same fact.
//   - audio channels / channel layout / bitrate: the renderer's audio lane is
//     copy-first (audio_copy_eligible=true), so the artifact inherits the
//     SOURCE layout, while the consumer gate requires the contract's channels
//     EXACTLY (cliprender/contract.go checkAudioBlock). Two certified renders
//     disagree (mono/58524 on 2026-09-16, stereo/128576 on 2026-09-17), so
//     pinning the contract's 2ch/stereo/128k here would fail closed on a mono
//     source that the master's copy_if_compatible audio mode may legally copy.
//     Naming the missing measurement: a mono-SOURCE clip-lane render would show
//     whether the plan's "copy_if_compatible" transcodes to the declared 2ch or
//     copies the mono layout (and therefore which side must change).
//
// Audio codec/profile/sample rate ARE pinned: the lane enforces the declared
// output rate (--audio-sample-rate) and AAC-LC is what the muxer emits, so
// those three are properties of the renderer rather than of the source.
func assemblyReadyProfile() OutputProfile {
	return OutputProfile{
		Width:        1920,
		Height:       1080,
		FPSNum:       24,
		FPSDen:       1,
		Codec:        "h264",
		CodecProfile: "High",
		// The exact avcC (SPS/PPS) identity of the canonical assembly lane.
		// A High 4.0 profile with a different SPS/PPS is not packet-copy
		// compatible: concat -c copy writes only the first segment's avcC.
		CodecConfigSHA256:   "7201ce80a2b2b32a0c1a89501851099f267c1767d59ff799db744f72032aae5a",
		PixelFormat:         "yuv420p",
		Container:           "mp4",
		RequireNoAudio:      false,
		RequireKeyframe:     true,
		RequireClosedGOP:    true,
		KeyframeInterval:    48,
		SARNum:              1,
		SARDen:              1,
		ColorRange:          "tv",
		ColorSpace:          "bt709",
		ColorTransfer:       "bt709",
		ColorPrimaries:      "bt709",
		RequireStartPTSZero: true,
		VideoStreams:        1,
		AudioStreams:        1,
		AudioCodec:          "aac",
		AudioProfile:        "LC",
		AudioSampleRate:     48000,
	}
}

// overlayVideoOnlyProfile returns the measured stream identity of the final
// job's opaque overlay artifact (packed manifest timeline). Provenance:
// `b1486eecefc5f09c…` (r64/r65, 1607 frames, 66,958333 s) inspected with
// ffprobe on 2026-11-02: h264 High 4.0 1920x1080 yuv420p 24 fps, has_b_frames=0,
// SAR 1:1, tv/bt709, 34 keyframes at GOP 48, first packet IDR at pts 0,
// one video stream and ZERO audio streams.
//
// Three dimensions the assembly-ready profile pins are deliberately NOT pinned
// here, because pinning any of them would make the certificate unsatisfiable or
// false:
//
//   - colour transfer: the artifact reports `iec61966-2-1` (the sRGB transfer
//     tag the encoder attaches), while V1 declares bt709. Both are real; the
//     renderer does not own that tag today, so this profile pins range/space/
//     primaries (measured tv/bt709/bt709) and leaves transfer unpinned.
//   - video timebase: the muxer emits 1/12288; the consumer accepts it and
//     remuxes, so the producer does not pin it (same rule as V1).
//   - closed GOP: the cadence rule needs at least three keyframes (two full GOP
//     intervals) to prove anything, and the fused batch set is 72–306 frames
//     (3,0–12,75 s, measured on r80/r82); its two shortest windows (72 and 84
//     frames) carry a single interval. Requiring the proof made the profile
//     reject real, correct batches at finalize (`closed GOP not certified`,
//     r79). The consumer's census derives
//     closed GOP from the artifact's own packet table and remains the copy gate,
//     so the producer certifies only what every artifact can prove: the first
//     frame is IDR and, WHEN a cadence is observable, it is exactly 48 frames.
//     A short artifact therefore has no cadence claim, not a false one.
//
// Audio: this is the whole point of the profile — RequireNoAudio and
// AudioStreams: 0, with no audio codec/sample-rate pins, because there is no
// audio track to describe.
func overlayVideoOnlyProfile() OutputProfile {
	return OutputProfile{
		Width:               1920,
		Height:              1080,
		FPSNum:              24,
		FPSDen:              1,
		Codec:               "h264",
		CodecProfile:        "High",
		PixelFormat:         "yuv420p",
		Container:           "mp4",
		RequireNoAudio:      true,
		RequireKeyframe:     true,
		KeyframeInterval:    48,
		SARNum:              1,
		SARDen:              1,
		ColorRange:          "tv",
		ColorSpace:          "bt709",
		ColorPrimaries:      "bt709",
		RequireStartPTSZero: true,
		VideoStreams:        1,
		AudioStreams:        0,
	}
}

var profiles = map[string]OutputProfile{
	ProfileVeloxOverlayVideoOnlyV1: func() OutputProfile {
		p := overlayVideoOnlyProfile()
		p.ID = ProfileVeloxOverlayVideoOnlyV1
		p.VideoLevel = "4.0"
		return p
	}(),
	ProfileVeloxOverlayVideoOnlyV2: func() OutputProfile {
		p := overlayVideoOnlyProfile()
		p.ID = ProfileVeloxOverlayVideoOnlyV2
		p.VideoLevel = "4.0"
		// This profile certifies the overlay SOURCE artifact, not an assembly
		// segment. It deliberately leaves CodecConfigSHA256 unpinned: the
		// fragment producer re-encodes any source whose avcC is not canonical,
		// and the copy/assembly gates compare the actual bytes. Pinning a
		// source hash here would reject every overlay the renderer legitimately
		// produces with a different encoder session.
		return p
	}(),
	ProfileVeloxAssemblyReadyV1: func() OutputProfile {
		p := assemblyReadyProfile()
		p.ID = ProfileVeloxAssemblyReadyV1
		p.VideoLevel = "4.0"
		return p
	}(),
	ProfileVeloxAssemblyReadyV2: func() OutputProfile {
		p := assemblyReadyProfile()
		p.ID = ProfileVeloxAssemblyReadyV2
		p.VideoLevel = "4.1"
		return p
	}(),
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

// acceptsProfile reports whether a certified ffprobe profile satisfies this
// profile: the canonical value or one of the explicitly accepted encoder-lane
// values. An empty certified value never satisfies a pinned canonical profile
// (it means "not reported", and a missing fact must not pass a check).
// pinRational reports whether a num/den pair is pinned (both sides non-zero)
// and, if so, whether the certified pair equals it by cross-multiplication.
// The zero rule is the struct-wide convention: 0 means "this profile does not
// pin the dimension", so an unpinned dimension never fails a check.
func pinRational(wantNum, wantDen, gotNum, gotDen int) (pinned bool, ok bool) {
	if wantNum == 0 || wantDen == 0 {
		return false, true
	}
	if gotNum == 0 || gotDen == 0 {
		// Not reported by the certification: a missing fact must not pass as a
		// match (the same rule acceptsProfile states for a missing profile).
		return true, false
	}
	return true, wantNum*gotDen == gotNum*wantDen
}

func (p OutputProfile) acceptsProfile(certified string) bool {
	if certified == p.CodecProfile {
		return true
	}
	for _, accepted := range p.AcceptedCodecProfiles {
		if certified == accepted {
			return true
		}
	}
	return false
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
	if !p.acceptsProfile(probe.CodecProfile) {
		return fmt.Errorf("profile %s: codec profile %q, want %q", p.ID, probe.CodecProfile, p.CodecProfile)
	}
	if p.VideoLevel != "" && probe.VideoLevel != "" && probe.VideoLevel != p.VideoLevel {
		return fmt.Errorf("profile %s: video level %q, want %q", p.ID, probe.VideoLevel, p.VideoLevel)
	}
	if p.CodecConfigSHA256 != "" && probe.CodecConfigSHA256 != p.CodecConfigSHA256 {
		return fmt.Errorf("profile %s: SPS/PPS fingerprint %q, want %q", p.ID, probe.CodecConfigSHA256, p.CodecConfigSHA256)
	}
	if p.RequireNoAudio && probe.AudioStreams != 0 {
		return fmt.Errorf("profile %s: audio streams=%d, want 0", p.ID, probe.AudioStreams)
	}
	if p.RequireKeyframe && !probe.FirstFrameKeyframe {
		return fmt.Errorf("profile %s: first frame is not a keyframe", p.ID)
	}
	if p.RequireClosedGOP && !probe.ClosedGOP {
		reason := "closed GOP not certified"
		if probe.ClosedGOPUncertifiable {
			reason = "closed GOP uncertifiable (the sync-sample table could not be observed)"
		}
		return fmt.Errorf("profile %s: %s", p.ID, reason)
	}
	if p.KeyframeInterval > 0 && probe.KeyframeInterval > 0 && probe.KeyframeInterval != p.KeyframeInterval {
		return fmt.Errorf("profile %s: keyframe interval %d, want %d", p.ID, probe.KeyframeInterval, p.KeyframeInterval)
	}
	if pinned, ok := pinRational(p.SARNum, p.SARDen, probe.SARNum, probe.SARDen); pinned && !ok {
		return fmt.Errorf("profile %s: SAR %d/%d, want %d/%d", p.ID, probe.SARNum, probe.SARDen, p.SARNum, p.SARDen)
	}
	if pinned, ok := pinRational(p.VideoTimeBaseNum, p.VideoTimeBaseDen, probe.VideoTimeBaseNum, probe.VideoTimeBaseDen); pinned && !ok {
		return fmt.Errorf("profile %s: video timebase %d/%d, want %d/%d", p.ID, probe.VideoTimeBaseNum, probe.VideoTimeBaseDen, p.VideoTimeBaseNum, p.VideoTimeBaseDen)
	}
	if pinned, ok := pinRational(p.AudioTimeBaseNum, p.AudioTimeBaseDen, probe.AudioTimeBaseNum, probe.AudioTimeBaseDen); pinned && !ok {
		return fmt.Errorf("profile %s: audio timebase %d/%d, want %d/%d", p.ID, probe.AudioTimeBaseNum, probe.AudioTimeBaseDen, p.AudioTimeBaseNum, p.AudioTimeBaseDen)
	}
	if err := p.validateColour(probe); err != nil {
		return err
	}
	if p.RequireStartPTSZero && probe.StartPTS != 0 {
		return fmt.Errorf("profile %s: start_pts=%d, want 0", p.ID, probe.StartPTS)
	}
	if p.VideoStreams > 0 && probe.VideoStreams > 0 && probe.VideoStreams != p.VideoStreams {
		return fmt.Errorf("profile %s: video streams=%d, want %d", p.ID, probe.VideoStreams, p.VideoStreams)
	}
	if p.AudioStreams > 0 && p.AudioStreams != probe.AudioStreams {
		return fmt.Errorf("profile %s: audio streams=%d, want %d", p.ID, probe.AudioStreams, p.AudioStreams)
	}
	if p.AudioCodec != "" && probe.AudioCodec != "" && probe.AudioCodec != p.AudioCodec {
		return fmt.Errorf("profile %s: audio codec %q, want %q", p.ID, probe.AudioCodec, p.AudioCodec)
	}
	if p.AudioProfile != "" && probe.AudioProfile != "" && probe.AudioProfile != p.AudioProfile {
		return fmt.Errorf("profile %s: audio profile %q, want %q", p.ID, probe.AudioProfile, p.AudioProfile)
	}
	if p.AudioSampleRate > 0 && probe.SampleRate > 0 && probe.SampleRate != p.AudioSampleRate {
		return fmt.Errorf("profile %s: sample rate %d, want %d", p.ID, probe.SampleRate, p.AudioSampleRate)
	}
	if p.AudioChannels > 0 && probe.Channels > 0 && probe.Channels != p.AudioChannels {
		return fmt.Errorf("profile %s: audio channels %d, want %d", p.ID, probe.Channels, p.AudioChannels)
	}
	if p.AudioChannelLayout != "" && probe.ChannelLayout != "" && probe.ChannelLayout != p.AudioChannelLayout {
		return fmt.Errorf("profile %s: channel layout %q, want %q", p.ID, probe.ChannelLayout, p.AudioChannelLayout)
	}
	if p.AudioBitrate != "" && probe.AudioBitrate != "" && probe.AudioBitrate != p.AudioBitrate {
		return fmt.Errorf("profile %s: audio bitrate %q, want %q", p.ID, probe.AudioBitrate, p.AudioBitrate)
	}
	if probe.DurationUS <= 0 || probe.FrameCount <= 0 {
		return fmt.Errorf("profile %s: duration/frame count must be positive", p.ID)
	}
	return nil
}

// validateColour checks each pinned colour dimension only when the probe
// reported it: "not reported" is not a violation (the fact stays covered by the
// consumer contract gate), while a reported mismatch is.
func (p OutputProfile) validateColour(probe ProbeResult) error {
	dimensions := []struct {
		name      string
		want      string
		certified string
	}{
		{"color_range", p.ColorRange, probe.ColorRange},
		{"color_space", p.ColorSpace, probe.ColorSpace},
		{"color_transfer", p.ColorTransfer, probe.ColorTransfer},
		{"color_primaries", p.ColorPrimaries, probe.ColorPrimaries},
	}
	for _, d := range dimensions {
		if d.want != "" && d.certified != "" && d.certified != d.want {
			return fmt.Errorf("profile %s: %s %q, want %q", p.ID, d.name, d.certified, d.want)
		}
	}
	return nil
}
