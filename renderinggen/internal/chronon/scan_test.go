package chronon

import (
	"strings"
	"testing"
	"time"
)

func TestScanRenderOutputForwardsLines(t *testing.T) {
	reader := strings.NewReader("[video] 1/10 frames\nframes_done=2\n")
	var got []string
	err := scanRenderOutput(reader, func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 2 || got[0] != "[video] 1/10 frames" || got[1] != "frames_done=2" {
		t.Fatalf("lines not forwarded intact: %#v", got)
	}
}

func TestScanRenderOutputLargeLineStillForwarded(t *testing.T) {
	// Just under the cap: a multi-hundred-KiB line must be forwarded intact
	// (the historical 64 KiB bufio.Scanner default would have silently
	// stopped the stream here and stalled the render).
	big := strings.Repeat("a", 300*1024)
	reader := strings.NewReader(big + "\n[video] 2/10 frames\n")
	var got []string
	err := scanRenderOutput(reader, func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 2 || got[0] != big || got[1] != "[video] 2/10 frames" {
		t.Fatalf("lines not forwarded intact: %d lines", len(got))
	}
}

func TestRenderOutputSamplerThrottlesWithinInterval(t *testing.T) {
	now := time.Unix(0, 0)
	s := newRenderOutputSampler(5 * time.Second)
	s.now = func() time.Time { return now }

	if !s.allowProgress("stdout") {
		t.Fatal("the first milestone of a stream must be logged")
	}
	now = now.Add(time.Second)
	if s.allowProgress("stdout") {
		t.Fatal("a milestone inside the throttle window must not be logged")
	}
	s.remember("stdout", "[video] 2/10 frames")
	now = now.Add(5 * time.Second)
	if !s.allowProgress("stdout") {
		t.Fatal("a milestone after the window must be logged")
	}
	// The window opened, so the buffered line is superseded by the logged one.
	if line, ok := s.flush("stdout"); ok {
		t.Fatalf("flush must not replay a milestone already logged, got %q", line)
	}
}

func TestRenderOutputSamplerFlushReturnsLastSuppressedMilestone(t *testing.T) {
	now := time.Unix(0, 0)
	s := newRenderOutputSampler(5 * time.Second)
	s.now = func() time.Time { return now }

	s.allowProgress("stderr")
	now = now.Add(time.Second)
	s.allowProgress("stderr")
	s.remember("stderr", "[video] 7/10 frames")
	now = now.Add(time.Second)
	s.allowProgress("stderr")
	s.remember("stderr", "[video] 9/10 frames")

	line, ok := s.flush("stderr")
	if !ok || line != "[video] 9/10 frames" {
		t.Fatalf("flush = (%q, %v), want the last suppressed milestone", line, ok)
	}
	if _, ok := s.flush("stderr"); ok {
		t.Fatal("flush must clear the buffered milestone")
	}
}

func TestRenderOutputSamplerStreamsAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	s := newRenderOutputSampler(5 * time.Second)
	s.now = func() time.Time { return now }

	if !s.allowProgress("stdout") {
		t.Fatal("stdout's first milestone must be logged")
	}
	if !s.allowProgress("stderr") {
		t.Fatal("stderr's first milestone is a different stream and must be logged")
	}
	now = now.Add(time.Second)
	if s.allowProgress("stdout") || s.allowProgress("stderr") {
		t.Fatal("both streams must be throttled inside their own window")
	}
}

func TestScanRenderOutputOversizedLineFailsLoudly(t *testing.T) {
	reader := strings.NewReader("ok line\n" + strings.Repeat("x", maxRenderOutputLine+1) + "\n")
	var got []string
	err := scanRenderOutput(reader, func(line string) { got = append(got, line) })
	if err == nil {
		t.Fatal("expected an error for an oversized output line (must not be silent)")
	}
	if len(got) != 1 || got[0] != "ok line" {
		t.Fatalf("normal lines before the oversized line must still be forwarded: %#v", got)
	}
}
