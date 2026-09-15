package chronon

import (
	"regexp"
	"strings"
	"testing"
)

// The client derives per-frame progress from the ENGINE'S HUMAN LOG, because
// that is the only per-frame signal the engine emits: --report writes a FINAL
// execution report (frames_written, timings) once the render is over, so it
// cannot feed a progress readout or a stall heartbeat during the render.
//
// That makes the log line a wire contract with no version on it, and the failure
// mode is silent: if the engine renames or reformats the milestone, progress
// stops advancing and the stall watchdog starts aborting healthy renders while
// every unit test that feeds the parser a hand-written fixture still passes.
// These tests close that gap from both ends —
//
//   - progressSourceFormatRE reads the emit site out of the engine's own source
//     (when the sibling checkout is present) and requires the parser to accept
//     the line the format string produces;
//   - TestProgressParserRejectsNonMilestoneEngineLines feeds the OTHER lines the
//     same source prints, the ones that merely mention frames, and requires them
//     to stay out of progress.
//
// The checkout-independent halves pin this package's own accepted and rejected
// forms, so a standalone RenderingGen checkout is still protected from an
// accidental widening of the regexp.

// progressLogSourceRelPath is the engine's render loop: the only place a
// per-frame milestone is printed for either execution path (pipe export and
// direct-YUV both funnel through this loop).
const progressLogSourceRelPath = "Chronon3d/apps/chronon3d_cli/commands/video/common/pipe_export_render_loop.cpp"

// progressLogPathEnv overrides the discovery of that file.
const progressLogPathEnv = "CHRONON_PROGRESS_LOG_SOURCE"

// progressMilestoneFormatRE extracts the format string of the milestone
// spdlog::info call, i.e. the literal between the quotes.
var progressMilestoneFormatRE = regexp.MustCompile(`spdlog::info\(\s*"([^"]*)"`)

// renderCppFormat substitutes positional {} arguments into an spdlog format
// string, which is how the engine builds the line the parser sees.
func renderCppFormat(format string, args ...string) string {
	for _, arg := range args {
		format = strings.Replace(format, "{}", arg, 1)
	}
	return format
}

// TestProgressMilestoneFormatMatchesEngineSource reads the engine's milestone
// format string and requires the parser to accept a line rendered from it.
//
// This is the anti-drift half: it fails when the engine changes the format (the
// extracted string changes) or when this parser stops accepting what the engine
// prints. The two must agree on the exact line, not on a hand-written fixture.
func TestProgressMilestoneFormatMatchesEngineSource(t *testing.T) {
	source := chrononSourceFile(t, progressLogSourceRelPath, progressLogPathEnv)

	matches := progressMilestoneFormatRE.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		t.Fatalf("no spdlog::info format string found in %s; the milestone emit site moved", progressLogSourceRelPath)
	}
	// The milestone is the format carrying two positional frames fields.
	var format string
	for _, match := range matches {
		if strings.Count(match[1], "{}") == 2 && strings.Contains(match[1], "frames") {
			format = match[1]
			break
		}
	}
	if format == "" {
		t.Fatalf("no two-field frames milestone format in %s; found %v", progressLogSourceRelPath, matches)
	}

	line := renderCppFormat(format, "485", "1800")
	progress, ok := parseProgressLine(line, 0)
	if !ok {
		t.Fatalf("the engine's own milestone %q does not parse; progress and the stall watchdog would go silent", line)
	}
	if progress.FramesDone != 485 || progress.FramesTotal != 1800 {
		t.Fatalf("engine milestone %q parsed as %+v, want 485/1800", line, progress)
	}
	// The milestone format must not be a private spelling of the line as well:
	// it has to start with the [video] tag the parser keys on, or a second
	// emitter could print the same numbers without being recognized.
	if !strings.Contains(format, "[video]") {
		t.Fatalf("milestone format %q no longer carries the [video] tag the parser keys on", format)
	}
}

