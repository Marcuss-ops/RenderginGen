package media

import (
	"context"
	"encoding/json"
	"fmt"
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
	Encoder            string
	FrameCount         int
	FirstFrameKeyframe bool
	AudioStreams       int
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
		Tags       struct {
			Encoder string `json:"encoder"`
		} `json:"tags"`
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
	result := ProbeResult{Container: doc.Format.FormatName, Encoder: doc.Format.Tags.Encoder}
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
			result.FPSNum, _ = strconv.Atoi(parts[0])
			result.FPSDen, _ = strconv.Atoi(parts[1])
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
		}
	}
	return result, nil
}

// ValidateOverlay enforces the overlay media contract. Overlays are video
// artifacts with no audio; every other invariant is checked against the
// requested canvas and a positive probed duration.
func (p ProbeResult) ValidateOverlay(width, height, fpsNum, fpsDen int) error {
	if p.AudioStreams != 0 {
		return fmt.Errorf("overlay media: audio_streams=%d, want 0", p.AudioStreams)
	}
	if p.DurationUS <= 0 || p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("overlay media: duration and dimensions must be positive")
	}
	if width > 0 && (p.Width != width || p.Height != height) {
		return fmt.Errorf("overlay media: resolution %dx%d, want %dx%d", p.Width, p.Height, width, height)
	}
	if fpsNum > 0 && fpsDen > 0 && p.FPSNum > 0 && p.FPSDen > 0 && p.FPSNum*fpsDen != fpsNum*p.FPSDen {
		return fmt.Errorf("overlay media: fps %d/%d, want %d/%d", p.FPSNum, p.FPSDen, fpsNum, fpsDen)
	}
	if p.VideoCodec == "" || p.Container == "" {
		return fmt.Errorf("overlay media: missing container or video codec")
	}
	return nil
}

// ValidateVisible checks the produced pixels across multiple timestamps.
// Codec/container validation alone is not sufficient: a renderer can emit a
// perfectly valid MP4 whose composited overlay is entirely black/transparent.
func (p ProbeResult) ValidateVisible(ctx context.Context, path string) error {
	if p.DurationUS <= 0 {
		return fmt.Errorf("visibility: non-positive duration")
	}
	// Sample multiple positions across the clip duration to catch animations
	// that fade in/out or move across the screen.
	sampleRatios := []float64{0.25, 0.50, 0.75}
	durationSec := float64(p.DurationUS) / 1_000_000.0

	var maxObservedYMAX float64
	var visibleSampleCount int

	for _, ratio := range sampleRatios {
		seekSec := durationSec * ratio
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss",
			fmt.Sprintf("%.3f", seekSec), "-i", path,
			"-frames:v", "1", "-vf",
			"format=gray,signalstats,metadata=print:key=lavfi.signalstats.YMAX:file=-,metadata=print:key=lavfi.signalstats.YAVG:file=-",
			"-f", "null", "-")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("visibility ffmpeg (seek=%.2fs): %w", seekSec, err)
		}
		var sampleYMAX float64
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "lavfi.signalstats.YMAX=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					val, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
					if val > sampleYMAX {
						sampleYMAX = val
					}
				}
			}
		}
		if sampleYMAX > maxObservedYMAX {
			maxObservedYMAX = sampleYMAX
		}
		if sampleYMAX > 16.0 {
			visibleSampleCount++
		}
	}

	if visibleSampleCount == 0 || maxObservedYMAX <= 16.0 {
		return fmt.Errorf("visibility: output video has no visible pixels (max YMAX=%.1f <= 16.0 across %d samples)",
			maxObservedYMAX, len(sampleRatios))
	}
	return nil
}
