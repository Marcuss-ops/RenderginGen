package workerlog

import (
	"strings"
	"testing"
)

// TestStatefEmitsAGrepableTransition pins the marker an operator greps to read
// one job's whole timeline: job_state=<state> plus the detail, attributed to the
// job it belongs to.
func TestStatefEmitsAGrepableTransition(t *testing.T) {
	buf := initToBuffer(t, "info", "text")
	ByJobID("job-1").Statef("rendering", "progress=%s", "42.0%")
	out := buf.String()
	if !strings.Contains(out, "job_state=rendering") {
		t.Fatalf("record = %q, missing job_state marker", out)
	}
	if !strings.Contains(out, "progress=42.0%") {
		t.Fatalf("record = %q, missing transition detail", out)
	}
	if !strings.Contains(out, "job_id=job-1") {
		t.Fatalf("record = %q, missing job attribution", out)
	}
}

func TestStatefWithoutDetailStillEmitsTheMarker(t *testing.T) {
	buf := initToBuffer(t, "info", "text")
	ByJobID("job-2").Statef("completed", "")
	if out := buf.String(); !strings.Contains(out, "job_state=completed") || strings.HasSuffix(strings.TrimSpace(out), "completed ") {
		t.Fatalf("record = %q, want a clean marker with no trailing detail", out)
	}
}