// TestProgressParserAcceptsEngineMilestoneForms pins the accepted forms as
// literals, including the exact spacing the engine prints, so the standalone
// checkout is protected too.
func TestProgressParserAcceptsEngineMilestoneForms(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		callerTo   int64
		wantDone   int64
		wantTotal  int64
		wantParsed bool
	}{
		{"engine milestone, exact spacing", "[video]   485/1800 frames", 0, 485, 1800, true},
		{"line total overrides the caller's", "[video]   485/1800 frames", 900, 485, 1800, true},
		{"collapsed spacing still parses", "[video] 485/1800 frames", 0, 485, 1800, true},
		{"frame-tagged key/value form", "render frames_done=485 fps=18.4", 1800, 485, 1800, true},
		{"frames_rendered key/value form", "frames_rendered=485", 1800, 485, 1800, true},
		{"bare frame key/value form", "frame=485", 1800, 485, 1800, true},
		{"first frame", "[video]   1/240 frames", 0, 1, 240, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			progress, ok := parseProgressLine(tc.line, tc.callerTo)
			if ok != tc.wantParsed {
				t.Fatalf("parseProgressLine(%q) parsed = %v, want %v", tc.line, ok, tc.wantParsed)
			}
			if !ok {
				return
			}
			if progress.FramesDone != tc.wantDone || progress.FramesTotal != tc.wantTotal {
				t.Fatalf("parseProgressLine(%q) = %d/%d, want %d/%d",
					tc.line, progress.FramesDone, progress.FramesTotal, tc.wantDone, tc.wantTotal)
			}
		})
	}
}

// TestProgressParserRejectsNonMilestoneEngineLines pins the false positives: the
// other lines the engine prints that mention frames or a frame count.
//
// Every one of these is verbatim from the engine source. A parser that accepted
// them would report progress that did not happen (an adapter's closed-sink total
// is not this render's position) or, worse, an allocation/dry-run line as a
// milestone — which is how the stall heartbeat gets fooled into thinking a
// wedged render is advancing.
func TestProgressParserRejectsNonMilestoneEngineLines(t *testing.T) {
	lines := []string{
		// Allocator noise: the original reason the parser has a fast path.
		"allocated 1329 MiB VRAM",
		// Sink/adapter lifecycle summaries: a total for a sink, not a position.
		"[video_adapter] Closed sink — 485 frames written, write_blocked=1.20ms, close=2.30ms",
		"[native_av] Closed native encoder — 485 frames written, YUV cache: 12 hits / 3 misses",
		// Dry-run: a declared range, not progress.
		"[dry-run]   Frame range: 0 – 239 inclusive (240 frames)",
		// The DirectYUV completion summary: final-only, and the caller's totals
		// stay authoritative. It must not be mistaken for a milestone mid-render.
		"[direct-yuv] loop finished: frames=485 total_render=12.34ms template_evals=9 template_reuses=7",
		// Failure and warning lines that quote a frame number.
		"[video] FFmpeg writer failed before frame 485",
		"[video] Failed to render frame 485",
		"[video] Render cancelled at frame 485",
		// A job descriptor line.
		"Rendering 240 [240 selected frames]...",
	}
	for _, line := range lines {
		if progress, ok := parseProgressLine(line, 1800); ok {
			t.Errorf("line %q was read as progress %+v; it is not a per-frame milestone", line, progress)
		}
	}
}

// TestProgressParserKeepsCallerTotalOnKeyValueForms pins that a form without a
// denominator reports the caller's total: the key/value lines carry no total of
// their own, and inventing one would corrupt the percent a producer reports.
func TestProgressParserKeepsCallerTotalOnKeyValueForms(t *testing.T) {
	progress, ok := parseProgressLine("render frames_done=485 fps=18.4", 1800)
	if !ok {
		t.Fatal("expected the key/value form to parse")
	}
	if progress.FramesTotal != 1800 {
		t.Fatalf("FramesTotal = %d, want the caller's 1800", progress.FramesTotal)
	}
	if progress.FPS != 18.4 {
		t.Fatalf("FPS = %v, want 18.4", progress.FPS)
	}
	// A zero caller total stays zero rather than becoming a fabricated one.
	progress, ok = parseProgressLine("frames_done=10", 0)
	if !ok {
		t.Fatal("expected the key/value form to parse")
	}
	if progress.FramesTotal != 0 {
		t.Fatalf("FramesTotal = %d, want 0 (no total was supplied)", progress.FramesTotal)
	}
}

// TestProgressParserIgnoresAllocationAndSupportsCaseInsensitivity pins the two
// properties the fast path depends on: the pre-filter must not drop a real
// milestone, and the parser must not be case-sensitive about the tag.
func TestProgressParserIgnoresAllocationAndSupportsCaseInsensitivity(t *testing.T) {
	for _, line := range []string{"[VIDEO]   7/240 frames", "[Video]   7/240 frames"} {
		progress, ok := parseProgressLine(line, 0)
		if !ok {
			t.Fatalf("parseProgressLine(%q) did not parse; the tag match must be case-insensitive", line)
		}
		if progress.FramesDone != 7 || progress.FramesTotal != 240 {
			t.Fatalf("parseProgressLine(%q) = %d/%d, want 7/240", line, progress.FramesDone, progress.FramesTotal)
		}
	}
}
