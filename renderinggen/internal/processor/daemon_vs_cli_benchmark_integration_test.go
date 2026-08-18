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
			Backend:    "software",
		}); err != nil {
			t.Fatalf("cli %s: %v", phase, err)
		}
		record("cli", phase, start)
	}

	// ── Daemon: start on a unix socket, cold + warm, then shutdown ───────
	socketPath := filepath.Join(t.TempDir(), "chronon.sock")
	daemon := startDaemon(t, cli.Binary(), socketPath, ws.Root())
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
			Backend:    "software",
		}); err != nil {
			t.Fatalf("daemon %s: %v", phase, err)
		}
		record("daemon", phase, start)
	}

	// ── Report + delta ───────────────────────────────────────────────────
	by := map[string]float64{}
	for _, r := range results {
		by[r.Backend+"-"+r.Phase] = r.TotalMS
	}
	fmt.Printf("\n=== RenderingGen daemon vs CLI benchmark (GoldenOverlayJobV1) ===\n")
	fmt.Printf("%-9s %-6s %10s\n", "backend", "phase", "total_ms")
	for _, r := range results {
		fmt.Printf("%-9s %-6s %10.1f\n", r.Backend, r.Phase, r.TotalMS)
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

// startDaemon launches `chronon3d_cli daemon -s <socket> -a <assets>` with
// output redirected to a log file in the test dir.
func startDaemon(t *testing.T, binary, socketPath, assetsRoot string) *exec.Cmd {
	t.Helper()
	logFile, err := os.Create(filepath.Join(t.TempDir(), "daemon.log"))
	if err != nil {
		t.Fatalf("daemon log: %v", err)
	}
	cmd := exec.Command(binary, "daemon", "-s", socketPath, "-a", assetsRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
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
