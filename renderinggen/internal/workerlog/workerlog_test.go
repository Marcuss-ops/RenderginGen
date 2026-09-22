package workerlog

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initToBuffer installs the logger over an in-memory sink and returns it. Tests
// never share the process handler state with each other, so each one re-Init-s
// over its own buffer.
func initToBuffer(t *testing.T, level, format string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := Init(Options{Level: level, Format: format, Writer: &buf}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return &buf
}

// TestParseLevelIsStrict pins the vocabulary: config validation and the logger
// must agree on what a level is, and a typo must be an error rather than a
// silent downgrade to info.
func TestParseLevelIsStrict(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  slog.Level
		fails bool
	}{
		{in: "", want: slog.LevelInfo},
		{in: "info", want: slog.LevelInfo},
		{in: "DEBUG", want: slog.LevelDebug},
		{in: "warn", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "verbose", fails: true},
	} {
		got, err := ParseLevel(tc.in)
		if tc.fails {
			if err == nil {
				t.Fatalf("ParseLevel(%q): want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestClassifyReadsTheMarkersTheCodebaseWrites pins the level/component
// derivation for the standard-library lines the worker still emits, including
// the refusal to invent a component out of prose.
func TestClassifyReadsTheMarkersTheCodebaseWrites(t *testing.T) {
	for _, tc := range []struct {
		line          string
		wantLevel     slog.Level
		wantComponent string
		wantMsg       string
	}{
		{
			line:      "WARN: no GPU detected at device 0 (no /dev/nvidia0); rendering will fail",
			wantLevel: slog.LevelWarn,
			wantMsg:   "no GPU detected at device 0 (no /dev/nvidia0); rendering will fail",
		},
		{
			line:          "ERROR processor: workspace cleanup failed for job job-1 (total failures=2): boom",
			wantLevel:     slog.LevelError,
			wantComponent: "processor",
			wantMsg:       "workspace cleanup failed for job job-1 (total failures=2): boom",
		},
		{
			line:          "[processor WARN] chronon receipt size 10 != output 11; hashing the output directly",
			wantLevel:     slog.LevelWarn,
			wantComponent: "processor",
			wantMsg:       "chronon receipt size 10 != output 11; hashing the output directly",
		},
		{
			// The marker becomes the LEVEL; prose that merely follows it stays in
			// the message ("buffer full" is not a package name).
			line:      "ERROR buffer full: dropping the frame",
			wantLevel: slog.LevelError,
			wantMsg:   "buffer full: dropping the frame",
		},
		{
			line:      "prep claim: connection refused",
			wantLevel: slog.LevelInfo,
			wantMsg:   "prep claim: connection refused",
		},
	} {
		level, component, msg := classify(tc.line)
		if level != tc.wantLevel || component != tc.wantComponent || msg != tc.wantMsg {
			t.Fatalf("classify(%q) = (%v, %q, %q), want (%v, %q, %q)",
				tc.line, level, component, msg, tc.wantLevel, tc.wantComponent, tc.wantMsg)
		}
	}
}

// TestBridgedStdlibLinesBecomeStructuredRecords is the migration guarantee: the
// call sites that were NOT converted must still produce a levelled structured
// record, so the stream has one format at any moment.
func TestBridgedStdlibLinesBecomeStructuredRecords(t *testing.T) {
	buf := initToBuffer(t, "info", FormatJSON)

	log.Printf("WARN: bridged warn line")
	log.Printf("plain info line")

	records := parseJSONLines(t, buf.String())
	if len(records) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(records), records)
	}
	if records[0]["level"] != "WARN" {
		t.Fatalf("want the bridged WARN marker to set the level, got %v", records[0])
	}
	if records[0]["msg"] != "bridged warn line" {
		t.Fatalf("want the marker stripped from the message, got %v", records[0]["msg"])
	}
	if records[1]["level"] != "INFO" {
		t.Fatalf("want INFO default, got %v", records[1])
	}
	if records[1]["logger"] != "stdlib" {
		t.Fatalf("want the bridged records marked, got %v", records[1])
	}
}

// TestJobRecordsCarryJobAndParentJobId is the point of the whole package: the
// parent_job_id the master stamps must appear on the worker's lines for that
// job, including through ByJobID (the packages that only receive an id string).
func TestJobRecordsCarryJobAndParentJobId(t *testing.T) {
	buf := initToBuffer(t, "info", FormatJSON)
	dir := t.TempDir()
	live := filepath.Join(dir, "job-1", "worker.log")
	durable := filepath.Join(dir, ".workerlogs", "job-1.log")

	job := BindJob("prefetch-fix-1790095227", "job-1")
	if job.ParentID() != "prefetch-fix-1790095227" {
		t.Fatalf("ParentID = %q", job.ParentID())
	}
	if _, err := AttachJobLog("job-1", live, durable); err != nil {
		t.Fatalf("AttachJobLog: %v", err)
	}
	job.Infof("progress: frames_done=%d", 12)
	// A package that only ever received the id string must still resolve the
	// run: this is what makes `grep job_1790…` work without threading a logger.
	ByJobID("job-1").Warnf("artifact identity %s", "receipt")
	if err := CloseJobLog("job-1"); err != nil {
		t.Fatalf("CloseJobLog: %v", err)
	}

	for _, path := range []string{live, durable} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(content)
		for _, want := range []string{
			"job_id=job-1",
			"parent_job_id=prefetch-fix-1790095227",
			"progress: frames_done=12",
			"artifact identity receipt",
			"WARN",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s is missing %q:\n%s", path, want, text)
			}
		}
	}

	records := parseJSONLines(t, buf.String())
	if len(records) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(records), records)
	}
	for _, rec := range records {
		if rec["job_id"] != "job-1" || rec["parent_job_id"] != "prefetch-fix-1790095227" {
			t.Fatalf("record lacks job identity: %v", rec)
		}
	}
}

