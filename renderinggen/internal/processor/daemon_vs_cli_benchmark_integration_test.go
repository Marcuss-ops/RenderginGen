package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

// benchRun is one measured render through one backend path.
type benchRun struct {
	Backend string // "cli" or "daemon"
	Phase   string // "cold" or "warm"
	TotalMS float64
	Timing  timingSummary
}

type timingSummary struct {
	RenderMS          float64
	WallMS            float64
	EncodeCloseMS     float64
	P50FrameMS        float64
	P95FrameMS        float64
	P99FrameMS        float64
	GPUExecuteMS      *float64
	GPUReadbackMS     *float64
	GPUNodes          *int64
	FallbackNodes     *int64
	FallbackDrawNode  *int64
	FallbackTextRun   *int64
	FallbackComposite *int64
	FallbackEffect    *int64
	FallbackBlur      *int64
	FallbackDOF       *int64
	EffectiveBackend  string
	ConversionMS      float64
	Frames            int
}

type chrononTimingSidecar struct {
	FramesTotal int     `json:"frames_total"`
	RenderMS    float64 `json:"render_ms"`
	WallMS      float64 `json:"wall_time_ms"`
	EncodeClose float64 `json:"encode_close_ms"`
	Summary     struct {
		P50 float64 `json:"p50_frame_ms"`
		P95 float64 `json:"p95_frame_ms"`
		P99 float64 `json:"p99_frame_ms"`
	} `json:"summary"`
	Job struct {
		GPU struct {
			Execute           *float64 `json:"gpu_execute_ms"`
			Readback          *float64 `json:"gpu_readback_ms"`
			Nodes             *int64   `json:"gpu_nodes"`
			Fallback          *int64   `json:"software_fallback_nodes"`
			FallbackDrawNode  *int64   `json:"fallback_draw_node"`
			FallbackTextRun   *int64   `json:"fallback_text_run"`
			FallbackComposite *int64   `json:"fallback_composite"`
			FallbackEffect    *int64   `json:"fallback_effect"`
			FallbackBlur      *int64   `json:"fallback_blur"`
			FallbackDOF       *int64   `json:"fallback_dof"`
			EffectiveBackend  string   `json:"effective_backend"`
		} `json:"gpu"`
		ConversionMS float64 `json:"conversion_ms"`
	} `json:"job"`
}

// TestDaemonVsCLIBenchmarkGoldenOverlay compares the two render backends on
// the SAME GoldenOverlayJobV1 workload:
//
//	CLI subprocess:  cold = first render (process spawn + cold engine)
//	                 warm = second render (OS page cache warm, engine still cold)
//	Daemon (socket): cold = first render right after daemon start
//	                 warm = second render (RenderEngine, font cache, image cache,
//	                       framebuffer/surface pools, pipelines, VRAM alive)
//
// The daemon keeps every warm component alive between jobs; the CLI subprocess
// cannot, because the process exits after each render. The recorded total-ms
// delta between "daemon warm" and "cli warm" is exactly what the warm engine
// saves. Skips when chronon3d_cli is not installed.
func TestDaemonVsCLIBenchmarkGoldenOverlay(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	// One job payload, prepared once, reused by every measured render so the
	// comparison isolates the render backend (no per-run plan variance).
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenOverlayJobV1), &job); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	job.ID = "daemon-vs-cli-bench"
	backend := os.Getenv("CHRONON_TEST_BACKEND")
	if backend == "" {
		backend = "software"
	}

	// Shared workspace with the two golden assets materialized once.
	ws, err := workspace.New(t.TempDir(), job.ID)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	defer ws.Cleanup()
	store := storage.New(storage.NewMemory(), storage.Options{})
	seedGoldenAssets(t, store, job.Assets)
	if err := ws.Materialize(context.Background(), store.Get, job.Assets); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if err := ws.WritePlan(job.RenderPlan); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	output := ws.OutputPath("result.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	var results []benchRun
	record := func(backend, phase string, start time.Time) {
		results = append(results, benchRun{
			Backend: backend,
			Phase:   phase,
			TotalMS: float64(time.Since(start).Microseconds()) / 1000.0,
		})
	}

	// ── CLI subprocess: cold + warm ──────────────────────────────────────
	for _, phase := range []string{"cold", "warm"} {
		start := time.Now()
		if err := cli.Render(ctx, chronon.RenderRequest{
			PlanPath:   ws.PlanPath(),
			AssetsRoot: ws.Root(),
			OutputPath: output,
			Backend:    backend,
			Report:     true,
		}); err != nil {
			t.Fatalf("cli %s: %v", phase, err)
		}
		record("cli", phase, start)
		results[len(results)-1].Timing = readChrononTiming(t, output)
	}

	// ── Daemon: start on a unix socket, cold + warm, then shutdown ───────
	socketPath := filepath.Join(t.TempDir(), "chronon.sock")
	daemon := startDaemon(t, cli.Binary(), socketPath, ws.Root(), backend)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = chronon.NewIPCClient(socketPath).Shutdown(shutdownCtx)
		if daemon.Process != nil {
			_ = daemon.Process.Kill()
		}
	})

	client := chronon.NewIPCClient(socketPath)
	waitForSocket(t, socketPath, 30*time.Second)

	for _, phase := range []string{"cold", "warm"} {
		start := time.Now()
		if err := client.Render(ctx, chronon.RenderRequest{
			PlanPath:   ws.PlanPath(),
			AssetsRoot: ws.Root(),
			OutputPath: output,
			Backend:    backend,
			Report:     true,
		}); err != nil {
			t.Fatalf("daemon %s: %v", phase, err)
		}
		record("daemon", phase, start)
		results[len(results)-1].Timing = readChrononTiming(t, output)
	}

	// ── Report + delta ───────────────────────────────────────────────────
	by := map[string]float64{}
	for _, r := range results {
		by[r.Backend+"-"+r.Phase] = r.TotalMS
	}
	fmt.Printf("\n=== RenderingGen daemon vs CLI benchmark (GoldenOverlayJobV1) ===\n")
	fmt.Printf("%-9s %-6s %10s %10s %10s %10s %10s %10s %10s %10s %10s %10s\n",
		"backend", "phase", "total_ms", "render_ms", "p50_ms", "p95_ms", "p99_ms", "wall_ms", "gpu_ms", "readback_ms", "gpu_nodes", "fallbacks")
	for _, r := range results {
		fmt.Printf("%-9s %-6s %10.1f %10.1f %10.3f %10.3f %10.3f %10.1f %10s %10s %10s %10s %10s\n",
			r.Backend, r.Phase, r.TotalMS, r.Timing.RenderMS, r.Timing.P50FrameMS,
			r.Timing.P95FrameMS, r.Timing.P99FrameMS, r.Timing.WallMS,
			optionalMetric(r.Timing.GPUExecuteMS), optionalMetric(r.Timing.GPUReadbackMS),
			optionalIntMetric(r.Timing.GPUNodes), optionalIntMetric(r.Timing.FallbackNodes),
			effectiveBackend(r.Timing))
	}
	cliWarm := by["cli-warm"]
	daemonWarm := by["daemon-warm"]
	if cliWarm > 0 && daemonWarm > 0 {
		fmt.Printf("\nDelta (cli warm - daemon warm): %+.1f ms (daemon %s%%)\n",
			cliWarm-daemonWarm, fmt.Sprintf("%.1f%%", daemonWarm/cliWarm*100))
	}
	fmt.Printf("Interpretation: the daemon keeps RenderEngine, font cache, image cache,\n")
	fmt.Printf("framebuffer/surface pools, Vulkan device, pipelines and VRAM cache alive\n")
	fmt.Printf("between jobs; the CLI subprocess re-spawns them for every render.\n")
}

