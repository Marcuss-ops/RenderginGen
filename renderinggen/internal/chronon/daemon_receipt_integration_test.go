package chronon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDaemonRenderEmitsMediaReceipt certifies the RECEIPT boundary of the WARM
// DAEMON transport, which is the deployed hot path (worker config
// chronon.mode=ipc). The CLI-transport test covers the same guarded code
// through a different entry point; this one proves the deployed wiring: the
// worker's IPC payload asks for a report, the daemon renders, and the output
// carries the receipt the worker's native gate requires before publishing.
//
// The daemon is started by the test on a private socket base, from the binary
// under certification — no operator unit is touched, so a candidate build can
// be certified before it replaces the running daemon.
//
// Gated like the other live tests:
//
//	CHRONON_HOME            install/build prefix (default /opt/chronon3d)
//	CHRONON_BINARY          explicit chronon3d_cli path (highest priority)
//	CHRONON_DAEMON_BACKEND  daemon backend (default vulkan)
//
// Without a usable binary the test skips visibly.
func TestDaemonRenderEmitsMediaReceipt(t *testing.T) {
	binary := daemonBinary(t)
	backend := os.Getenv("CHRONON_DAEMON_BACKEND")
	if backend == "" {
		backend = "vulkan"
	}
	planPath, assetsRoot, outputPath := writeColorSmokePlan(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	base := filepath.Join(t.TempDir(), "chronon.sock")
	pool, err := StartDaemonPool(ctx, DaemonOptions{
		SocketBase:     base,
		Count:          1,
		LanesPerDaemon: 1,
		Binary:         binary,
		AssetsRoot:     assetsRoot,
		Backend:        backend,
		GPUDevice:      0,
		Stdout:         os.Stderr,
		Stderr:         os.Stderr,
	})
	if err != nil {
		t.Fatalf("start daemon pool: %v", err)
	}
	// Shutdown is idempotent and must run even on the failure path: a daemon
	// left behind would hold the GPU device for the next run.
	defer pool.Shutdown(context.Background())
	if pool.OwnedCount() != 1 {
		t.Fatalf("pool owned %d daemons, want 1 (a socket that was already served would make this test certify someone else's daemon)", pool.OwnedCount())
	}

	if err := pool.Render(ctx, RenderRequest{
		PlanPath:      planPath,
		AssetsRoot:    assetsRoot,
		OutputPath:    outputPath,
		Report:        true,
		ReceiptVerify: "fast",
		Requirements:  ExecutionRequirements{Backend: backend},
		TotalFrames:   2,
	}); err != nil {
		t.Fatalf("daemon render: %v", err)
	}

	assertMediaReceiptCertifies(t, outputPath, "fast")
}

// daemonBinary resolves the chronon3d_cli the daemon pool must execute. The
// pool needs a path up front (unlike the CLI client, which can resolve it
// lazily), so this is the single resolution the daemon transport uses.
func daemonBinary(t *testing.T) string {
	t.Helper()
	if override := os.Getenv("CHRONON_BINARY"); override != "" {
		return override
	}
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	binary := (&Client{Home: home}).Binary()
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("chronon3d_cli not available at %s (set CHRONON_HOME or CHRONON_BINARY): %v", binary, err)
	}
	return binary
}
