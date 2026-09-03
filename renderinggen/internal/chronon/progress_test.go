package chronon

import "testing"

func TestParseProgressLine(t *testing.T) {
	progress, ok := parseProgressLine("render frames_done=485 fps=18.4", 1800)
	if !ok {
		t.Fatal("expected progress line to parse")
	}
	if progress.FramesDone != 485 || progress.FramesTotal != 1800 || progress.FPS != 18.4 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

func TestParseProgressLineIgnoresUnrelatedOutput(t *testing.T) {
	if _, ok := parseProgressLine("allocated 1329 MiB VRAM", 1800); ok {
		t.Fatal("unrelated output must not be reported as frame progress")
	}
}
