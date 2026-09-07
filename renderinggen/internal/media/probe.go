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

// ProbeResult is the small, stable media surface needed to certify an overlay
// artifact. It is intentionally derived from ffprobe, never from Chronon's
// exit code or the render plan alone.
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
	// ClosedGOP certifies a uniform closed-GOP structure from the container's
	// sync-sample table (see closedGOPCadence): the stream starts with a
	// keyframe and every GOP boundary occurs at a strictly regular interval.
	// It is a conservative proxy for closed-GOP encoding — it is never derived
	// from FirstFrameKeyframe alone and fails closed (false) when the cadence
	// cannot be proven.
	ClosedGOP    bool
	AudioStreams int
	// FPSUncertifiable is true when the video stream's r_frame_rate could not
	// be parsed into positive integers (for example "0/0"). Certification
	// contracts that REQUIRE an fps (ValidateOverlay with a declared rate)
	// treat this as an error; it is never silently treated as "fps matches".
	FPSUncertifiable bool
}

type ffprobeDocument struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		PixFmt    string `json:"pix_fmt"`
		Rate      string `json:"r_frame_rate"`
		Profile   string `json:"profile"`
		NbFrames  string `json:"nb_frames"`
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
	for _, stream := range doc.Streams {
		if stream.CodecType == "audio" {
			result.AudioStreams++
			continue
		}
		if stream.CodecType != "video" || result.VideoCodec != "" {
			continue
		}
		result.VideoCodec = stream.CodecName
		result.CodecProfile = stream.Profile
		if n, err := strconv.Atoi(stream.NbFrames); err == nil {
			result.FrameCount = n
		}
		result.Width, result.Height = stream.Width, stream.Height
		result.PixelFormat = stream.PixFmt
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
	// decode), so the cadence check is cheap even on long clips.
	result.ClosedGOP = probeClosedGOP(ctx, path)
	return result, nil
}
