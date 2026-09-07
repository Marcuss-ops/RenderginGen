// validate.go owns the post-probe certification gates: full decode, the
// overlay media contract and sampled pixel visibility.
package media

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ValidateDecoded performs a complete decode pass. ffprobe validates container
// metadata, but it can still accept an MP4 whose H.264 packets are truncated
// or malformed. A render is not publishable until FFmpeg consumes every frame.
func ValidateDecoded(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-f", "null", "-")
	if out, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			return fmt.Errorf("full decode %s: %w", path, err)
		}
		return fmt.Errorf("full decode %s: %w: %s", path, err, message)
	}
	return nil
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
	if fpsNum > 0 && fpsDen > 0 {
		// A declared fps contract must be certified by a parseable probed
		// rate. A failed/absent rate parse (0/0, garbage) is a contract
		// ERROR, never an exemption: the previous conditional gate silently
		// skipped the fps check exactly when the probe could not certify it.
		if p.FPSUncertifiable || p.FPSNum <= 0 || p.FPSDen <= 0 {
			return fmt.Errorf("overlay media: fps contract uncertifiable (probed rate %d/%d)", p.FPSNum, p.FPSDen)
		}
		if p.FPSNum*fpsDen != fpsNum*p.FPSDen {
			return fmt.Errorf("overlay media: fps %d/%d, want %d/%d", p.FPSNum, p.FPSDen, fpsNum, fpsDen)
		}
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
