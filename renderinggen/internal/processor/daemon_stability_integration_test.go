package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

// TestDaemonVulkanStabilityRecovery keeps one Vulkan daemon alive across a
// deliberate bad-plan recovery and a configurable number of valid jobs. It is
// opt-in because the default 100-job run is a production certification gate,
// not a unit test. Run with CHRONON_RUN_STABILITY=1.
func TestDaemonVulkanStabilityRecovery(t *testing.T) {
	if os.Getenv("CHRONON_RUN_STABILITY") != "1" {
		t.Skip("set CHRONON_RUN_STABILITY=1 for the Production V1 daemon gate")
	}

	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	jobs := 100
	if raw := os.Getenv("CHRONON_STABILITY_JOBS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			t.Fatalf("CHRONON_STABILITY_JOBS must be a positive integer: %q", raw)
		}
		jobs = value
	}

	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenOverlayJobV1), &job); err != nil {
		t.Fatalf("decode golden job: %v", err)
	}

	workspaceRoot, err := os.MkdirTemp("", "chronon-stability-")
	if err != nil {
		t.Fatalf("workspace temp dir: %v", err)
	}
	if os.Getenv("CHRONON_STABILITY_KEEP") != "1" {
		t.Cleanup(func() { _ = os.RemoveAll(workspaceRoot) })
	} else {
		t.Logf("keeping stability workspace: %s", workspaceRoot)
	}
	ws, err := workspace.New(workspaceRoot, "daemon-stability-v1")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if os.Getenv("CHRONON_STABILITY_KEEP") != "1" {
		defer ws.Cleanup()
	}
	store := storage.New(storage.NewMemory(), storage.Options{})
	seedGoldenAssets(t, store, job.Assets)
	if err := ws.Materialize(context.Background(), store.Get, job.Assets); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if err := ws.WritePlan(job.RenderPlan); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "chronon.sock")
	daemon := startDaemon(t, cli.Binary(), socketPath, ws.Root(), "vulkan")
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = chronon.NewIPCClient(socketPath).Shutdown(shutdownCtx)
		if daemon.Process != nil {
			_ = daemon.Process.Kill()
		}
	})
	waitForSocket(t, socketPath, 30*time.Second)
	client := chronon.NewIPCClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	request := chronon.RenderRequest{
		PlanPath: ws.PlanPath(), AssetsRoot: ws.Root(),
		Backend: "vulkan", Report: true,
	}

	// Recovery gate: a malformed plan must fail without poisoning the daemon.
	if os.Getenv("CHRONON_STABILITY_SKIP_BAD") != "1" {
		if err := os.WriteFile(ws.PlanPath(), []byte("{"), 0o600); err != nil {
			t.Fatalf("write malformed plan: %v", err)
		}
		if err := client.Render(ctx, request); err == nil {
			t.Fatal("malformed plan unexpectedly rendered successfully")
		}
		if err := ws.WritePlan(job.RenderPlan); err != nil {
			t.Fatalf("restore valid plan: %v", err)
		}
	}
	// The first valid render after a failed request is an intentional runtime
	// warm-up: it rebuilds per-session graph/frame state while keeping the
	// daemon alive. Certify determinism on the steady-state sequence below.
	request.OutputPath = ws.OutputPath("warmup.mp4")
	if err := client.Render(ctx, request); err != nil {
		t.Fatalf("warm-up valid job after recovery: %v", err)
	}

	var firstFrameDigest string
	for i := 0; i < jobs; i++ {
		request.OutputPath = ws.OutputPath(fmt.Sprintf("result-%03d.mp4", i))
		if err := client.Render(ctx, request); err != nil {
			t.Fatalf("valid job %d/%d after recovery: %v", i+1, jobs, err)
		}
		timing := readChrononTiming(t, request.OutputPath)
		if timing.EffectiveBackend != "vulkan" {
			t.Fatalf("job %d/%d effective_backend=%q, want vulkan", i+1, jobs, timing.EffectiveBackend)
		}
		if timing.FallbackNodes == nil || *timing.FallbackNodes != 0 {
			t.Fatalf("job %d/%d software_fallback_nodes=%s, want 0", i+1, jobs, optionalIntMetric(timing.FallbackNodes))
		}
		got := frameDigest(t, request.OutputPath)
		if i == 0 {
			firstFrameDigest = got
		} else if got != firstFrameDigest {
			t.Fatalf("job %d/%d decoded frame digest changed: first=%s current=%s", i+1, jobs, firstFrameDigest, got)
		}
	}

	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("daemon status after %d jobs: %v", jobs, err)
	}
	t.Logf("Production V1 daemon stability PASS: jobs=%d frame_digest=%s status=%s", jobs, firstFrameDigest, status)
}

func frameDigest(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-map", "0:v:0", "-f", "framemd5", "-")
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode %s with ffmpeg framemd5: %v", path, err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
