package workerlog

// workerlog_sink.go owns the log sinks and line formatting: the fan-out sink,
// the size-capped writer and the single line renderer both handlers share.
import (
	"fmt"
	"io"
	slog "log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

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
