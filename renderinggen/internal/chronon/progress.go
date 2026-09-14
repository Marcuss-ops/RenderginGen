package chronon

import (
	"sync"
	"sync/atomic"
	"time"
)

// ProgressTracker accumulates the latest render progress for the job
// currently occupying the GPU lane and derives percent + fps from the
// observed frame positions. It is safe for concurrent use: the renderer's
// output-streaming goroutines call Observe while health and queue-pusher
// goroutines read Snapshot.
//
// Concurrency shape. One tracker is shared by every GPU lane, so its lock
// discipline decides whether the observability of one lane contends with the
// data plane of another:
//
//   - state lives in one entry per in-flight job (sync.Map, per-key locking)
//     and each entry carries its own mutex. Two lanes rendering different jobs
//     never contend, and the per-frame writes of one lane never serialize
//     another lane's;
//   - Current()/Snapshot() read an entry without any tracker-wide lock, so the
//     readers (the /progress endpoint and the 10s queue pusher) never queue
//     behind the per-frame writer.
//
// The previous shape held ONE mutex for the whole map, so every observed frame
// of every lane bounced a single cache line between cores and the readers took
// the same lock as the render data plane.
type ProgressTracker struct {
	// entries maps jobID -> *progressEntry, one per in-flight render.
	entries sync.Map
	// latest is the most recently observed entry: the lock-free fast path of
	// Current(). A stale pointer (its job was forgotten) is detected and
	// resolved by a scan of the live entries.
	latest atomic.Pointer[progressEntry]
}

// progressEntry is the per-job state. jobID is immutable; everything else is
// guarded by mu.
type progressEntry struct {
	jobID string
	mu    sync.Mutex
	state progressState
}

type progressState struct {
	done, total int64
	firstDone   int64
	startAt     time.Time
	lastFrameAt time.Time
}

// NewProgressTracker creates a tracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{}
}

// entry returns the live entry for jobID, creating it on the first
// observation. LoadOrStore keeps concurrent first observations of the same job
// on one entry.
func (t *ProgressTracker) entry(jobID string) *progressEntry {
	if value, ok := t.entries.Load(jobID); ok {
		return value.(*progressEntry)
	}
	created := &progressEntry{jobID: jobID}
	actual, _ := t.entries.LoadOrStore(jobID, created)
	return actual.(*progressEntry)
}

// Observe records a frame-position observation for jobID. The first
// observation of a job becomes the FPS baseline, so a chunk that starts at
// absolute frame 240 measures fps from its own start.
func (t *ProgressTracker) Observe(jobID string, done, total int64) {
	if jobID == "" || total <= 0 {
		return
	}
	entry := t.entry(jobID)
	now := time.Now()
	entry.mu.Lock()
	if entry.state.startAt.IsZero() {
		entry.state.startAt = now
		entry.state.firstDone = done
	}
	entry.state.done, entry.state.total = done, total
	entry.state.lastFrameAt = now
	entry.mu.Unlock()
	t.latest.Store(entry)
}

// Forget drops all state for jobID (called when a job leaves the GPU lane).
// It also retires the job as the "current" one, so a finished lane stops
// answering health/pusher readers with a stale position.
func (t *ProgressTracker) Forget(jobID string) {
	value, ok := t.entries.LoadAndDelete(jobID)
	if !ok {
		return
	}
	t.latest.CompareAndSwap(value.(*progressEntry), nil)
}

// snapshotFor builds the immutable Progress for one tracker entry. Both
// Snapshot and Current derive their public snapshot through it, so the
// percent/fps rules exist once instead of as a hand-copied duplicate that
// would silently diverge the first time a field is added.
//
// The caller must hold the entry's mutex: the state fields are read here.
func snapshotFor(jobID string, st *progressState) *Progress {
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

// snapshot returns the immutable public snapshot of this entry.
func (e *progressEntry) snapshot() *Progress {
	e.mu.Lock()
	defer e.mu.Unlock()
	return snapshotFor(e.jobID, &e.state)
}

// lastObservedAt returns when this entry last observed a frame.
func (e *progressEntry) lastObservedAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.lastFrameAt
}

// Snapshot returns the current progress for jobID, or nil when nothing has
// been observed (e.g. a renderer whose output format carried no frame lines).
func (t *ProgressTracker) Snapshot(jobID string) *Progress {
	value, ok := t.entries.Load(jobID)
	if !ok {
		return nil
	}
	return value.(*progressEntry).snapshot()
}

// Current returns the snapshot of the most recently observed job, or nil
// when no render is in flight. Each GPU lane renders one job at a time, and
// Forget cleans up on completion, so the most recently observed job is the
// live render a caller (health, the queue progress pusher) should report. If
// that job has just been forgotten, the newest live entry wins instead of
// reporting nothing.
func (t *ProgressTracker) Current() *Progress {
	if entry := t.latest.Load(); entry != nil {
		if _, live := t.entries.Load(entry.jobID); live {
			return entry.snapshot()
		}
	}
	var best *progressEntry
	var bestAt time.Time
	t.entries.Range(func(_, value any) bool {
		entry := value.(*progressEntry)
		if at := entry.lastObservedAt(); best == nil || at.After(bestAt) {
			best, bestAt = entry, at
		}
		return true
	})
	if best == nil {
		return nil
	}
	return best.snapshot()
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
