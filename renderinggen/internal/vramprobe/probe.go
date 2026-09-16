// Package vramprobe measures the device working set of ONE overlay render, per
// job kind, on the host it runs on.
//
// Why it exists. The renderer serializes the RENDER_JOB execution domain because
// a single 1080p exporter was measured at ~8.8 GB peak on a 16 GB card, and that
// figure is the only gate between today's throughput (one job at a time) and a
// second concurrent exporter. A quoted comment is not a measurement, so the probe
// takes two real jobs out of a certified manifest, perturbs one semantic value
// each (a new content address, so no cache can answer for the render), submits
// them through the normal queue path, and samples nvidia-smi while they are in
// flight.
//
// It reports the idle baseline too, because a second job's cost is the DELTA: the
// absolute peak on its own says nothing about headroom.
package vramprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/batch"
)

// ReportSchema identifies the probe report.
const ReportSchema = "renderinggen.vram-probe-report.v1"

// Options configures one probe.
type Options struct {
	// SourceManifest is a certified batch manifest the two probe jobs come from.
	SourceManifest string
	// ProbeManifest is where the perturbed two-job manifest is written.
	ProbeManifest string
	// BatchID scopes the probe jobs (a fresh id keeps them out of the corpus).
	BatchID string
	// QueueURL is the central queue endpoint.
	QueueURL string
	// Interval is the nvidia-smi sampling period.
	Interval time.Duration
	// Timeout bounds the render window.
	Timeout time.Duration
	// ReportPath is the report to write (required).
	ReportPath string
	Logger     *log.Logger
}

// Report is the probe document of record.
type Report struct {
	Schema          string            `json:"schema"`
	BatchID         string            `json:"batch_id"`
	Jobs            map[string]string `json:"jobs"`
	IdleDeviceMiB   int               `json:"idle_device_mib"`
	IdleDaemonMiB   int               `json:"idle_daemon_mib"`
	PeakDeviceMiB   int               `json:"peak_device_mib"`
	PeakDaemonMiB   int               `json:"peak_daemon_mib"`
	PeakUtilization int               `json:"peak_utilization_pct"`
	DeviceDeltaMiB  int               `json:"device_delta_mib"`
	DaemonDeltaMiB  int               `json:"daemon_delta_mib"`
	Samples         int               `json:"samples"`
	WindowStart     time.Time         `json:"window_start"`
	WindowEnd       time.Time         `json:"window_end"`
	SourceManifest  string            `json:"source_manifest"`
	ProbeManifest   string            `json:"probe_manifest"`
}

// Run builds the probe, renders it on the production path and samples the device.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.SourceManifest == "" {
		return nil, fmt.Errorf("vramprobe: -source-manifest is required")
	}
	if opts.ProbeManifest == "" {
		return nil, fmt.Errorf("vramprobe: -probe-manifest is required")
	}
	if opts.ReportPath == "" {
		return nil, fmt.Errorf("vramprobe: -out is required")
	}
	if opts.BatchID == "" {
		opts.BatchID = "vram-probe"
	}
	if opts.QueueURL == "" {
		opts.QueueURL = "http://localhost:8081"
	}
	if opts.Interval <= 0 {
		opts.Interval = 50 * time.Millisecond
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "vram-probe: ", log.LstdFlags)
	}

	probeRaw, jobIDs, err := buildProbe(opts.SourceManifest, opts.BatchID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.ProbeManifest), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(opts.ProbeManifest, probeRaw, 0o644); err != nil {
		return nil, fmt.Errorf("vramprobe: write probe manifest: %w", err)
	}

	idleDevice, idleUtil, err := deviceSample(ctx)
	if err != nil {
		return nil, fmt.Errorf("vramprobe: nvidia-smi: %w", err)
	}
	idleDaemon := daemonMiB(ctx)
	_ = idleUtil

	var (
		mu      sync.Mutex
		samples []sample
		stop    = make(chan struct{})
		done    = make(chan struct{})
	)
	go func() {
		defer close(done)
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				device, util, err := deviceSample(ctx)
				if err != nil {
					continue
				}
				entry := sample{device: device, daemon: daemonMiB(ctx), utilization: util}
				mu.Lock()
				samples = append(samples, entry)
				mu.Unlock()
			}
		}
	}()

	windowStart := time.Now()
	client := queue.New(opts.QueueURL)
	if err := client.Health(ctx); err != nil {
		close(stop)
		<-done
		return nil, fmt.Errorf("vramprobe: queue %s is not healthy: %w", opts.QueueURL, err)
	}
	jobs, err := batch.Decode(probeRaw)
	if err != nil {
		close(stop)
		<-done
		return nil, err
	}
	if _, err := batch.SubmitAll(ctx, batch.ClientSubmitter{Client: client}, jobs); err != nil {
		close(stop)
		<-done
		return nil, err
	}

	renderCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	states := map[string]string{}
	for _, id := range jobIDs {
		body, err := client.WaitTerminal(renderCtx, id)
		if err != nil {
			close(stop)
			<-done
			return nil, fmt.Errorf("vramprobe: wait %s: %w", id, err)
		}
		states[id] = string(body.State)
	}
	windowEnd := time.Now()
	close(stop)
	<-done

	report := &Report{
		Schema:         ReportSchema,
		BatchID:        opts.BatchID,
		Jobs:           states,
		IdleDeviceMiB:  idleDevice,
		IdleDaemonMiB:  idleDaemon,
		SourceManifest: opts.SourceManifest,
		ProbeManifest:  opts.ProbeManifest,
		Samples:        len(samples),
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
	}
	for _, entry := range samples {
		report.PeakDeviceMiB = max(report.PeakDeviceMiB, entry.device)
		report.PeakDaemonMiB = max(report.PeakDaemonMiB, entry.daemon)
		report.PeakUtilization = max(report.PeakUtilization, entry.utilization)
	}
	report.DeviceDeltaMiB = report.PeakDeviceMiB - idleDevice
	report.DaemonDeltaMiB = report.PeakDaemonMiB - idleDaemon

	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(opts.ReportPath, append(raw, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("vramprobe: write report: %w", err)
	}
	logger.Printf("idle %d MiB (daemon %d MiB) -> peak %d MiB (daemon %d MiB), delta %d MiB over %d samples",
		report.IdleDeviceMiB, report.IdleDaemonMiB, report.PeakDeviceMiB, report.PeakDaemonMiB, report.DeviceDeltaMiB, report.Samples)
	return report, nil
}

