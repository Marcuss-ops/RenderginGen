package chronon

import (
	"testing"
	"time"
)

// TestTouchRefreshesLivenessWithoutInventingFrames pins the contract the
// producer's stall watchdog depends on: an observation with no frame counters
// still advances last_frame_at, and it never turns "unknown" into "0/N".
func TestTouchRefreshesLivenessWithoutInventingFrames(t *testing.T) {
	tracker := NewProgressTracker()

	tracker.Touch("job-liveness", 7)
	snap := tracker.Current()
	if snap == nil {
		t.Fatalf("Touch must publish a snapshot: the queue pusher has nothing to relay otherwise")
	}
	if snap.JobID != "job-liveness" || snap.LeaseGeneration != 7 {
		t.Fatalf("snapshot identity = %q/%d, want job-liveness/7", snap.JobID, snap.LeaseGeneration)
	}
	if snap.FramesDone != 0 || snap.FramesTotal != 0 {
		t.Fatalf("Touch invented frames: done=%d total=%d, want 0/0", snap.FramesDone, snap.FramesTotal)
	}
	if snap.LastFrameAt.IsZero() {
		t.Fatalf("Touch did not stamp last_frame_at")
	}

	first := snap.LastFrameAt
	time.Sleep(2 * time.Millisecond)
	tracker.Touch("job-liveness", 7)
	if now := tracker.Current().LastFrameAt; !now.After(first) {
		t.Fatalf("second Touch did not advance last_frame_at: %s -> %s", first, now)
	}
}

// TestTouchKeepsReportedFrames makes sure a liveness ping cannot reset a
// position the renderer already reported.
func TestTouchKeepsReportedFrames(t *testing.T) {
	tracker := NewProgressTracker()
	tracker.Observe("job-frames", 1, 40, 120)
	tracker.Touch("job-frames", 1)

	snap := tracker.Current()
	if snap == nil {
		t.Fatalf("no snapshot after Observe+Touch")
	}
	if snap.FramesDone != 40 || snap.FramesTotal != 120 {
		t.Fatalf("Touch changed reported frames: done=%d total=%d, want 40/120", snap.FramesDone, snap.FramesTotal)
	}
}

// TestRelayIPCProgressReportsSilenceAsZeroCounters pins the wire shape of the
// relay: counters the daemon never reported stay unknown, a reported counter is
// forwarded, and a missing sink is a no-op.
func TestRelayIPCProgressReportsZeroCountersWhenTheDaemonOmitsThem(t *testing.T) {
	at := time.Now()
	var got []RenderProgress
	relayIPCProgress(func(p RenderProgress) { got = append(got, p) }, []string{"a"}, map[string]int64{"a": 0}, map[string]int64{"a": 0}, at)
	if len(got) != 1 {
		t.Fatalf("relay calls = %d, want 1", len(got))
	}
	if got[0].FramesDone != 0 || got[0].FramesTotal != 0 || !got[0].At.Equal(at) {
		t.Fatalf("relay payload = %+v, want 0/0 stamped at %s", got[0], at)
	}

	got = nil
	relayIPCProgress(func(p RenderProgress) { got = append(got, p) }, []string{"a"}, map[string]int64{"a": 75}, map[string]int64{"a": 120}, at)
	if len(got) != 1 || got[0].FramesDone != 75 || got[0].FramesTotal != 120 {
		t.Fatalf("reported counters were not forwarded: %+v", got)
	}

	relayIPCProgress(nil, []string{"a"}, map[string]int64{"a": 1}, map[string]int64{"a": 2}, at)
}
