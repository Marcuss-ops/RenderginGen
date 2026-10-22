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
// //   3. A per-job log file that does not depend on journald: the live file lives
//
//	in the job's workspace (so `collect_chain_debug.sh` can read it while the
//	job runs) and is mirrored to a durable, size-capped copy under the jobs
//	root, because the workspace is removed at the end of the job and tmpfs
//	loses everything on reboot. The worker's own record is what makes the
//	collector independent of the host's journald retention.
//
// File layout: workerlog.go owns the handler setup (options, levels, Init),
// workerlog_bridge.go the standard-library bridge and line classification,
// workerlog_job.go the job-scoped state and Job handle, workerlog_sink.go the
// sinks and line formatting, workerlog_joblog.go the per-job log files and
// their retention.
package workerlog

import (
	"fmt"
	"io"
	"log"
	slog "log/slog"
	"os"
	"strings"
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

// ── standard library bridge ──────────────────────────────────────────────

// maxPendingLine bounds the bridge buffer. log's Writer contract has no newline
// when a caller passes a multi-line payload; the bridge must not grow without
// bound because of one misbehaving formatter.
