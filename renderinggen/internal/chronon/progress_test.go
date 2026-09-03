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

// TestParseProgressLineVideoFormat covers the actual render-loop output both
// execution paths emit: "[video]   485/1800 frames". The M/total on the line
// must override any caller-provided total.
func TestParseProgressLineVideoFormat(t *testing.T) {
	progress, ok := parseProgressLine("[video]   485/1800 frames", 0)
	if !ok {
		t.Fatal("expected [video] N/M frames line to parse")
	}
	if progress.FramesDone != 485 || progress.FramesTotal != 1800 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

// TestTrackerFPSAndPercent covers the derived observability fields: percent
// of total and fps measured over frames rendered since the job's first
// observation (baseline offset for chunked execution).
func TestTrackerFPSAndPercent(t *testing.T) {
	tr := NewProgressTracker()
	tr.Observe("job-1", 240, 1800) // chunk starts at absolute frame 240
	tr.Observe("job-1", 740, 1800) // 500 frames later

	p := tr.Snapshot("job-1")
	if p == nil {
		t.Fatal("want snapshot, got nil")
	}
	if p.FramesDone != 740 || p.FramesTotal != 1800 {
		t.Fatalf("frames = %d/%d, want 740/1800", p.FramesDone, p.FramesTotal)
	}
	if want := float64(100 * 740 / 1800); p.Percent < want-1 || p.Percent > want+1 {
		t.Fatalf("percent = %.1f, want ~%.0f", p.Percent, want)
	}
	if p.FPS <= 0 {
		t.Fatalf("fps = %v, want > 0", p.FPS)
	}
}

func TestTrackerUnknownJob(t *testing.T) {
	tr := NewProgressTracker()
	if p := tr.Snapshot("missing"); p != nil {
		t.Fatalf("want nil snapshot for unobserved job, got %+v", p)
	}
}

func TestTrackerForget(t *testing.T) {
	tr := NewProgressTracker()
	tr.Observe("job-1", 10, 100)
	tr.Forget("job-1")
	if p := tr.Snapshot("job-1"); p != nil {
		t.Fatalf("want nil snapshot after Forget, got %+v", p)
	}
}
