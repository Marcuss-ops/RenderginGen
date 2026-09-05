package chronon

import (
	"sync"
	"time"
)

// ProgressTracker accumulates the latest render progress for the job
// currently occupying the GPU lane and derives percent + fps from the
// observed frame positions. It is safe for concurrent use: the renderer's
// output-streaming goroutines call Observe while health and queue-pusher
// goroutines read Snapshot.
type ProgressTracker struct {
	mu   sync.Mutex
	jobs map[string]*progressState
}

type progressState struct {
	done, total int64
	firstDone   int64
	startAt     time.Time
	lastFrameAt time.Time
}

// NewProgressTracker creates a tracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{jobs: make(map[string]*progressState)}
}

// Observe records a frame-position observation for jobID. The first
// observation of a job becomes the FPS baseline, so a chunk that starts at
// absolute frame 240 measures fps from its own start.
func (t *ProgressTracker) Observe(jobID string, done, total int64) {
	if jobID == "" || total <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.jobs[jobID]
	if st == nil {
		st = &progressState{startAt: time.Now(), firstDone: done}
		t.jobs[jobID] = st
	}
	now := time.Now()
	st.done, st.total = done, total
	st.lastFrameAt = now
}

// Forget drops all state for jobID (called when a job leaves the GPU lane).
func (t *ProgressTracker) Forget(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.jobs, jobID)
}

// Snapshot returns the current progress for jobID, or nil when nothing has
// been observed (e.g. a renderer whose output format carried no frame lines).
func (t *ProgressTracker) Snapshot(jobID string) *Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.jobs[jobID]
	if st == nil {
		return nil
	}
	p := &Progress{
		JobID:       jobID,
		FramesDone:  st.done,
		FramesTotal: st.total,
		LastFrameAt: st.lastFrameAt,
		StartedAt:   st.startAt,
	}
	if st.total > 0 {
		p.Percent = 100 * float64(st.done) / float64(st.total)
	}
	// FPS over the frames rendered by this run (done - firstDone), which
	// keeps the value stable across chunked execution.
	done := st.done - st.firstDone
	if elapsed := time.Since(st.startAt); elapsed > 0 && done > 0 {
		p.FPS = float64(done) / elapsed.Seconds()
	}
	return p
}

// Current returns the snapshot of the most recently observed job, or nil
// when no render is in flight. Each GPU lane renders one job at a time, and
// Forget cleans up on completion, so the most recently observed job is the
// live render a caller (health, the queue progress pusher) should report.
func (t *ProgressTracker) Current() *Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	var best *progressState
	var bestID string
	for id, st := range t.jobs {
		if best == nil || st.lastFrameAt.After(best.lastFrameAt) {
			best, bestID = st, id
		}
	}
	if best == nil {
		return nil
	}
	p := &Progress{
		JobID:       bestID,
		FramesDone:  best.done,
		FramesTotal: best.total,
		LastFrameAt: best.lastFrameAt,
		StartedAt:   best.startAt,
	}
	if best.total > 0 {
		p.Percent = 100 * float64(best.done) / float64(best.total)
	}
	done := best.done - best.firstDone
	if elapsed := time.Since(best.startAt); elapsed > 0 && done > 0 {
		p.FPS = float64(done) / elapsed.Seconds()
	}
	return p
}

// Progress is an immutable progress snapshot for one job.
type Progress struct {
	JobID       string    `json:"job_id"`
	FramesDone  int64     `json:"frames_done"`
	FramesTotal int64     `json:"frames_total"`
	Percent     float64   `json:"progress"`
	FPS         float64   `json:"fps"`
	LastFrameAt time.Time `json:"last_frame_at"`
	StartedAt   time.Time `json:"started_at"`
}
