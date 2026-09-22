// Package workerlog is the GPU worker's logging surface.
//
// Why it exists: the worker emitted 110 plain `log.Printf` lines with no level,
// no fields and no job identity. Triaging one run on the worker host meant
// `grep job_1790…` against its journal and getting ZERO matches, because the only
// key it ever printed was the queue's internal job key
// (yt_…/en/overlay-v3/<hash>) — the parent_job_id the master stamps on every
// child was simply never logged. The half of the chain that spends the GPU time
// was therefore the half an operator could not correlate.
//
// Three things are fixed here, in the order of their value:
//
//  1. Job identity on every line: BindJob(job.ParentJobID, job.ID) at claim
//     time, then any package can emit with job_id + parent_job_id through
//     ForJob / ByJobID — including packages that only receive a job id string.
//
//  2. Structure: one slog handler (JSON by default, text on request) at the
//     level configured in renderinggen.yaml. The standard library logger is
//     bridged into the SAME handler, so the call sites that were not converted
//     still produce a structured record instead of a bare line — a partial
//     migration must not split the log stream into two formats.
//
//  3. A per-job log file that does not depend on journald: the live file lives
//     in the job's workspace (so `collect_chain_debug.sh` can read it while the
//     job runs) and is mirrored to a durable, size-capped copy under the jobs
//     root, because the workspace is removed at the end of the job and tmpfs
//     loses everything on reboot. The worker's own record is what makes the
//     collector independent of the host's journald retention.
package workerlog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Supported formats. JSON is the default because these lines are read by
// tooling (the collector, journalctl -o json, grep by field); text stays
// available for a human-on-a-terminal deployment.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// Options configures Init. Empty values select the shipped default
// (info/JSON/stderr).
type Options struct {
	Level  string
	Format string
	// Writer is where records are written. nil means os.Stderr, which is what
	// the systemd unit captures; tests (and a deployment that prefers stdout)
	// pass their own sink.
	Writer io.Writer
}

// Field names the worker's records carry. They are constants because the
// collector greps for them by name.
const (
	// FieldJobID is the queue job this line belongs to.
	FieldJobID = "job_id"
	// FieldParentJobID is the MASTER run id the queue stamped on the job. This
	// is the field that makes `grep job_1790…` work on the worker.
	FieldParentJobID = "parent_job_id"
	// FieldComponent names the emitting package for a record that has no job
	// scope (startup, heartbeat, sweeper).
	FieldComponent = "component"
	// FieldLogger marks a record that arrived through the bridged standard
	// library logger instead of an explicit slog call. It keeps the two
	// migrations distinguishable without changing either format.
	FieldLogger = "logger"
)

// levelVar is process-wide so a test (or a future /loglevel knob) can change
// the level without rebuilding the handler.
var levelVar = new(slog.LevelVar)

// ParseLevel maps the configured spelling to a slog level. It is strict: a
// typo must fail at config load (see config.LoggingConfig.validate), never
// silently degrade the worker to a different verbosity.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("workerlog: unknown level %q (want debug, info, warn or error)", s)
	}
}

