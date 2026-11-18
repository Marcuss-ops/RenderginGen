// gpu_progress.go owns the render progress observer: one mutex, one log
// sampler and one stall heartbeat. The renderer invokes Progress from both its
// stdout and stderr streaming goroutines, so every field here is guarded.
package processor

import (
	"context"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// RenderProgressObserver serialises progress observations and keeps the
// heartbeat independent from the render call.
type RenderProgressObserver struct {
	mu        sync.Mutex
	last      chronon.RenderProgress
	saw       bool
	lastAt    time.Time
	lastLogAt time.Time
	startedAt time.Time
	jobID     string
	lease     int64
	backend   string
	encoder   string
	tracker   *chronon.ProgressTracker
	logf      func(format string, args ...any)
	statef    func(state, format string, args ...any)
	logEvery  time.Duration
	heartbeat time.Duration
}

func newRenderProgressObserver(job *queue.Job, tracker *chronon.ProgressTracker, backend, encoder string) *RenderProgressObserver {
	o := &RenderProgressObserver{
		startedAt: time.Now(),
		backend:   backend,
		encoder:   encoder,
		tracker:   tracker,
		logEvery:  progressLogInterval,
		heartbeat: renderHeartbeatInterval,
	}
	if job != nil {
		o.jobID = job.ID
		o.lease = job.LeaseGeneration
	}
	o.logf = func(format string, args ...any) {
		workerlog.ByJobID(o.jobID).Infof(format, args...)
	}
	o.statef = func(state, format string, args ...any) {
		workerlog.ByJobID(o.jobID).Statef(state, format, args...)
	}
	return o
}

// Observe records one milestone. I/O and tracker calls happen outside the
// mutex so a slow sink cannot block the renderer's stream goroutines.
func (o *RenderProgressObserver) Observe(progress chronon.RenderProgress) {
	now := time.Now()
	o.mu.Lock()
	o.saw = true
	o.last = progress
	o.lastAt = now
	final := progress.FramesTotal > 0 && progress.FramesDone >= progress.FramesTotal
	logNow := final || o.lastLogAt.IsZero() || now.Sub(o.lastLogAt) >= o.logEvery
	if logNow {
		o.lastLogAt = now
	}
	o.mu.Unlock()

	if logNow {
		o.logf("job_state=rendering progress=%s frames_done=%d frames_total=%d fps=%.2f last_frame_at=%s backend=%s encoder=%s",
			progressSummary(progress.FramesDone, progress.FramesTotal, progress.FPS),
			progress.FramesDone, progress.FramesTotal, progress.FPS,
			progress.At.Format(time.RFC3339Nano), o.backend, o.encoder)
	}
	if o.tracker != nil {
		o.tracker.Observe(o.jobID, o.lease, progress.FramesDone, progress.FramesTotal)
	}
}

// Snapshot returns the last observation and whether the renderer ever
// reported a frame milestone.
func (o *RenderProgressObserver) Snapshot() (chronon.RenderProgress, bool) {
	if o == nil {
		return chronon.RenderProgress{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last, o.saw
}

// Forget clears the tracker's live position for this job.
func (o *RenderProgressObserver) Forget() {
	if o == nil || o.tracker == nil {
		return
	}
	o.tracker.Forget(o.jobID)
}

// Heartbeat launches the stall heartbeat and returns a stop function that
// waits for the goroutine to exit. The caller invokes it once per observer.
func (o *RenderProgressObserver) Heartbeat(ctx context.Context) func() {
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(o.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case now := <-ticker.C:
				o.heartbeatOnce(now)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (o *RenderProgressObserver) heartbeatOnce(now time.Time) {
	o.mu.Lock()
	saw := o.saw
	last := o.last
	observedAt := o.lastAt
	o.mu.Unlock()
	if saw && !observedAt.IsZero() && now.Sub(observedAt) < o.heartbeat {
		return
	}
	elapsed := now.Sub(o.startedAt).Round(time.Second)
	if saw {
		o.statef("rendering", "state=in_progress elapsed=%s frames_done=%d frames_total=%d progress=%s note=no_new_frames backend=%s encoder=%s",
			elapsed, last.FramesDone, last.FramesTotal,
			progressSummary(last.FramesDone, last.FramesTotal, last.FPS), o.backend, o.encoder)
		return
	}
	o.statef("rendering", "state=in_progress elapsed=%s progress=unknown note=renderer_sent_no_frame_milestones backend=%s encoder=%s",
		elapsed, o.backend, o.encoder)
}
