// decode.go owns the full-decode gate: the artifact is decoded end to end by
// ffmpeg and any decode error fails the check.
//
// A structural probe (ProbeFile) reads container and stream metadata; it does
// not prove the frames can be decoded. A file whose bitstream is truncated, or
// whose frame count only exists in the container header, probes perfectly and
// fails in every downstream consumer. This gate is the cheap, tool-provided
// version of "does the whole clip actually play".
package media

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ValidateDecode decodes path with ffmpeg, discarding the output, and returns an
// error if any decode step fails.
//
// ffmpeg is invoked at "-v error" so its diagnostics are the error's body — the
// decode error names the stream, timestamp and reason, which is what makes the
// failure actionable in a batch log.
// CountDecodedFrames scans the complete video stream rather than trusting
// nb_frames in the container header. Decoder diagnostics are fatal even when
// ffprobe returns zero: a partial decode must never certify a segment window.
func CountDecodedFrames(ctx context.Context, path string) (int, error) {
	cmd := exec.CommandContext(ctx, ffprobeBinary(), "-v", "error", "-count_frames",
		"-select_streams", "v:0", "-show_entries", "stream=nb_read_frames",
		"-of", "default=nokey=1:noprint_wrappers=1", path)
	var diagnostics strings.Builder
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil || diagnostics.Len() > 0 {
		return 0, fmt.Errorf("media: count decoded frames %s: error=%v diagnostics=%s", path, err, strings.TrimSpace(diagnostics.String()))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("media: invalid decoded frame count %q for %s", strings.TrimSpace(string(out)), path)
	}
	return count, nil
}

func ValidateDecode(ctx context.Context, path string) error {
	out, err := exec.CommandContext(ctx, ffmpegBinary(), "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("media: decode %s: %w", path, err)
		}
		return fmt.Errorf("media: decode %s: %w: %s", path, err, detail)
	}
	return nil
}
