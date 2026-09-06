package chronon

import (
	"strings"
	"testing"
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
