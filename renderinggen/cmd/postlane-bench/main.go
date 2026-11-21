// Command postlane-bench runs one canonical RenderingGen job through a queue
// and writes the post-lane metrics to JSON.
//
// The report is the ledger projection an operator needs to compare the
// canonicalization move: canonicalize_overlay_ms (post pool), render_ms (GPU
// lane), gpu_lane_wait_ms and gpu_gap_us, plus total_ms. It can submit a job
// envelope with -job or observe an already-completed job with -job-id; both
// modes read the queue's authoritative artifact metrics.
//
// Usage:
//
//	postlane-bench -queue <endpoint> -job canonical-job.json -out report.json
//	postlane-bench -queue <endpoint> -job-id postlane-canonical-... -out report.json
//
// The job file is the renderinggen.job.v1 envelope accepted by POST /jobs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

const reportSchema = "renderinggen.postlane-bench.v1"

type options struct {
	QueueURL     string
	JobPath      string
	JobID        string
	OutPath      string
	Timeout      time.Duration
	PollInterval time.Duration
}

type queueJob struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Worker   string `json:"worker"`
	Artifact *struct {
		ID           string             `json:"id"`
		ArtifactHash string             `json:"artifact_hash"`
		SHA256       string             `json:"sha256"`
		Hash         string             `json:"hash"`
		Metrics      map[string]float64 `json:"metrics"`
	} `json:"artifact"`
}

type report struct {
	Schema                string             `json:"schema"`
	GeneratedAt           string             `json:"generated_at"`
	Queue                 string             `json:"queue"`
	JobID                 string             `json:"job_id"`
	State                 string             `json:"state"`
	Worker                string             `json:"worker,omitempty"`
	ArtifactSHA256        string             `json:"artifact_sha256,omitempty"`
	CanonicalizeOverlayMS *float64           `json:"canonicalize_overlay_ms"`
	RenderMS              *float64           `json:"render_ms"`
	GPULaneWaitMS         *float64           `json:"gpu_lane_wait_ms"`
	GPUGapUS              *float64           `json:"gpu_gap_us"`
	TotalMS               *float64           `json:"total_ms"`
	Metrics               map[string]float64 `json:"metrics"`
}

func main() {
	var opts options
	flag.StringVar(&opts.QueueURL, "queue", config.DefaultQueueEndpoint, "queue base URL")
	flag.StringVar(&opts.JobPath, "job", "", "renderinggen.job.v1 envelope to submit")
	flag.StringVar(&opts.JobID, "job-id", "", "existing job ID to observe instead of submitting")
	flag.StringVar(&opts.OutPath, "out", "", "JSON report path (required)")
	flag.DurationVar(&opts.Timeout, "timeout", 15*time.Minute, "maximum wait for a terminal job")
	flag.DurationVar(&opts.PollInterval, "poll", 2*time.Second, "job poll interval")
	flag.Parse()

	if err := run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "postlane-bench:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options) error {
	opts.QueueURL = strings.TrimRight(strings.TrimSpace(opts.QueueURL), "/")
	if opts.QueueURL == "" {
		return fmt.Errorf("queue URL is required")
	}
	if opts.OutPath == "" {
		return fmt.Errorf("out path is required")
	}
	if opts.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if opts.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive")
	}
	if (opts.JobPath == "") == (opts.JobID == "") {
		return fmt.Errorf("exactly one of -job or -job-id is required")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	jobID := opts.JobID
	if opts.JobPath != "" {
		data, err := os.ReadFile(opts.JobPath)
		if err != nil {
			return fmt.Errorf("read job: %w", err)
		}
		jobID, data, err = ensureJobID(data)
		if err != nil {
			return fmt.Errorf("decode job: %w", err)
		}
		submittedID, err := submitJob(ctx, client, opts.QueueURL, data)
		if err != nil {
			return err
		}
		if submittedID != "" {
			jobID = submittedID
		}
	}

	job, err := waitForJob(ctx, client, opts.QueueURL, jobID, opts.Timeout, opts.PollInterval)
	if err != nil && job == nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job %s returned no result", jobID)
	}
	finished := buildReport(opts.QueueURL, job)
	if err := writeReport(opts.OutPath, finished); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if job.State != "completed" {
		return fmt.Errorf("job %s finished in state %s (report: %s)", job.ID, job.State, opts.OutPath)
	}
	return nil
}

func ensureJobID(data []byte) (string, []byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", nil, err
	}
	var id string
	if raw, ok := envelope["id"]; ok {
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", nil, fmt.Errorf("id: %w", err)
		}
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("postlane-bench-%d", time.Now().UnixNano())
		raw, err := json.Marshal(id)
		if err != nil {
			return "", nil, err
		}
		envelope["id"] = raw
		data, err = json.Marshal(envelope)
		if err != nil {
			return "", nil, err
		}
	}
	return id, data, nil
}

func submitJob(ctx context.Context, client *http.Client, queueURL string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queueURL+"/jobs", strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit job: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read submit response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("submit job: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var accepted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}
	return accepted.ID, nil
}

func waitForJob(ctx context.Context, client *http.Client, queueURL, jobID string, timeout, poll time.Duration) (*queueJob, error) {
	deadline := time.Now().Add(timeout)
	var last *queueJob
	for {
		job, err := getJob(ctx, client, queueURL, jobID)
		if err != nil {
			return last, err
		}
		last = job
		switch job.State {
		case "completed", "failed", "cancelled":
			return job, nil
		}
		if time.Now().After(deadline) {
			return job, fmt.Errorf("job %s did not finish within %s (last state %s)", jobID, timeout, job.State)
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func getJob(ctx context.Context, client *http.Client, queueURL, jobID string) (*queueJob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queueURL+"/jobs/"+jobID, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get job %s: %w", jobID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read job %s: %w", jobID, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get job %s: HTTP %d: %s", jobID, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var job queueJob
	if err := json.Unmarshal(body, &job); err != nil {
		return nil, fmt.Errorf("decode job %s: %w", jobID, err)
	}
	return &job, nil
}

func buildReport(queueURL string, job *queueJob) report {
	metrics := map[string]float64{}
	var artifactSHA string
	if job.Artifact != nil {
		if job.Artifact.Metrics != nil {
			metrics = job.Artifact.Metrics
		}
		artifactSHA = job.Artifact.ArtifactHash
		if artifactSHA == "" {
			artifactSHA = job.Artifact.SHA256
		}
		if artifactSHA == "" {
			artifactSHA = job.Artifact.Hash
		}
	}
	return report{
		Schema:                reportSchema,
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		Queue:                 queueURL,
		JobID:                 job.ID,
		State:                 job.State,
		Worker:                job.Worker,
		ArtifactSHA256:        artifactSHA,
		CanonicalizeOverlayMS: metric(metrics, metricnames.CanonicalizeOverlayMS),
		RenderMS:              metric(metrics, metricnames.RenderMS),
		GPULaneWaitMS:         metric(metrics, metricnames.GPULaneWaitMS),
		GPUGapUS:              metric(metrics, metricnames.GPUGapUS),
		TotalMS:               metric(metrics, metricnames.TotalMS),
		Metrics:               metrics,
	}
}

func metric(metrics map[string]float64, name string) *float64 {
	value, ok := metrics[name]
	if !ok {
		return nil
	}
	return &value
}

func writeReport(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