func optionalMetric(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%.3f", *value)
}

func optionalIntMetric(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *value)
}

func effectiveBackend(t timingSummary) string {
	if t.EffectiveBackend != "" {
		return t.EffectiveBackend
	}
	if t.GPUNodes != nil && *t.GPUNodes > 0 {
		if t.FallbackNodes != nil && *t.FallbackNodes > 0 {
			return "hybrid"
		}
		return "vulkan"
	}
	if t.FallbackNodes != nil && *t.FallbackNodes > 0 {
		return "software-fallback"
	}
	return "unknown"
}

func readChrononTiming(t *testing.T, output string) timingSummary {
	t.Helper()
	data, err := os.ReadFile(output + ".timing.json")
	if err != nil {
		t.Fatalf("read Chronon timing sidecar: %v", err)
	}
	var doc chrononTimingSidecar
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode Chronon timing sidecar: %v", err)
	}
	return timingSummary{
		RenderMS:          doc.RenderMS,
		WallMS:            doc.WallMS,
		EncodeCloseMS:     doc.EncodeClose,
		P50FrameMS:        doc.Summary.P50,
		P95FrameMS:        doc.Summary.P95,
		P99FrameMS:        doc.Summary.P99,
		GPUExecuteMS:      doc.Job.GPU.Execute,
		GPUReadbackMS:     doc.Job.GPU.Readback,
		GPUNodes:          doc.Job.GPU.Nodes,
		FallbackNodes:     doc.Job.GPU.Fallback,
		FallbackDrawNode:  doc.Job.GPU.FallbackDrawNode,
		FallbackTextRun:   doc.Job.GPU.FallbackTextRun,
		FallbackComposite: doc.Job.GPU.FallbackComposite,
		FallbackEffect:    doc.Job.GPU.FallbackEffect,
		FallbackBlur:      doc.Job.GPU.FallbackBlur,
		FallbackDOF:       doc.Job.GPU.FallbackDOF,
		EffectiveBackend:  doc.Job.GPU.EffectiveBackend,
		ConversionMS:      doc.Job.ConversionMS,
		Frames:            doc.FramesTotal,
	}
}

// startDaemon launches `chronon3d_cli daemon -s <socket> -a <assets>` with
// output redirected to a log file in the test dir.
func startDaemon(t *testing.T, binary, socketPath, assetsRoot, backend string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binary, "daemon", "-s", socketPath, "-a", assetsRoot,
		"--backend", backend)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	return cmd
}

// waitForSocket polls until the daemon's unix socket file exists.
func waitForSocket(t *testing.T, socketPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("daemon socket %s did not appear within %s (is the daemon command built?)", socketPath, timeout)
}