// TestAnUnboundJobStillLogsWithoutInventingIds: the zero Job (a startup or
// maintenance path with no job) must not fabricate a job_id.
func TestAnUnboundJobStillLogsWithoutInventingIds(t *testing.T) {
	buf := initToBuffer(t, "info", FormatJSON)
	var zero Job
	zero.Infof("startup line")
	records := parseJSONLines(t, buf.String())
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	if _, ok := records[0]["job_id"]; ok {
		t.Fatalf("unbound job invented an id: %v", records[0])
	}
	if records[0]["msg"] != "startup line" {
		t.Fatalf("msg = %v", records[0]["msg"])
	}
}

// TestJobLogIsSizeCapped: the files live under the jobs root (often tmpfs), so
// the cap is a RAM-safety property, not a nicety. The truncation must be
// recorded ONCE and the writes must keep succeeding (a full disk must not turn
// into a failed render).
func TestJobLogIsSizeCapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "job-cap.log")
	if _, err := AttachJobLog("job-cap", path); err != nil {
		t.Fatalf("AttachJobLog: %v", err)
	}
	job := ByJobID("job-cap")
	payload := strings.Repeat("x", 64*1024)
	for i := 0; i < (jobLogMaxBytes/len(payload))+8; i++ {
		job.Infof("%s", payload)
	}
	if err := CloseJobLog("job-cap"); err != nil {
		t.Fatalf("CloseJobLog: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() > jobLogMaxBytes+4096 {
		t.Fatalf("job log grew past the cap: %d bytes", info.Size())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "truncated at") {
		t.Fatalf("the cap must be recorded in the file, got %d bytes without a note", len(content))
	}
	if strings.Count(string(content), "truncated at") != 1 {
		t.Fatalf("the truncation note must be written exactly once")
	}
}

// TestPruneJobLogsAppliesRetentionAndCount pins both bounds: age first, then
// the file count (oldest first), so the directory cannot grow either way.
func TestPruneJobLogsAppliesRetentionAndCount(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-72 * time.Hour)
	write := func(name string, mod time.Time) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return path
	}
	stale := write("stale.log", old)
	fresh := []string{
		write("a.log", time.Now().Add(-3*time.Hour)),
		write("b.log", time.Now().Add(-2*time.Hour)),
		write("c.log", time.Now().Add(-1*time.Hour)),
	}

	removed, err := PruneJobLogs(dir, 48*time.Hour, 2)
	if err != nil {
		t.Fatalf("PruneJobLogs: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (1 stale by age + 1 oldest by count)", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the expired log must be gone: %v", err)
	}
	kept := 0
	for _, name := range fresh {
		if _, err := os.Stat(name); err == nil {
			kept++
		}
	}
	if kept != 2 {
		t.Fatalf("kept %d of 3 fresh logs, want 2 (max_files)", kept)
	}
}

// TestDurableJobLogPathStaysInsideTheLogDirectory: a job id is
// producer-controlled, so it must never be able to escape the directory.
func TestDurableJobLogPathStaysInsideTheLogDirectory(t *testing.T) {
	dir := filepath.Join("/jobs", ".workerlogs")
	for _, hostile := range []string{"../../etc/passwd", "..", "/absolute/path", "a/b"} {
		got := DurableJobLogPath("/jobs", hostile)
		if filepath.Dir(got) != dir {
			t.Fatalf("DurableJobLogPath(%q) = %q, which is not inside %s", hostile, got, dir)
		}
		if strings.Contains(filepath.Base(got), "..") {
			t.Fatalf("DurableJobLogPath(%q) = %q keeps a traversal sequence", hostile, got)
		}
	}
	if DurableJobLogDir("") != "" {
		t.Fatalf("an empty jobs root must have no durable log dir")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func parseJSONLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record is not JSON: %q", line)
		}
		records = append(records, rec)
	}
	return records
}