type sample struct {
	device      int
	daemon      int
	utilization int
}

// buildProbe takes one phrase job and one image job from the source manifest and
// perturbs each plan so the render cannot be answered from a content-addressed
// cache: a new duration (one extra frame) and, for text, one extra character.
func buildProbe(sourcePath, batchID string) ([]byte, []string, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("vramprobe: read source manifest: %w", err)
	}
	var source struct {
		SchemaVersion string                       `json:"schema_version"`
		Jobs          []map[string]json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, nil, fmt.Errorf("vramprobe: decode source manifest: %w", err)
	}

	var phrase, image map[string]json.RawMessage
	for _, job := range source.Jobs {
		id := rawString(job["id"])
		if phrase == nil && strings.Contains(id, "phrase") {
			phrase = job
			continue
		}
		if image == nil && !strings.Contains(id, "phrase") {
			image = job
		}
	}
	if phrase == nil || image == nil {
		return nil, nil, fmt.Errorf("vramprobe: source manifest needs one phrase and one image job, got phrase=%v image=%v", phrase != nil, image != nil)
	}

	probeJobs := make([]map[string]json.RawMessage, 0, 2)
	jobIDs := make([]string, 0, 2)
	for index, job := range []map[string]json.RawMessage{phrase, image} {
		logical := "vram_probe_phrase"
		if index == 1 {
			logical = "vram_probe_image"
		}
		patched, err := perturbPlan(job["render_plan"], logical)
		if err != nil {
			return nil, nil, err
		}
		clone := make(map[string]json.RawMessage, len(job))
		for key, value := range job {
			clone[key] = value
		}
		clone["id"] = mustJSON(logical)
		clone["render_plan"] = patched
		probeJobs = append(probeJobs, clone)
		jobIDs = append(jobIDs, batchID+":"+logical)
	}

	document := struct {
		SchemaVersion string                       `json:"schema_version"`
		BatchID       string                       `json:"batch_id"`
		Jobs          []map[string]json.RawMessage `json:"jobs"`
	}{SchemaVersion: source.SchemaVersion, BatchID: batchID, Jobs: probeJobs}
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(out, '\n'), jobIDs, nil
}

// perturbPlan rewrites the plan identity and duration, and appends one character
// to a text item so the phrase job's pixels change too.
func perturbPlan(raw json.RawMessage, logicalID string) (json.RawMessage, error) {
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("vramprobe: decode plan for %s: %w", logicalID, err)
	}
	plan["plan_id"] = logicalID + "__probe"
	plan["video_id"] = logicalID + "__probe"
	if duration, ok := plan["duration_ms"].(float64); ok {
		plan["duration_ms"] = duration + 10 // one extra frame at 100 fps-safe granularity
	}
	if items, ok := plan["items"].([]any); ok {
		for _, entry := range items {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := item["text"].(string); ok {
				item["text"] = text + " "
			}
			if end, ok := item["end_ms"].(float64); ok {
				item["end_ms"] = end + 10
			}
		}
	}
	out, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func rawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

// deviceSample reads the device-wide used memory and utilization.
func deviceSample(ctx context.Context) (int, int, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=memory.used,utilization.gpu", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("unexpected nvidia-smi output %q", strings.TrimSpace(string(out)))
	}
	used, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return 0, 0, err
	}
	utilization, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil {
		return 0, 0, err
	}
	return used, utilization, nil
}

// daemonMiB sums the device memory held by chronon3d_cli processes: the warm
// daemon's own residency, which is what makes the second exporter's cost a delta
// rather than the absolute peak.
func daemonMiB(ctx context.Context) int {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0
	}
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pid := strings.TrimSpace(fields[0])
		memory, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		command, err := os.ReadFile("/proc/" + pid + "/cmdline")
		if err != nil {
			continue // the process exited between the listing and the read
		}
		if strings.Contains(strings.ReplaceAll(string(command), "\x00", " "), "chronon3d_cli") {
			total += memory
		}
	}
	return total
}
