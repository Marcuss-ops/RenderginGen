// Package media certifies rendered artifacts with ffprobe/ffmpeg: structural
// probe facts (ProbeFile), closed-GOP cadence (gop_probe.go) and the
// post-probe validation gates (validate.go).
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// ProbeResult is the stable media surface needed to certify a rendered
// artifact. It is intentionally derived from ffprobe, never from Chronon's
// exit code or the render plan alone.
//
// It is the CERTIFICATION OWNER for every structural dimension the clip.render
// output contract validates: a consumer (PipelineGen) that cannot probe a
// dimension itself consumes the value certified here instead of skipping the
// check. That is why the set below is complete rather than minimal.
type ProbeResult struct {
	Container          string
	DurationUS         int64
	Width              int
	Height             int
	FPSNum             int
	FPSDen             int
	PixelFormat        string
	VideoCodec         string
	CodecProfile       string
	FrameCount         int
	FirstFrameKeyframe bool
	// HasVideo and VideoStreams are the video-presence/stream-layout facts the
	// output contract validates (video-stream-present, video-streams).
	HasVideo     bool
	VideoStreams int
	// VideoLevel is the H.264/H.265 level as a dotted string ("4.1"), derived
	// from ffprobe's integer level field. Empty when ffprobe did not report it.
	VideoLevel string
	// VideoTimeBaseNum/Den and AudioTimeBaseNum/Den are the exact stream
	// timebases (ffprobe time_base), compared by cross-multiplication.
	VideoTimeBaseNum int
	VideoTimeBaseDen int
	AudioTimeBaseNum int
	AudioTimeBaseDen int
	// SARNum/SARDen is the sample aspect ratio (ffprobe sample_aspect_ratio).
	SARNum int
	SARDen int
	// Colour facts (ffprobe color_* fields) — empty when ffprobe did not
	// report them.
	ColorRange     string
	ColorSpace     string
	ColorTransfer  string
	ColorPrimaries string
	FieldOrder     string
	// StartPTS is the first video packet's start timestamp. The assembly-ready
	// contract requires 0.
	StartPTS int64
	// KeyframeInterval is the measured GOP length in frames (packet-index
	// spacing between uniform keyframes). 0 when the cadence could not be
	// proven (see ClosedGOPUncertifiable).
	KeyframeInterval int
	// Audio facts. AudioStreams counts audio streams; the codec/profile/rate/
	// channels/layout/bitrate are the first audio stream's, matching the audio
	// contract block.
	AudioStreams  int
	HasAudio      bool
	AudioCodec    string
	AudioProfile  string
	SampleRate    int
	Channels      int
	ChannelLayout string
	AudioBitrate  string
	// ClosedGOP certifies a uniform closed-GOP structure from the container's
	// sync-sample table (see closedGOPPositionsCadence): the stream starts with a
	// keyframe and every GOP boundary occurs at a strictly regular interval.
	// It is a conservative proxy for closed-GOP encoding — it is never derived
	// from FirstFrameKeyframe alone and fails closed (false) when the cadence
	// cannot be proven.
	ClosedGOP bool
	// ClosedGOPUncertifiable is true when the closed-GOP probe could not
	// observe the sync-sample table at all (ffprobe missing, unreadable file,
	// undecodable packet table). ClosedGOP is then false because nothing was
	// proven, NOT because the cadence was measured and found non-uniform.
	// Consumers must distinguish the two: an uncertifiable probe means the
	// certification input is unavailable (environment problem), a certified
	// false means the artifact really carries an irregular GOP structure.
	ClosedGOPUncertifiable bool
	// FPSUncertifiable is true when the video stream's r_frame_rate could not
	// be parsed into positive integers (for example "0/0"). Certification
	// contracts that REQUIRE an fps (ValidateOverlay with a declared rate)
	// treat this as an error; it is never silently treated as "fps matches".
	FPSUncertifiable bool
}

