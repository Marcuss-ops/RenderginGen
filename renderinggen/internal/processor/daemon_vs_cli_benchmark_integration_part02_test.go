package processor

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

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
		RenderMS:                    doc.RenderMS,
		WallMS:                      doc.WallMS,
		EncodeCloseMS:               doc.EncodeClose,
		P50FrameMS:                  doc.Summary.P50,
		P95FrameMS:                  doc.Summary.P95,
		P99FrameMS:                  doc.Summary.P99,
		GPUExecuteMS:                doc.Job.GPU.Execute,
		GPUReadbackMS:               doc.Job.GPU.Readback,
		GPUNodes:                    doc.Job.GPU.Nodes,
		FallbackNodes:               doc.Job.GPU.Fallback,
		FallbackDrawNode:            doc.Job.GPU.FallbackDrawNode,
		FallbackDrawImage:           doc.Job.GPU.FallbackDrawImage,
		FallbackDrawOther:           doc.Job.GPU.FallbackDrawOther,
		FallbackTextRun:             doc.Job.GPU.FallbackTextRun,
		FallbackComposite:           doc.Job.GPU.FallbackComposite,
		FallbackCompositeDimensions: doc.Job.GPU.FallbackCompositeDimensions,
		FallbackCompositeMode:       doc.Job.GPU.FallbackCompositeMode,
		FallbackEffect:              doc.Job.GPU.FallbackEffect,
		FallbackBlur:                doc.Job.GPU.FallbackBlur,
		FallbackDOF:                 doc.Job.GPU.FallbackDOF,
		EffectiveBackend:            doc.Job.GPU.EffectiveBackend,
		ConversionMS:                doc.Job.ConversionMS,
		Frames:                      doc.FramesTotal,
	}
}

// startDaemon launches `chronon3d_cli daemon -s <socket> -a <assets>` with
// output redirected to a log file in the test dir.
func startDaemon(t *testing.T, binary, socketPath, assetsRoot, backend string) *exec.Cmd {
	t.Helper()
	args := []string{"daemon", "-s", socketPath, "-a", assetsRoot,
		"--backend", backend}
	if level := os.Getenv("CHRONON_DAEMON_LOG_LEVEL"); level != "" {
		args = append(args, "--log-level", level)
	}
	cmd := exec.Command(binary, args...)
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
