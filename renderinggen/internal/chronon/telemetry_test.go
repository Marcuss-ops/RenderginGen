package chronon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadTimingSidecarDropsPerFrameArray(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "result.mp4")
	sidecar := output + ".timing.json"
	// A minimal sidecar mirroring the chronon3d.frame-timing.v1 shape: the
	// per-frame array is deep-profiling detail, the job/summary sections are
	// the source of truth the worker ingests.
	fixture := `{
  "schema": "chronon3d.frame-timing.v1",
  "wall_time_ms": 5123.0,
  "frames_total": 150,
  "frame_times_ms": [{"frame": 0}, {"frame": 1}],
  "summary": {"p50_frame_ms": 12.3, "end_to_end_fps": 29.2},
  "job": {"plan_compile_ms": 4.1, "graph_compile_ms": 2.2}
}`
	if err := os.WriteFile(sidecar, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := ReadTimingSidecar(output)
	if err != nil {
		t.Fatalf("ReadTimingSidecar: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if _, ok := doc["frame_times_ms"]; ok {
		t.Fatalf("frame_times_ms must be dropped from the ingested document")
	}
	for _, key := range []string{"schema", "wall_time_ms", "frames_total", "summary", "job"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("ingested document lost key %q: %s", key, got)
		}
	}
}

func TestReadTimingSidecarMissingFile(t *testing.T) {
	if _, err := ReadTimingSidecar(filepath.Join(t.TempDir(), "result.mp4")); err == nil {
		t.Fatal("expected an error for a missing sidecar")
	}
}
