package progresspush

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

// recordingQueue captures ReportProgress calls for assertions.
type recordingQueue struct {
	mu    sync.Mutex
	calls []report
	failN int // fail the first N calls
}

type report struct {
	id          string
	framesDone  int64
	framesTotal int64
}

func (r *recordingQueue) ReportProgress(_ context.Context, id string, done, total int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failN > 0 {
		r.failN--
		return context.DeadlineExceeded
	}
	r.calls = append(r.calls, report{id: id, framesDone: done, framesTotal: total})
	return nil
}

func (r *recordingQueue) all() []report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]report(nil), r.calls...)
}

// TestPushOnceReportsTrackedJob verifies a single push relays the tracker's
// most recent snapshot to the queue.
func TestPushOnceReportsTrackedJob(t *testing.T) {
	tr := chronon.NewProgressTracker()
	tr.Observe("job-1", 485, 1800)
	q := &recordingQueue{}
	p := New(q, tr, time.Second)

	p.PushOnce(context.Background())

	calls := q.all()
	if len(calls) != 1 {
		t.Fatalf("want 1 report, got %d", len(calls))
	}
	if calls[0].id != "job-1" || calls[0].framesDone != 485 || calls[0].framesTotal != 1800 {
		t.Fatalf("report = %+v, want job-1 485/1800", calls[0])
	}
}

// TestPushOnceIdleIsNoOp ensures an idle worker (no tracked render) never
// invents progress reports.
func TestPushOnceIdleIsNoOp(t *testing.T) {
	tr := chronon.NewProgressTracker()
	q := &recordingQueue{}
	p := New(q, tr, time.Second)

	p.PushOnce(context.Background())

	if calls := q.all(); len(calls) != 0 {
		t.Fatalf("idle worker must not report, got %+v", calls)
	}
}

// TestPushOnceFailureTolerant verifies a queue error is swallowed: losing a
// progress report must never fail a healthy render.
func TestPushOnceFailureTolerant(t *testing.T) {
	tr := chronon.NewProgressTracker()
	tr.Observe("job-1", 10, 100)
	q := &recordingQueue{failN: 1}
	p := New(q, tr, time.Second)

	p.PushOnce(context.Background()) // fails, must not panic/propagate
	tr.Observe("job-1", 20, 100)
	p.PushOnce(context.Background()) // succeeds

	calls := q.all()
	if len(calls) != 1 || calls[0].framesDone != 20 {
		t.Fatalf("want exactly the successful report, got %+v", calls)
	}
}

// TestRunStopsOnCancel verifies Run returns promptly on context cancellation.
func TestRunStopsOnCancel(t *testing.T) {
	tr := chronon.NewProgressTracker()
	q := &recordingQueue{}
	p := New(q, tr, time.Hour) // would never tick within the test

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestRunThrottlesToInterval verifies the Run loop pushes at the configured
// cadence, not once per frame milestone.
func TestRunThrottlesToInterval(t *testing.T) {
	tr := chronon.NewProgressTracker()
	q := &recordingQueue{}
	p := New(q, tr, 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Many milestones, one push per tick.
	for i := 0; i < 5; i++ {
		tr.Observe("job-throttle", int64(100*(i+1)), 1000)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond) // allow a couple of ticks

	if calls := q.all(); len(calls) == 0 {
		t.Fatal("want at least one throttled report")
	}
}
