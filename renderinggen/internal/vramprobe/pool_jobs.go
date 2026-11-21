package vramprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// pool_jobs.go owns the per-job helpers of the pool run: launching the job
// batch, the failure and identity comparisons, path absolutization and plan
// reading, plus the small hashing/formatting helpers the report uses.

// runPoolJobs submits one job per runtime CONCURRENTLY and waits for both.
//
// The two goroutines start behind a barrier so the renders genuinely overlap —
// a sequential loop would measure one runtime twice and hide the whole point.
func runPoolJobs(ctx context.Context, opts PoolOptions, pool *chronon.DaemonPool, sockets []string, shape PoolShape, logger *log.Logger) []PoolJobFacts {
	jobs := make([]PoolJobFacts, opts.Daemons)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < opts.Daemons; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			output := filepath.Join(opts.OutputDir, fmt.Sprintf("two-runtime-job-%d.mp4", index))
			facts := PoolJobFacts{Index: index, Output: output, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			if index < len(sockets) {
				facts.Socket = sockets[index]
			}
			<-start
			renderCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			request := chronon.RenderRequest{
				PlanPath:        opts.PlanPath,
				AssetsRoot:      opts.AssetsRoot,
				OutputPath:      output,
				EncodePreset:    opts.EncodePreset,
				HardwareEncoder: opts.HardwareEncoder,
				TotalFrames:     int64(shape.Frames),
				Requirements: chronon.ExecutionRequirements{
					Backend:            opts.Backend,
					GPURequired:        opts.HardwareEncoder != "" && opts.HardwareEncoder != chronon.HardwareEncoderNone,
					CPUFallbackAllowed: true,
				},
			}
			began := time.Now()
			err := pool.Render(renderCtx, request)
			facts.RenderMS = time.Since(began).Milliseconds()
			facts.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			facts.WallMS = facts.RenderMS
			if err != nil {
				facts.FailedWith = err.Error()
				logger.Printf("job %d on %s FAILED: %v", index, facts.Socket, err)
				jobs[index] = facts
				return
			}
			if info, statErr := os.Stat(output); statErr == nil {
				facts.Bytes = info.Size()
			}
			facts.SHA256 = fileSHA256(output)
			logger.Printf("job %d on %s done in %d ms (%d bytes, sha %s)", index, facts.Socket, facts.RenderMS, facts.Bytes, firstN(facts.SHA256, 12))
			jobs[index] = facts
		}(i)
	}
	close(start)
	wg.Wait()
	return jobs
}

// failedJobs names every render that did not produce an artifact, or "" when
// each runtime rendered. A two-runtime measurement with one failed job did not
// measure two runtimes, so the report is written and the call is an error.
func failedJobs(jobs []PoolJobFacts) string {
	failed := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.FailedWith != "" {
			failed = append(failed, fmt.Sprintf("job %d: %s", job.Index, job.FailedWith))
		}
	}
	return strings.Join(failed, "; ")
}

// jobsIdentical reports whether every job produced the same bytes. Same plan,
// same host, same encoder: identical output is what makes the two-runtime number
// comparable to a single-runtime render instead of a different workload.
func jobsIdentical(jobs []PoolJobFacts) bool {
	reference := ""
	for _, job := range jobs {
		if job.FailedWith != "" || job.SHA256 == "" {
			return false
		}
		if reference == "" {
			reference = job.SHA256
			continue
		}
		if job.SHA256 != reference {
			return false
		}
	}
	return reference != ""
}

// absolutizePoolPaths resolves every input and output path the run depends on.
func absolutizePoolPaths(opts *PoolOptions) error {
	fields := []struct {
		name  string
		value *string
	}{
		{"plan", &opts.PlanPath},
		{"assets root", &opts.AssetsRoot},
		{"output dir", &opts.OutputDir},
		{"report", &opts.ReportPath},
		{"samples", &opts.SamplesPath},
		{"chronon binary", &opts.Binary},
	}
	for _, field := range fields {
		if *field.value == "" {
			continue
		}
		absolved, err := filepath.Abs(*field.value)
		if err != nil {
			return fmt.Errorf("vramprobe: resolve %s %q: %w", field.name, *field.value, err)
		}
		*field.value = absolved
	}
	return nil
}

