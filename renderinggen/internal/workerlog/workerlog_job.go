package workerlog

// workerlog_job.go owns job-scoped logging: the job state map, the parent/child
// binding and the Job handle any package can emit through.
import (
	"context"
	"fmt"
	slog "log/slog"
	"strings"
	"sync"
)

type jobState struct {
	mu          sync.Mutex
	parentJobID string
	sinks       []*sink
}

var jobStates sync.Map // jobID -> *jobState

func stateFor(jobID string) *jobState {
	if v, ok := jobStates.Load(jobID); ok {
		return v.(*jobState)
	}
	created := &jobState{}
	actual, _ := jobStates.LoadOrStore(jobID, created)
	return actual.(*jobState)
}

func (s *jobState) setParent(parent string) {
	if parent == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The parent is immutable for a job id, but a first-writer race must not
	// leave a half-filled state: only fill an empty value.
	if s.parentJobID == "" {
		s.parentJobID = parent
	}
}

func (s *jobState) parent() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parentJobID
}

func (s *jobState) snapshotSinks() []*sink {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*sink, len(s.sinks))
	copy(out, s.sinks)
	return out
}

// Job is a job-scoped logger. The zero Job is valid and emits without job
// fields, so a call site that legitimately has no job (startup) can still use
// the same helpers.
type Job struct {
	id string
	st *jobState
}

// BindJob binds a claimed job to its MASTER run id and returns its logger.
// It is idempotent: the claim path may re-bind the same job after a restart,
// and the parent recorded first wins.
func BindJob(parentJobID, jobID string) *Job {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return &Job{}
	}
	st := stateFor(id)
	st.setParent(strings.TrimSpace(parentJobID))
	return &Job{id: id, st: st}
}

// ByJobID returns the logger for a job id that was bound earlier. It exists for
// the packages that only ever receive the id string (the processor's report and
// store helpers): they still get job_id + parent_job_id without the pipeline
// having to thread a logger through every signature.
func ByJobID(jobID string) *Job {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return &Job{}
	}
	st := stateFor(id)
	return &Job{id: id, st: st}
}

// ParentID reports the master run id recorded for this job ("" when unknown).
func (j *Job) ParentID() string { return j.st.parent() }

// Infof/Warnf/Errorf are the printf-shaped helpers used by the converted call
// sites, so a conversion stays a one-line change.
func (j *Job) Infof(format string, args ...any) { j.emit(slog.LevelInfo, fmt.Sprintf(format, args...)) }
func (j *Job) Warnf(format string, args ...any) { j.emit(slog.LevelWarn, fmt.Sprintf(format, args...)) }
func (j *Job) Errorf(format string, args ...any) {
	j.emit(slog.LevelError, fmt.Sprintf(format, args...))
}

// emit publishes one job-scoped record to the process handler and to every
// sink bound for this job.
func (j *Job) emit(level slog.Level, msg string, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+2)
	if j.id != "" {
		all = append(all, slog.String(FieldJobID, j.id))
	}
	if parent := j.st.parent(); parent != "" {
		all = append(all, slog.String(FieldParentJobID, parent))
	}
	all = append(all, attrs...)
	slog.Default().LogAttrs(context.Background(), level, msg, all...)
	for _, s := range j.st.snapshotSinks() {
		s.log(level, msg, all)
	}
}

// ── per-job sinks ────────────────────────────────────────────────────────

// sink is one per-job log file: a capped writer plus the text logger that
// formats records into it. The file is plain text (not JSON) on purpose —
// it is read by a human tailing a job, and by the collector's `find | tail`.
