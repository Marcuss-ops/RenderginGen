// Package progresspush relays live render progress from the worker's
// ProgressTracker to the central queue. The queue is the source of truth for
// observability: parents and dashboards read GET /jobs/{id} without ever
// asking the worker, and each accepted report doubles as a render liveness
// signal on the queue side.
//
// Pushes are throttled (default 10s) and failure-tolerant: the queue being
// briefly unavailable must never fail a render that is otherwise healthy.
package progresspush

import (
	"context"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

// DefaultInterval is how often progress is pushed to the queue while a
// render reports frame milestones.
const DefaultInterval = 10 * time.Second

// Queue is the subset of the queue client the pusher needs. *queue.Client
// implements it via ReportProgress; tests supply their own recorder.
type Queue interface {
	ReportProgress(ctx context.Context, id string, framesDone, framesTotal int64) error
}

// Pusher relays tracker snapshots to the queue at a throttled interval.
type Pusher struct {
	queue    Queue
	tracker  *chronon.ProgressTracker
	interval time.Duration
}

// New creates a pusher. interval <= 0 selects DefaultInterval.
func New(q Queue, tracker *chronon.ProgressTracker, interval time.Duration) *Pusher {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Pusher{queue: q, tracker: tracker, interval: interval}
}

// Run blocks until ctx is cancelled, pushing progress on every tick while a
// render is being tracked. The tick cadence is the throttle: frame milestones
// may arrive many times per second, but the queue only hears about the job
// every interval. A tick whose snapshot is unchanged is still pushed: the
// queue-side last_frame_at then doubles as a render liveness signal (a job
// stuck at the same frame for minutes is distinguishable from a healthy one).
func (p *Pusher) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PushOnce(ctx)
		}
	}
}

// PushOnce performs at most one report: the tracker's most recent job. A nil
// snapshot (idle worker, or a renderer that printed no frame lines) is a
// no-op — the worker never invents progress. Errors are logged, never
// propagated: losing a progress report must not fail a healthy render.
func (p *Pusher) PushOnce(ctx context.Context) {
	snap := p.tracker.Current()
	if snap == nil || snap.JobID == "" {
		return
	}
	if err := p.queue.ReportProgress(ctx, snap.JobID, snap.FramesDone, snap.FramesTotal); err != nil {
		log.Printf("progress push: job %s: %v", snap.JobID, err)
	}
}