// Init installs the process logger. It must be called once, before the worker
// pools start, and its error must be fatal: a worker that cannot install the
// configured log surface should not start claiming jobs.
func Init(opts Options) error {
	lvl, err := ParseLevel(opts.Level)
	if err != nil {
		return err
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	switch format {
	case "":
		format = FormatJSON
	case FormatJSON, FormatText:
	default:
		return fmt.Errorf("workerlog: unknown format %q (want json or text)", opts.Format)
	}
	levelVar.Set(lvl)

	out := opts.Writer
	if out == nil {
		out = os.Stderr
	}

	var handler slog.Handler
	handlerOpts := &slog.HandlerOptions{Level: levelVar}
	if format == FormatText {
		handlerOpts.ReplaceAttr = textAttr()
		handler = slog.NewTextHandler(out, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(out, handlerOpts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Bridge the standard library logger into the same handler. This is the
	// part that makes the migration safe: 110 un-converted call sites keep
	// working and their lines still land as structured records, so the stream
	// has ONE format at any moment during the migration.
	log.SetFlags(0)
	log.SetOutput(newBridge(logger))
	return nil
}

// textAttr keeps "time" in RFC3339Nano in text mode, like the JSON handler's
// default, so the two formats remain diffable against each other.
func textAttr() func([]string, slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey && len(groups) == 0 {
			return slog.String(slog.TimeKey, a.Value.Time().UTC().Format(time.RFC3339Nano))
		}
		return a
	}
}

// Level reports the active level.
func Level() slog.Level { return levelVar.Level() }

// ── standard library bridge ──────────────────────────────────────────────

// maxPendingLine bounds the bridge buffer. log's Writer contract has no newline
// when a caller passes a multi-line payload; the bridge must not grow without
// bound because of one misbehaving formatter.
const maxPendingLine = 1 << 16

// bridge adapts the standard library logger to slog. It splits the byte stream
// into lines (log.Printf is line-oriented, but a payload can contain newlines)
// and classifies each line's level from the marker the call sites already use
// ("WARN: …", "ERROR processor: …", "[processor WARN] …"), so the existing
// wording becomes a real level instead of being lost.
type bridge struct {
	mu     sync.Mutex
	buf    []byte
	logger *slog.Logger
}

func newBridge(logger *slog.Logger) *bridge { return &bridge{logger: logger} }

func (b *bridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	for {
		i := bytes.IndexByte(b.buf, '\n')
		if i < 0 {
			break
		}
		line := string(b.buf[:i])
		b.buf = b.buf[i+1:]
		b.emit(line)
	}
	if len(b.buf) >= maxPendingLine {
		b.emit(string(b.buf))
		b.buf = b.buf[:0]
	}
	return len(p), nil
}

// emit publishes one bridged line.
func (b *bridge) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	lvl, component, msg := classify(line)
	attrs := []slog.Attr{slog.String(FieldLogger, "stdlib")}
	if component != "" {
		attrs = append(attrs, slog.String(FieldComponent, component))
	}
	b.logger.LogAttrs(context.Background(), lvl, msg, attrs...)
}

// classify derives (level, component, message) from a bridged line.
//
// It recognizes exactly the markers this codebase already writes — it never
// guesses a level from the message text, because an inferred level that disagrees
// with the caller's intent is worse than a stable default:
//
//	"WARN: no GPU detected …"            → WARN,  msg "no GPU detected …"
//	"ERROR processor: workspace …"        → ERROR, component "processor"
//	"[processor WARN] policy=… "          → WARN,  component "processor"
//	"anything else"                       → INFO,  msg unchanged
func classify(line string) (slog.Level, string, string) {
	trimmed := strings.TrimSpace(line)
	upper := strings.ToUpper(trimmed)

	// Bracketed component tag, e.g. "[processor WARN] …" or "[processor] …".
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.IndexByte(trimmed, ']'); end > 1 {
			tag := trimmed[1:end]
			rest := strings.TrimSpace(trimmed[end+1:])
			fields := strings.Fields(tag)
			component := ""
			if len(fields) > 0 {
				component = strings.ToLower(fields[0])
			}
			level := slog.LevelInfo
			for _, f := range fields[1:] {
				switch strings.ToUpper(f) {
				case "WARN", "WARNING":
					level = slog.LevelWarn
				case "ERROR", "ERR":
					level = slog.LevelError
				}
			}
			if rest == "" {
				rest = trimmed
			}
			return level, component, rest
		}
	}

	for _, marker := range []struct {
		prefix string
		level  slog.Level
	}{
		{"ERROR", slog.LevelError},
		{"WARN", slog.LevelWarn},
	} {
		if !strings.HasPrefix(upper, marker.prefix) {
			continue
		}
		rest := trimmed[len(marker.prefix):]
		// Only a separator makes it a marker: "WARNINGSHOT" is not a level.
		rest = strings.TrimLeft(rest, ": ")
		if rest == "" || (!strings.HasPrefix(trimmed[len(marker.prefix):], ":") && !strings.HasPrefix(trimmed[len(marker.prefix):], " ")) {
			continue
		}
		// "ERROR processor: …" / "WARN processor: …": the word after the marker
		// names the emitting component, which is worth a field. The candidate is
		// accepted only when it looks like a package identifier AND is followed
		// by a colon — otherwise "ERROR buffer full: …" would silently drop
		// "buffer full" from the message.
		component := ""
		if i := strings.Index(rest, ":"); i > 0 && isComponentName(rest[:i]) {
			component = strings.ToLower(rest[:i])
			rest = strings.TrimLeft(rest[i:], ": ")
		}
		return marker.level, component, rest
	}
	return slog.LevelInfo, "", trimmed
}

// isComponentName reports whether s can name an emitting package: a single
// short token made of letters, digits, '_' or '-'. Anything with a space is
// prose and stays part of the message.
func isComponentName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 24 || strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// ── job scopes ───────────────────────────────────────────────────────────

// jobState is what the registry remembers about one job id: its parent (so a
// package that received only an id can still print the run it belongs to) and
// the sinks its lines are teed into.
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

// Writer returns an io.Writer that turns each written line into a job-scoped
// record. It exists for subprocess output (the engine's render stream), which
// arrives as bytes and must be attributed to the job that produced it.
func (j *Job) Writer() io.Writer { return &lineWriter{job: j} }

// lineWriter splits byte streams into records.
type lineWriter struct {
	job *Job
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if strings.TrimSpace(line) != "" {
			w.job.emit(slog.LevelInfo, strings.TrimSpace(line), slog.String(FieldLogger, "engine"))
		}
	}
	if len(w.buf) >= maxPendingLine {
		w.job.emit(slog.LevelInfo, strings.TrimSpace(string(w.buf)), slog.String(FieldLogger, "engine"))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// ── per-job sinks ────────────────────────────────────────────────────────

// sink is one per-job log file: a capped writer plus the text logger that
// formats records into it. The file is plain text (not JSON) on purpose —
// it is read by a human tailing a job, and by the collector's `find | tail`.
type sink struct {
	mu     sync.Mutex
	file   *os.File
	writer *cappedWriter
	name   string
	closed bool
}

func (s *sink) log(level slog.Level, msg string, attrs []slog.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return
	}
	if s.writer.exhausted() {
		// Keep the handle logic out of the hot path: a capped file stops
		// accepting records entirely, so no formatting cost is paid for lines
		// nobody will read.
		return
	}
	record := formatLine(level, msg, attrs)
	if _, err := s.writer.Write([]byte(record)); err != nil {
		// A per-job log file is diagnostic: a broken sink must never fail the
		// render or spam the process log with its own errors.
		return
	}
}

// cappedWriter stops writing after maxBytes and records the truncation once.
// Unbounded per-job files on a tmpfs jobs root would be an unbounded RAM
// consumer, and the codebase treats that as a defect (see workspace sweeper).
type cappedWriter struct {
	mu        sync.Mutex
	w         io.Writer
	max       int64
	written   int64
	truncated bool
}

func (c *cappedWriter) exhausted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.truncated {
		return len(p), nil
	}
	if c.max > 0 && c.written+int64(len(p)) > c.max {
		note := fmt.Sprintf("… %s: per-job log truncated at %d bytes; further records are dropped (raise logging.job_log_max_bytes to keep them)\n", time.Now().UTC().Format(time.RFC3339Nano), c.max)
		_, _ = c.w.Write([]byte(note))
		c.truncated = true
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	return n, err
}

// formatLine renders one text record for a per-job log file:
//
//	2026-09-22T16:41:08.123Z INFO  job_id=… parent_job_id=… msg
//
// Attributes are appended as key=value so the file stays greppable without a
// JSON reader.
func formatLine(level slog.Level, msg string, attrs []slog.Attr) string {
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	b.WriteByte(' ')
	b.WriteString(strings.ToUpper(level.String()))
	b.WriteByte(' ')
	for _, a := range attrs {
		value := a.Value.String()
		if value == "" {
			continue
		}
		b.WriteString(a.Key)
		b.WriteByte('=')
		if strings.ContainsAny(value, " \t") {
			b.WriteString(strconvQuote(value))
		} else {
			b.WriteString(value)
		}
		b.WriteByte(' ')
	}
	b.WriteString(msg)
	b.WriteByte('\n')
	return b.String()
}

func strconvQuote(s string) string { return fmt.Sprintf("%q", s) }

// AttachJobLog binds per-job log FILES to a job id. Every subsequent record for
// that job (from any package, via BindJob/ByJobID) is teed into them.
//
// The caller passes both destinations because they answer different questions:
// the workspace file is the live view the collector can read while the job
// runs, and the durable file under the jobs root survives the workspace's
// removal (the workspace is deleted at the end of every job).
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