type ffprobeDocument struct {
	Streams []struct {
		Index             int    `json:"index"`
		CodecType         string `json:"codec_type"`
		CodecName         string `json:"codec_name"`
		Width             int    `json:"width"`
		Height            int    `json:"height"`
		PixFmt            string `json:"pix_fmt"`
		Rate              string `json:"r_frame_rate"`
		Profile           string `json:"profile"`
		Level             int    `json:"level"`
		NbFrames          string `json:"nb_frames"`
		TimeBase          string `json:"time_base"`
		SampleAspectRatio string `json:"sample_aspect_ratio"`
		ColorRange        string `json:"color_range"`
		ColorSpace        string `json:"color_space"`
		ColorTransfer     string `json:"color_transfer"`
		ColorPrimaries    string `json:"color_primaries"`
		FieldOrder        string `json:"field_order"`
		StartPTS          *int64 `json:"start_pts"`
		SampleRate        string `json:"sample_rate"`
		Channels          int    `json:"channels"`
		ChannelLayout     string `json:"channel_layout"`
		BitRate           string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

func ProbeFile(ctx context.Context, path string) (ProbeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var doc ffprobeDocument
	if err := json.Unmarshal(out, &doc); err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe decode %s: %w", path, err)
	}
	result := ProbeResult{Container: doc.Format.FormatName}
	if duration, err := strconv.ParseFloat(doc.Format.Duration, 64); err == nil && duration > 0 {
		result.DurationUS = int64(duration * 1_000_000)
	}
	videoSeen, audioSeen := false, false
	for _, stream := range doc.Streams {
		if stream.CodecType == "audio" {
			result.AudioStreams++
			if !audioSeen {
				audioSeen = true
				result.AudioCodec = stream.CodecName
				result.AudioProfile = stream.Profile
				result.ChannelLayout = stream.ChannelLayout
				result.Channels = stream.Channels
				result.AudioBitrate = strings.TrimSpace(stream.BitRate)
				if rate, err := strconv.Atoi(strings.TrimSpace(stream.SampleRate)); err == nil && rate > 0 {
					result.SampleRate = rate
				}
				result.AudioTimeBaseNum, result.AudioTimeBaseDen = parseRational(stream.TimeBase)
			}
			continue
		}
		if stream.CodecType != "video" {
			continue
		}
		result.VideoStreams++
		if videoSeen {
			continue
		}
		videoSeen = true
		result.HasVideo = true
		result.VideoCodec = stream.CodecName
		result.CodecProfile = stream.Profile
		result.VideoLevel = levelString(stream.Level)
		if n, err := strconv.Atoi(stream.NbFrames); err == nil {
			result.FrameCount = n
		}
		result.Width, result.Height = stream.Width, stream.Height
		result.PixelFormat = stream.PixFmt
		result.ColorRange = stream.ColorRange
		result.ColorSpace = stream.ColorSpace
		result.ColorTransfer = stream.ColorTransfer
		result.ColorPrimaries = stream.ColorPrimaries
		result.FieldOrder = stream.FieldOrder
		if stream.StartPTS != nil && *stream.StartPTS > 0 {
			result.StartPTS = *stream.StartPTS
		}
		result.VideoTimeBaseNum, result.VideoTimeBaseDen = parseRational(stream.TimeBase)
		result.SARNum, result.SARDen = parseRational(stream.SampleAspectRatio)
		parts := strings.SplitN(stream.Rate, "/", 2)
		if len(parts) == 2 {
			num, numErr := strconv.Atoi(parts[0])
			den, denErr := strconv.Atoi(parts[1])
			// A parseable rate must be positive on both sides: "0/0" (or a
			// malformed string) means the fps contract cannot be certified, and
			// the artifact must fail a declared fps check — never silently
			// exempt it (the gate must not depend on data the same function
			// just failed to parse).
			if numErr != nil || denErr != nil || num <= 0 || den <= 0 {
				result.FPSUncertifiable = true
				log.Printf("media: ffprobe %s: unparseable r_frame_rate %q; fps contract cannot be certified", path, stream.Rate)
			} else {
				result.FPSNum, result.FPSDen = num, den
			}
		} else {
			result.FPSUncertifiable = true
			log.Printf("media: ffprobe %s: malformed r_frame_rate %q; fps contract cannot be certified", path, stream.Rate)
		}
	}
	result.HasAudio = result.AudioStreams > 0

	// A packet-copy segment must begin with an IDR/key frame. Only inspect the
	// first decoded frame. The previous implementation used -show_frames with
	// no interval, which walked and serialized every frame merely to read index
	// zero and could become a sizeable post-render tax on long clips.
	var frames struct {
		Frames []struct {
			Key int `json:"key_frame"`
		} `json:"frames"`
	}
	frameCmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-read_intervals", "%+#1", "-show_frames", "-show_entries", "frame=key_frame", "-of", "json", path)
	if frameOut, err := frameCmd.Output(); err == nil && json.Unmarshal(frameOut, &frames) == nil && len(frames.Frames) > 0 {
		result.FirstFrameKeyframe = frames.Frames[0].Key == 1
	} else if err != nil {
		// Fail closed (first_frame_keyframe stays false) but never silently:
		// a systematically broken keyframe probe would otherwise produce
		// first_frame_keyframe=false forever with no signal pointing at
		// ffprobe, silently disabling the copy-eligible fast path.
		log.Printf("media: ffprobe %s: first-frame keyframe probe unavailable: %v (first_frame_keyframe=false)", path, err)
	}

	// MP4 normally exposes nb_frames in the stream metadata above. Keep an
	// exact fallback for containers that do not, but pay the full frame-count
	// scan only in that exceptional case instead of on every render.
	if result.FrameCount == 0 {
		countCmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
			"-show_entries", "stream=nb_read_frames", "-of", "default=nokey=1:noprint_wrappers=1", path)
		if countOut, err := countCmd.Output(); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(countOut))); err == nil {
				result.FrameCount = n
			}
		} else {
			log.Printf("media: ffprobe %s: frame-count fallback unavailable: %v (frame_count=0)", path, err)
		}
	}

	// Closed-GOP certification: reads only the container packet table (no
	// decode), so the cadence check is cheap even on long clips. The probe
	// reports whether the verdict was actually observed (see
	// ClosedGOPUncertifiable) so "no ffprobe" is never confused with "open
	// GOP". The same scan yields the measured GOP length.
	result.ClosedGOP, result.ClosedGOPUncertifiable, result.KeyframeInterval = probeClosedGOP(ctx, path)
	return result, nil
}

// parseRational parses ffprobe's "num/den" (time_base) and "num:den"
// (sample_aspect_ratio) forms. A missing, zero or non-positive side yields
// (0, 0) — "not reported", which the contract gate treats as unreported rather
// than as a mismatch.
func parseRational(raw string) (num, den int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0
	}
	sep := "/"
	if strings.Contains(raw, ":") {
		sep = ":"
	}
	parts := strings.SplitN(raw, sep, 2)
	if len(parts) != 2 {
		return 0, 0
	}
	n, nErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	d, dErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if nErr != nil || dErr != nil || n <= 0 || d <= 0 {
		return 0, 0
	}
	return n, d
}

// levelString renders ffprobe's integer level (41) as the contract's dotted
// form ("4.1"). Zero means ffprobe did not report a level.
func levelString(level int) string {
	if level <= 0 {
		return ""
	}
	return strconv.Itoa(level/10) + "." + strconv.Itoa(level%10)
}
