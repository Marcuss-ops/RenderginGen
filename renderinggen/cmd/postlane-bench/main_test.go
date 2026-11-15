package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunSubmitsPollsAndWritesPostlaneMetrics(t *testing.T) {
	const jobID = "job-postlane-1"
	var submitted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/jobs":
			submitted = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"` + jobID + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/"+jobID:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"` + jobID + `",
				"state":"completed",
				"worker":"renderinggen-test",
				"artifact":{
					"artifact_hash":"abc123",
					"metrics":{
						"canonicalize_overlay_ms":13088.026,
						"render_ms":7144.685,
						"gpu_lane_wait_ms":0.113,
						"gpu_gap_us":117595354,
						"total_ms":22184.073
					}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	jobPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(jobPath, []byte(`{"schema":"renderinggen.job","version":1,"render_plan":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "report.json")
	err := run(context.Background(), options{
		QueueURL:     server.URL,
		JobPath:      jobPath,
		OutPath:      outPath,
		Timeout:      time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !submitted {
		t.Fatal("job was not submitted")
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != reportSchema || got.JobID != jobID || got.State != "completed" {
		t.Fatalf("report identity = %+v", got)
	}
	if got.ArtifactSHA256 != "abc123" {
		t.Fatalf("artifact hash = %q, want the queue's artifact_hash field", got.ArtifactSHA256)
	}
	if got.CanonicalizeOverlayMS == nil || *got.CanonicalizeOverlayMS != 13088.026 {
		t.Fatalf("canonicalize = %v", got.CanonicalizeOverlayMS)
	}
	if got.RenderMS == nil || *got.RenderMS != 7144.685 {
		t.Fatalf("render = %v", got.RenderMS)
	}
	if got.GPULaneWaitMS == nil || *got.GPULaneWaitMS != 0.113 {
		t.Fatalf("gpu lane wait = %v", got.GPULaneWaitMS)
	}
	if got.GPUGapUS == nil || *got.GPUGapUS != 117595354 {
		t.Fatalf("gpu gap = %v", got.GPUGapUS)
	}
	if got.TotalMS == nil || *got.TotalMS != 22184.073 {
		t.Fatalf("total = %v", got.TotalMS)
	}
}