// readPoolPlan reads the SHAPE out of the concrete render plan and hashes the
// document, so the evidence names exactly what was rendered.
func readPoolPlan(path string) (PoolShape, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PoolShape{}, "", fmt.Errorf("vramprobe: read plan: %w", err)
	}
	var document struct {
		Schema string `json:"schema"`
		Canvas struct {
			Width          int `json:"width"`
			Height         int `json:"height"`
			FPSNum         int `json:"fps_num"`
			FPSDen         int `json:"fps_den"`
			DurationFrames int `json:"duration_frames"`
		} `json:"canvas"`
		Output struct {
			Format string `json:"format"`
			Codec  string `json:"codec"`
		} `json:"output"`
		Layers []struct {
			Type string `json:"type"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return PoolShape{}, "", fmt.Errorf("vramprobe: decode plan %s: %w", path, err)
	}
	// The schema constants are renderbatch's: they are the one declaration of
	// the concrete plan contract, so the harness cannot silently accept a
	// document the renderer would reject. V3 is the Marshal() upgrade for
	// plans whose layers use V3 features; the decoder accepts both.
	if document.Schema != overlay.RenderPlanSchemaV2 && document.Schema != overlay.RenderPlanSchemaV3 {
		return PoolShape{}, "", fmt.Errorf("vramprobe: plan %s declares schema %q, want the concrete %s or %s (a semantic plan cannot be rendered)", path, document.Schema, overlay.RenderPlanSchemaV2, overlay.RenderPlanSchemaV3)
	}
	if document.Canvas.Width <= 0 || document.Canvas.Height <= 0 || document.Canvas.DurationFrames <= 0 {
		return PoolShape{}, "", fmt.Errorf("vramprobe: plan %s has an unusable canvas %dx%d frames=%d", path, document.Canvas.Width, document.Canvas.Height, document.Canvas.DurationFrames)
	}
	digest := sha256.Sum256(raw)
	seen := map[string]bool{}
	types := make([]string, 0, len(document.Layers))
	for _, layer := range document.Layers {
		if layer.Type == "" || seen[layer.Type] {
			continue
		}
		seen[layer.Type] = true
		types = append(types, layer.Type)
	}
	sort.Strings(types)
	return PoolShape{
		Width:        document.Canvas.Width,
		Height:       document.Canvas.Height,
		FPSNum:       document.Canvas.FPSNum,
		FPSDen:       document.Canvas.FPSDen,
		Frames:       document.Canvas.DurationFrames,
		Codec:        document.Output.Codec,
		OutputFormat: document.Output.Format,
		LayerTypes:   types,
	}, hex.EncodeToString(digest[:]), nil
}

// poolDeviceInfo reads the device identity the number belongs to.
func poolDeviceInfo(ctx context.Context) (PoolDevice, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,driver_version,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return PoolDevice{}, err
	}
	fields := strings.Split(strings.TrimSpace(strings.Split(string(out), "\n")[0]), ",")
	if len(fields) < 3 {
		return PoolDevice{}, fmt.Errorf("unexpected nvidia-smi output %q", strings.TrimSpace(string(out)))
	}
	total, err := strconv.Atoi(strings.TrimSpace(fields[2]))
	if err != nil {
		return PoolDevice{}, err
	}
	return PoolDevice{
		Name:          strings.TrimSpace(fields[0]),
		DriverVersion: strings.TrimSpace(fields[1]),
		TotalMiB:      total,
	}, nil
}
func fileSHA256(path string) string {
	digest, _, err := hashio.File(path)
	if err != nil {
		return ""
	}
	return digest
}

func firstN(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
