package processor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// TestExecuteRenderSessionGPUOnlyFailsClosedWhenTheDeviceDisappears pins the
// post-claim half of gpu_only: the job was admitted to a GPU worker, the device
// is lost during the render, and the session must not silently rerun on the
// software lane. The renderer is called exactly once (the native attempt); a
// fallback attempt would make it two.
func TestExecuteRenderSessionGPUOnlyFailsClosedWhenTheDeviceDisappears(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
	job := policyTestJob("gpu-only-device-loss")
	job.ExecutionPolicy = queue.ExecutionPolicyGPUOnly
	policy, err := ResolveExecutionPolicy(job, plan, ProcessorConfig{
		Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.GPURequired || policy.SoftwareRetryAllowed || policy.CPUFallbackAllowed {
		t.Fatalf("gpu_only policy = %+v, want no fallback and no retry", policy)
	}

	renderer.err = errors.New("Vulkan device lost after the job was claimed")
	prepared := &PreparedJob{
		Job: job, Plan: plan,
		OutputPath: filepath.Join(t.TempDir(), "out.mp4"),
		Metrics:    map[string]float64{},
	}
	if _, err := proc.ExecuteRenderSession(context.Background(), prepared, chronon.RenderRequest{OutputPath: prepared.OutputPath}, policy); err == nil {
		t.Fatal("gpu_only session succeeded after the device was lost")
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want only the native attempt (no software downgrade)", renderer.calls)
	}
}

// TestExecuteRenderSessionRecoversAfterADaemonReset pins the retry shape the
// queue relies on: a failed attempt leaves no session state behind, so the
// next job after the daemon restarts renders normally.
func TestExecuteRenderSessionRecoversAfterADaemonReset(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
	policy, err := ResolveExecutionPolicy(policyTestJob("reset"), plan, ProcessorConfig{
		Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native",
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.errs = []error{errors.New("chronon daemon reset (socket closed)"), nil}

	first := &PreparedJob{Job: policyTestJob("before-reset"), Plan: plan,
		OutputPath: filepath.Join(t.TempDir(), "before.mp4"), Metrics: map[string]float64{}}
	if _, err := proc.ExecuteRenderSession(context.Background(), first, chronon.RenderRequest{OutputPath: first.OutputPath}, policy); err == nil {
		t.Fatal("the session before the reset unexpectedly succeeded")
	}

	second := &PreparedJob{Job: policyTestJob("after-reset"), Plan: plan,
		OutputPath: filepath.Join(t.TempDir(), "after.mp4"), Metrics: map[string]float64{}}
	if _, err := proc.ExecuteRenderSession(context.Background(), second, chronon.RenderRequest{OutputPath: second.OutputPath}, policy); err != nil {
		t.Fatalf("the first session after the daemon reset failed: %v", err)
	}
	if renderer.calls != 2 {
		t.Fatalf("renderer calls = %d, want one failed attempt and one recovered session", renderer.calls)
	}
}
