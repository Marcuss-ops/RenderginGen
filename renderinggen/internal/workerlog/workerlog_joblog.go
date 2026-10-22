package workerlog

// workerlog_joblog.go owns the per-job log file lifecycle: the live file in
// the job workspace, its durable size-capped mirror under the jobs root, and
// their retention.
import (
	"fmt"
	"os"
	filepath "path/filepath"
	"sort"
	"strings"
	"time"
)

func AttachJobLog(jobID string, paths ...string) (*JobLog, error) {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil, fmt.Errorf("workerlog: job id is required to attach a job log")
	}
	st := stateFor(id)
	logged := &JobLog{id: id}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			logged.Close()
			return nil, fmt.Errorf("workerlog: create job log dir for %s: %w", path, err)
		}
		// Append on purpose: a re-attempt of the same job id (worker restart,
		// lease retry) adds to the log instead of erasing the previous
		// attempt's evidence. The cap keeps it bounded.
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			logged.Close()
			return nil, fmt.Errorf("workerlog: open job log %s: %w", path, err)
		}
		s := &sink{file: file, name: path}
		s.writer = &cappedWriter{w: file, max: jobLogMaxBytes}
		logged.sinks = append(logged.sinks, s)
	}
	st.mu.Lock()
	st.sinks = append(st.sinks, logged.sinks...)
	st.mu.Unlock()
	return logged, nil
}

// jobLogMaxBytes is the per-file cap. 8 MiB holds a verbose render's record
// (the engine stream dominates) while keeping a 100-job backlog on a tmpfs
// jobs root well inside RAM.
const jobLogMaxBytes = 8 << 20

// JobLog is the handle returned by AttachJobLog.
type JobLog struct {
	id    string
	sinks []*sink
}

// Close detaches the sinks and closes their files. It never fails a job: the
// first close error is returned for a caller that wants to log it.
func (l *JobLog) Close() error {
	if l == nil {
		return nil
	}
	if st, ok := jobStates.Load(l.id); ok {
		state := st.(*jobState)
		state.mu.Lock()
		kept := state.sinks[:0]
		for _, s := range state.sinks {
			if !l.owns(s) {
				kept = append(kept, s)
			}
		}
		state.sinks = kept
		state.mu.Unlock()
	}
	var firstErr error
	for _, s := range l.sinks {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			if err := s.file.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		s.mu.Unlock()
	}
	l.sinks = nil
	return firstErr
}

// owns reports whether this handle attached s.
func (l *JobLog) owns(s *sink) bool {
	for _, mine := range l.sinks {
		if mine == s {
			return true
		}
	}
	return false
}

// CloseJobLog closes EVERY file attached to jobID and forgets the job. It is
// the production entry point: the pools call it once at the end of a job, so no
// call site has to thread the AttachJobLog handle through the three pipeline
// stages (prep -> GPU -> post) just to close two files.
func CloseJobLog(jobID string) error {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil
	}
	var firstErr error
	if v, ok := jobStates.Load(id); ok {
		state := v.(*jobState)
		state.mu.Lock()
		sinks := append([]*sink(nil), state.sinks...)
		state.sinks = nil
		state.mu.Unlock()
		for _, s := range sinks {
			s.mu.Lock()
			if !s.closed {
				s.closed = true
				if err := s.file.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			s.mu.Unlock()
		}
	}
	jobStates.Delete(id)
	return firstErr
}

// PruneJobLogs removes per-job log files older than retention and, if more than
// maxFiles remain, the oldest ones on top of that. It runs at startup (see
// cmd/renderinggen) because the durable log directory lives under a jobs root
// that is frequently tmpfs.
func PruneJobLogs(dir string, retention time.Duration, maxFiles int) (int, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var kept []candidate
	removed := 0
	cutoff := time.Now().Add(-retention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if retention > 0 && info.ModTime().Before(cutoff) {
			if os.Remove(path) == nil {
				removed++
			}
			continue
		}
		kept = append(kept, candidate{path: path, modTime: info.ModTime()})
	}
	if maxFiles > 0 && len(kept) > maxFiles {
		sort.Slice(kept, func(i, j int) bool { return kept[i].modTime.Before(kept[j].modTime) })
		for _, c := range kept[:len(kept)-maxFiles] {
			if os.Remove(c.path) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// DurableJobLogDir is the canonical location of a job's durable log file: a
// dot-directory beside the workspace directories, so the workspace sweeper
// (which walks the jobs root) never mistakes it for a leak to reap.
func DurableJobLogDir(jobsRoot string) string {
	if strings.TrimSpace(jobsRoot) == "" {
		return ""
	}
	return filepath.Join(jobsRoot, ".workerlogs")
}

// DurableJobLogPath is the durable log path for one job.
func DurableJobLogPath(jobsRoot, jobID string) string {
	dir := DurableJobLogDir(jobsRoot)
	if dir == "" || strings.TrimSpace(jobID) == "" {
		return ""
	}
	return filepath.Join(dir, safeJobFileName(jobID)+".log")
}

// safeJobFileName makes a job id safe as a file name. Queue job ids are
// `<parent>:render:<n>`, which contains no path separator, but an id is
// producer-controlled and must never be able to escape the log directory — not
// even with a name that only looks like a traversal (".."). Everything outside
// the conservative set is replaced, including '.'.
func safeJobFileName(jobID string) string {
	replaced := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, jobID)
	trimmed := strings.Trim(replaced, "-_")
	if trimmed == "" {
		// An id made entirely of separators/lookalikes must still name a file.
		return "job"
	}
	return trimmed
}
