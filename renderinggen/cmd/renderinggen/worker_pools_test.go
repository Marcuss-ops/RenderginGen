package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordedLivenessReport struct {
	id          string
	generation  int64
	framesDone  int64
	framesTotal int64
}

type recordingLivenessQueue struct {
	mu      sync.Mutex
	reports []recordedLivenessReport
}

func (q *recordingLivenessQueue) ReportProgress(_ context.Context, id string, generation, framesDone, framesTotal int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reports = append(q.reports, recordedLivenessReport{id, generation, framesDone, framesTotal})
	return nil
}

func (q *recordingLivenessQueue) snapshot() []recordedLivenessReport {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]recordedLivenessReport(nil), q.reports...)
}

func waitForReports(t *testing.T, q *recordingLivenessQueue, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if len(q.snapshot()) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat produced %d report(s), want at least %d", len(q.snapshot()), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// A renderer that reports frames must keep reporting its real position: the
// liveness tick exists to refresh the timestamp, not to reset progress to zero.
func TestRenderLivenessHeartbeatReportsTrackedFrames(t *testing.T) {
	q := &recordingLivenessQueue{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runRenderLivenessHeartbeat(ctx, q, "job-1", 7, 5*time.Millisecond, func() (int64, int64) {
			return 42, 100
		})
		close(done)
	}()

	waitForReports(t, q, 2)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after cancellation")
	}

	for _, report := range q.snapshot() {
		if report.id != "job-1" || report.generation != 7 || report.framesDone != 42 || report.framesTotal != 100 {
			t.Fatalf("report = %+v, want job-1 generation 7 frames 42/100", report)
		}
	}
}

// A renderer without frame milestones (native Vulkan) has no tracker snapshot;
// the heartbeat still has to publish a liveness timestamp with frames 0/0.
func TestRenderLivenessHeartbeatWithoutSnapshotReportsZero(t *testing.T) {
	q := &recordingLivenessQueue{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runRenderLivenessHeartbeat(ctx, q, "job-2", 1, 5*time.Millisecond, nil)
		close(done)
	}()

	waitForReports(t, q, 1)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after cancellation")
	}

	report := q.snapshot()[0]
	if report.id != "job-2" || report.generation != 1 || report.framesDone != 0 || report.framesTotal != 0 {
		t.Fatalf("report = %+v, want job-2 generation 1 frames 0/0", report)
	}
}
