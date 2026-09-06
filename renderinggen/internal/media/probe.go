package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// treat this as an error; it is never silently treated as \"fps matches\".
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
			// A parseable rate must be positive on both sides: \"0/0\" (or a
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

// probeClosedGOP inspects the video stream's sync-sample (keyframe) table and
// certifies a uniform closed-GOP structure. It fails closed: any probe or
// container error yields false, never a guess.
//
// The ffprobe packet table is stream-decoded with json.Decoder instead of
// cmd.Output(): -show_packets emits one JSON record per packet for the WHOLE
// clip, and buffering that document in RAM scaled with clip length (MBs of
// JSON for long-form content on every finalize). The decoder keeps memory
// constant while ffprobe streams, and a broken cadence aborts the probe (and
// the ffprobe process) as soon as it is provable.
func probeClosedGOP(ctx context.Context, path string) bool {
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_packets", "-show_entries", "packet=flags", "-of", "json", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("media: ffprobe %s: closed-GOP probe pipe: %v (closed_gop=false)", path, err)
		return false
	}
	if err := cmd.Start(); err != nil {
		log.Printf("media: ffprobe %s: closed-GOP probe start: %v (closed_gop=false)", path, err)
		return false
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	positions := make([]int, 0, 16)
	step := 0 // keyframe spacing once two keyframes are known (0 until proven)
	packetIndex := 0

	// Stream-decode the packet table with json.Decoder tokens (never
	// cmd.Output, which buffers the whole clip's packet JSON in RAM). The
	// invocation pins each element's shape to {"flags":"..."}, so the token
	// walk is deterministic: object open, key, value, object close.
	dec := json.NewDecoder(stdout)
	// Expected top level: { "packets": [ ...
	if err := expectJSONDelim(dec, '{'); err != nil {
		return failProbe(cancel, waitDone, path, err)
	}
	key, err := dec.Token()
	if err != nil {
		return failProbe(cancel, waitDone, path, err)
	}
	if k, ok := key.(string); !ok || k != "packets" {
		return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected top-level key %v", key))
	}
	if err := expectJSONDelim(dec, '['); err != nil {
		return failProbe(cancel, waitDone, path, err)
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				err = fmt.Errorf("unterminated packets array")
			}
			return failProbe(cancel, waitDone, path, err)
		}
		d, isDelim := tok.(json.Delim)
		if isDelim && d == ']' {
			break // end of the packet table
		}
		if !isDelim || d != '{' {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet-table token %v", tok))
		}
		// One packet object: { "flags" : "..." }
		keyTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		valueTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		closeTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		if k, ok := keyTok.(string); !ok || k != "flags" {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet key %v", keyTok))
		}
		flags, ok := valueTok.(string)
		if !ok {
			return failProbe(cancel, waitDone, path, fmt.Errorf("packet flags value is not a string: %v", valueTok))
		}
		if cd, ok := closeTok.(json.Delim); !ok || cd != '}' {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet close %v", closeTok))
		}
		if strings.Contains(flags, "K") {
			positions = append(positions, packetIndex)
			// Early refusal: three keyframes already prove the cadence broken,
			// so the probe (and the ffprobe process) can stop early.
			if len(positions) >= 3 {
				if positions[len(positions)-1]-positions[len(positions)-2] != step {
					cancel() // reap the child; we already have the verdict
					<-waitDone
					return false
				}
			} else if len(positions) == 2 {
				step = positions[1] - positions[0]
				if step <= 0 {
					cancel()
					<-waitDone
					return false
				}
			}
		}
		packetIndex++
	}
	if err := <-waitDone; err != nil {
		// A non-cancel wait error means ffprobe itself failed (bad file,
		// unsupported flag): fail closed and say so.
		log.Printf("media: ffprobe %s: closed-GOP probe failed: %v (closed_gop=false)", path, err)
		return false
	}
	return closedGOPPositionsCadence(positions)
}

// expectJSONDelim asserts the next decoder token is the given delimiter.
func expectJSONDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok && d == want {
		return nil
	}
	return fmt.Errorf("expected %q, got %v", want, tok)
}

// failProbe cancels the ffprobe child, drains its exit status and logs the
// failed closed-GOP probe so the degradation is never silent. It always
// reports false (fail closed).
func failProbe(cancel context.CancelFunc, waitDone <-chan error, path string, cause error) bool {
	cancel()
	<-waitDone
	log.Printf("media: ffprobe %s: closed-GOP probe decode failed: %v (closed_gop=false)", path, cause)
	return false
}

// closedGOPCadence reports whether keyframes occur at strictly uniform packet
// intervals starting at the first packet: positions 0, L, 2L, ... Uniform
// IDR boundaries are the observable signature of closed-GOP encoding (each
// GOP starts an independent IDR at a fixed cadence), while scene-cut or open
// GOP structures break the cadence. Fewer than two keyframes cannot prove a
// cadence, so the function returns false.
func closedGOPCadence(keyframes []bool) bool {
	positions := make([]int, 0, 8)
	for i, kf := range keyframes {
		if kf {
			positions = append(positions, i)
		}
	}
	return closedGOPPositionsCadence(positions)
}

// closedGOPPositionsCadence is the shared cadence decision over keyframe
// packet indices (0-based). Streaming probes collect positions directly and
// never materialize a per-packet flag array.
func closedGOPPositionsCadence(positions []int) bool {
	if len(positions) == 0 || positions[0] != 0 {
		return false
	}
	// Two keyframes (one interval) cannot prove a cadence — any single
	// interval is trivially \"uniform\". Require at least two full intervals.
	if len(positions) < 3 {
		return false
	}
	expected := positions[1] - positions[0]
	if expected <= 0 {
		return false
	}
	for i := 2; i < len(positions); i++ {
		if positions[i]-positions[i-1] != expected {
			return false
		}
	}
	return true
}

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
