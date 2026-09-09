// output_scan.go owns the CLI output streaming primitives: bounded line
// scanning and the frame-progress parsing that feeds the stall watchdog and
// RenderRequest.Progress.
package chronon

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// scanRenderOutput forwards each complete output line to onLine and returns
// the terminal scanner error (nil on EOF). The scanner buffer starts at 64
// KiB and grows up to maxRenderOutputLine, so ordinary multi-KiB log lines are
// forwarded intact instead of silently stopping the stream at the historical
// 64 KiB default.
func scanRenderOutput(r io.Reader, onLine func(string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxRenderOutputLine)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
	return scanner.Err()
}

// progressFrameRE matches the frame-position progress lines Chronon emits on
// stdout/stderr for both execution paths (pipe export and direct-YUV):
//
//	[video]   485/1800 frames
//	frames_done=485
//	frames rendered: 485
//
// It also captures an optional fps=<n> field on the same line. Memory-alloc
// lines ("allocated 1329 MiB VRAM") never match: GPU allocation is not
// progress evidence.
var progressFrameRE = regexp.MustCompile(`(?i)(?:\[\s*video\s*]\s*)?(\d+)\s*/\s*(\d+)\s+frames|\b(?:frames?_rendered|frames?_done|frame)\s*[:=]\s*(\d+)`)
var progressFPSRE = regexp.MustCompile(`(?i)\bfps\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)`)

func parseProgressLine(line string, total int64) (RenderProgress, bool) {
	// Fast-path: skip regex on lines that cannot be progress.
	if !strings.Contains(line, "frame") && !strings.Contains(line, "Frame") && !strings.Contains(line, "FRAME") && !strings.Contains(line, "video") && !strings.Contains(line, "Video") && !strings.Contains(line, "VIDEO") {
		return RenderProgress{}, false
	}
	match := progressFrameRE.FindStringSubmatch(line)
	if len(match) != 4 {
		return RenderProgress{}, false
	}
	progress := RenderProgress{FramesTotal: total, At: time.Now().UTC()}
	if match[1] != "" {
		// "[video] N/M frames" form: N is absolute, M is the authoritative total.
		done, err1 := strconv.ParseInt(match[1], 10, 64)
		tot, err2 := strconv.ParseInt(match[2], 10, 64)
		if err1 != nil || err2 != nil {
			return RenderProgress{}, false
		}
		progress.FramesDone, progress.FramesTotal = done, tot
	} else {
		frames, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			return RenderProgress{}, false
		}
		progress.FramesDone = frames
	}
	if fps := progressFPSRE.FindStringSubmatch(line); len(fps) == 2 {
		progress.FPS, _ = strconv.ParseFloat(fps[1], 64)
	}
	return progress, true
}
