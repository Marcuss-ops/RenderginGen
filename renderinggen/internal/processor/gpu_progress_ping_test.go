package processor

import (
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// TestObserveCounterlessProgressTouchesTheTracker pins the OVL-04 fix at the
// observer boundary: the IPC relay's counterless ping must publish the job to
// the tracker (so the queue pusher keeps last_frame_at fresh) without being
// recorded as a frame milestone.
func TestObserveCounterlessProgressTouchesTheTracker(t *testing.T) {
	tracker := chronon.NewProgressTracker()
	obs := newRenderProgressObserver(nil, tracker, "vulkan", "nvenc")
	obs.jobID = "job-ipc"
	obs.lease = 5

	obs.Observe(chronon.RenderProgress{})

	snap := tracker.Current()
	if snap == nil {
		t.Fatalf("counterless ping did not reach the tracker: the queue would keep the job silent")
	}
	if snap.JobID != "job-ipc" || snap.LeaseGeneration != 5 {
		t.Fatalf("tracked %q/%d, want job-ipc/5", snap.JobID, snap.LeaseGeneration)
	}
	if snap.FramesDone != 0 || snap.FramesTotal != 0 {
		t.Fatalf("ping invented frames: %d/%d", snap.FramesDone, snap.FramesTotal)
	}
	if _, saw := obs.Snapshot(); saw {
		t.Fatalf("a counterless ping must not be reported as a frame milestone")
	}

	obs.Observe(chronon.RenderProgress{FramesDone: 24, FramesTotal: 120})
	if _, saw := obs.Snapshot(); !saw {
		t.Fatalf("a real frame milestone must still be reported as one")
	}
	if got := tracker.Current(); got.FramesDone != 24 || got.FramesTotal != 120 {
		t.Fatalf("frame milestone not tracked: %d/%d", got.FramesDone, got.FramesTotal)
	}
}
