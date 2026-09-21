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
	"sync"
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

// renderOutputLogInterval bounds how often a repeated frame milestone from one
// Chronon output stream is copied to the worker log. The render itself still
// sees every line (progress parsing and the stall heartbeat are untouched); the
// bound exists because a 24-60fps render prints tens of thousands of milestones
// and every one of them would otherwise pay a formatted log write on the
// standard logger's mutex, which every GPU lane shares.
//
// It is deliberately a compile-time constant, not an environment knob. The only
// setting that matters is "log every line", which is precisely the behaviour
// this bound exists to remove, so there is nothing an operator should tune per
// host — and making it configurable would put that regression one typo away in
// production. The window stays a constructor parameter (newRenderOutputSampler
// takes it), so tests pin it exactly without sleeping.
const renderOutputLogInterval = 5 * time.Second

// renderOutputSampler throttles the raw renderer output written to the log and
// remembers the last suppressed milestone so the stream can still report where
// the render stopped. The clock is a field so tests can pin the window without
// sleeping.
type renderOutputSampler struct {
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	last    map[string]time.Time
	pending map[string]string
}

func newRenderOutputSampler(interval time.Duration) *renderOutputSampler {
	return &renderOutputSampler{
		interval: interval,
		now:      time.Now,
		last:     make(map[string]time.Time),
		pending:  make(map[string]string),
	}
}

// allowProgress reports whether a frame-milestone line is logged now. A
// suppressed line must be handed to remember so the stream can flush the final
// position when it ends.
func (s *renderOutputSampler) allowProgress(stream string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if last, ok := s.last[stream]; ok && now.Sub(last) < s.interval {
		return false
	}
	s.last[stream] = now
	delete(s.pending, stream)
	return true
}

// remember buffers the most recent throttled milestone of a stream.
func (s *renderOutputSampler) remember(stream, line string) {
	s.mu.Lock()
	s.pending[stream] = line
	s.mu.Unlock()
}

// flush returns the last throttled milestone of a stream and clears it, so the
// end of the stream reports the final frame position instead of dropping it
// with the throttle window.
func (s *renderOutputSampler) flush(stream string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line, ok := s.pending[stream]
	delete(s.pending, stream)
	return line, ok
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

// lineMentionsFrameProgress reports whether a line can carry a frame position,
// in ONE case-insensitive pass and with no allocation.
//
// It is the pre-filter of the hottest callback in a render: it runs for every
// line the engine prints (tens of thousands per render, on both the stdout and
// the stderr scanner), while the regexps below run only for lines that pass it.
// The previous guard was six strings.Contains calls — "frame"/"Frame"/"FRAME"
// and "video"/"Video"/"VIDEO" — i.e. up to six full scans of every line to
// decide whether to pay a seventh. Folding one byte with `| 0x20` tests both
// cases at once: it maps only 'F'/'f' onto 'f' and only 'V'/'v' onto 'v', so no
// other byte can start a candidate, and the comparison itself allocates nothing.
//
// The fold is strictly more permissive than the three spellings it replaces (it
// also admits "fRaMe"), which only means a line the case-insensitive regexp
// below may legitimately match is no longer dropped before it is asked.
func lineMentionsFrameProgress(line string) bool {
	const frame, video = "frame", "video"
	for i := 0; i < len(line); i++ {
		var keyword string
		switch line[i] | 0x20 {
		case 'f':
			keyword = frame
		case 'v':
			keyword = video
		default:
			continue
		}
		if len(line)-i >= len(keyword) && strings.EqualFold(line[i:i+len(keyword)], keyword) {
			return true
		}
	}
	return false
}

func parseProgressLine(line string, total int64) (RenderProgress, bool) {
	// Fast-path: skip the regexps on lines that cannot be progress.
	if !lineMentionsFrameProgress(line) {
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
