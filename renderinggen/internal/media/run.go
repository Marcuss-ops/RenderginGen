// run.go owns the buffered ffmpeg/ffprobe invocation shared by the decode,
// probe and visibility gates. gop_probe.go remains on its streaming path: it
// consumes packets as they arrive and cannot use a buffered runner.
package media

import (
	"bytes"
	"context"
	"os/exec"
)

// runTool runs name with args and returns stdout, stderr and the process error.
// Capturing both streams separately lets callers distinguish a diagnostic-only
// failure (exit 0 with stderr) from a clean success without a second process.
func runTool(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err = cmd.Run()
	return out.Bytes(), diagnostics.Bytes(), err
}
