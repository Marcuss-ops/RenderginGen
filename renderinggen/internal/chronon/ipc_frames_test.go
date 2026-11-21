package chronon

import "testing"

// TestParseIPCJobStatusExposesFrameProgress pins the optional progress fields
// the IPC-02 watchdog consumes. An older daemon omits them; the parser must
// still accept the lifecycle record and report zero frames (never a fake stall).
func TestParseIPCJobStatusExposesFrameProgress(t *testing.T) {
	snapshot, ok := parseIPCJobStatus(`{"job_id":"job-1","state":"running","message":"rendering","frames_done":42,"frames_total":100}`)
	if !ok {
		t.Fatal("a complete running record must parse")
	}
	if snapshot.state != "running" || snapshot.framesDone != 42 || snapshot.framesTotal != 100 {
		t.Fatalf("snapshot = %#v, want running 42/100", snapshot)
	}

	legacy, ok := parseIPCJobStatus(`{"job_id":"job-1","state":"running","message":"rendering"}`)
	if !ok {
		t.Fatal("a record without frame counters must remain readable")
	}
	if legacy.framesDone != 0 || legacy.framesTotal != 0 {
		t.Fatalf("absent counters = %d/%d, want 0/0", legacy.framesDone, legacy.framesTotal)
	}

	if _, ok := parseIPCJobStatus(`{"job_id":"job-1","message":"missing state"}`); ok {
		t.Fatal("a record without state is not a lifecycle observation")
	}
	if _, ok := parseIPCJobStatus(`not-json`); ok {
		t.Fatal("an unparsable body is not a lifecycle observation")
	}
}
