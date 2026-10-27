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

// ValidateDecodeSampled is the budget-conscious sibling of ValidateDecode,
// behind the sampled verification tier: instead of reconstructing every frame
// it proves the artifact is real, well-formed media at three deterministic
// points. For a freshly muxed output whose container facts, identity and
// encoder/muxer success are already verified elsewhere, this is the coverage
// that catches the realistic corruption modes (a truncated write, a damaged
// packet run, a header that lies) at a fraction of the decode cost.
//
// The three passes, each a separate ffmpeg run:
//
//  1. whole-bitstream traversal with -skip_frame nokey: every packet is
//     parsed and every I-frame decoded, so truncation or damage ANYWHERE in
//     the file surfaces without paying for P/B reconstruction;
//  2. head: a short complete decode from the start, exercising the reference
//     chain a real player would;
//  3. tail: a complete decode of the final window (-sseof) to EOF — the place
//     where an incomplete write hides.
//
// -xerror turns any decode error into a non-zero exit, so the verdict is the
// exit code and nothing weaker. A failure here is NOT a rejection verdict: the
// post-lane gate escalates to ValidateDecode (a complete decode) before
// rejecting, which is what makes it safe for this gate to be strict.
func ValidateDecodeSampled(ctx context.Context, path string) error {
	passes := [][]string{
		{"-skip_frame", "nokey", "-i", path, "-f", "null", "-"},
		{"-i", path, "-t", "0.5", "-f", "null", "-"},
		{"-sseof", "-0.5", "-i", path, "-f", "null", "-"},
	}
	for _, pass := range passes {
		args := append([]string{"-v", "error", "-xerror"}, pass...)
		out, err := exec.CommandContext(ctx, ffmpegBinary(), args...).CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				return fmt.Errorf("media: sampled decode %s (%s): %w", path, strings.Join(pass, " "), err)
			}
			return fmt.Errorf("media: sampled decode %s (%s): %w: %s", path, strings.Join(pass, " "), err, detail)
		}
	}
	return nil
}
