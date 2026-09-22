package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// TestLoggingDefaultsAreShipped pins the shipped log surface: info level in
// JSON (what the collector and journalctl -o json read), per-job logs kept 48 h
// with a 2000-file cap. An unmodified deployment must behave exactly as before
// the log surface existed.
func TestLoggingDefaultsAreShipped(t *testing.T) {
	var c Config
	applyLoggingDefaults(&c.Logging)
	if c.Logging.Level != "info" {
		t.Fatalf("level = %q, want info", c.Logging.Level)
	}
	if c.Logging.Format != workerlog.FormatJSON {
		t.Fatalf("format = %q, want %q", c.Logging.Format, workerlog.FormatJSON)
	}
	if c.Logging.JobLogRetention != 48*time.Hour {
		t.Fatalf("job_log_retention = %v, want 48h", c.Logging.JobLogRetention)
	}
	if c.Logging.JobLogMaxFiles != 2000 {
		t.Fatalf("job_log_max_files = %d, want 2000", c.Logging.JobLogMaxFiles)
	}
	if err := c.Logging.validate(); err != nil {
		t.Fatalf("the shipped defaults must validate: %v", err)
	}
}

// TestLoggingValidationRejectsWhatTheLoggerCannotHonour: a typo must fail at
// LOAD. A worker that started with an unparsable level would either die at
// startup (after accepting a config that looked fine) or silently run at a
// different verbosity than the operator asked for.
func TestLoggingValidationRejectsWhatTheLoggerCannotHonour(t *testing.T) {
	base := func() LoggingConfig {
		l := LoggingConfig{}
		applyLoggingDefaults(&l)
		return l
	}
	for _, tc := range []struct {
		name   string
		mutate func(*LoggingConfig)
	}{
		{name: "unknown level", mutate: func(l *LoggingConfig) { l.Level = "verbose" }},
		{name: "unknown format", mutate: func(l *LoggingConfig) { l.Format = "logfmt" }},
		{name: "negative retention", mutate: func(l *LoggingConfig) { l.JobLogRetention = -time.Second }},
		{name: "negative max files", mutate: func(l *LoggingConfig) { l.JobLogMaxFiles = -1 }},
	} {
		l := base()
		tc.mutate(&l)
		if err := l.validate(); err == nil {
			t.Fatalf("%s: want an error", tc.name)
		}
	}

	// The accepted spellings, including the debug/text pair an operator uses
	// while chasing a bad render.
	for _, level := range []string{"debug", "info", "warn", "error"} {
		l := base()
		l.Level = level
		if err := l.validate(); err != nil {
			t.Fatalf("level %q must be accepted: %v", level, err)
		}
	}
	l := base()
	l.Format = workerlog.FormatText
	if err := l.validate(); err != nil {
		t.Fatalf("text format must be accepted: %v", err)
	}
}

// TestLoggingConfigRoundTripsThroughYAML: the settings are read from
// renderinggen.yaml (and overridable through the RENDERINGGEN_* overlay), so the
// keys must survive a real Load.
func TestLoggingConfigRoundTripsThroughYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "renderinggen.yaml")
	body := `queue:
  endpoint: https://queue.example
artifact_store:
  endpoint: https://store.example
logging:
  level: debug
  format: text
  job_log_retention: 6h
  job_log_max_files: 7
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != workerlog.FormatText {
		t.Fatalf("logging level/format not loaded: %+v", cfg.Logging)
	}
	if cfg.Logging.JobLogRetention != 6*time.Hour || cfg.Logging.JobLogMaxFiles != 7 {
		t.Fatalf("logging retention/max files not loaded: %+v", cfg.Logging)
	}

	// And a typo fails at load, naming the key the operator must fix.
	bad := strings.Replace(body, "level: debug", "level: verbose", 1)
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("an unknown logging.level must fail at load")
	} else if !strings.Contains(err.Error(), "logging.level") {
		t.Fatalf("the error must name the key: %v", err)
	}
}

// TestShippedConfigDeclaresTheLogSurface guards the documented sample: an
// operator reading config.yaml must find the settings that produce the worker's
// records, since the whole point of the log surface is discoverability.
func TestShippedConfigDeclaresTheLogSurface(t *testing.T) {
	data, err := os.ReadFile("../../config.yaml")
	if err != nil {
		t.Skipf("shipped config not present in this checkout: %v", err)
	}
	for _, key := range []string{"logging:", "level:", "format:", "job_log_retention:", "job_log_max_files:"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("shipped config.yaml does not declare %q", key)
		}
	}
}
