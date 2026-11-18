package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

func sessionPolicy(t *testing.T, plan *overlay.Plan, cfg ProcessorConfig) RenderExecutionPolicy {
	t.Helper()
	policy, err := ResolveExecutionPolicy(&queue.Job{ID: "session"}, plan, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestExecuteRenderSessionSuccess(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "caption", Type: "text", Text: "x"})
	policy := sessionPolicy(t, plan, ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc"})
	prepared := &PreparedJob{Job: &queue.Job{ID: "session"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "out.mp4"), Metrics: map[string]float64{}}
	outcome, err := proc.ExecuteRenderSession(context.Background(), prepared, chronon.RenderRequest{OutputPath: prepared.OutputPath}, policy)
	if err != nil || outcome.SoftwareFallback {
		t.Fatalf("outcome = %+v, err=%v, want a native success", outcome, err)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1", renderer.calls)
	}
}

func TestExecuteRenderSessionPublishesSoftwareFallback(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
	policy := sessionPolicy(t, plan, ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc"})
	prepared := &PreparedJob{Job: &queue.Job{ID: "session"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "out.mp4"), Metrics: map[string]float64{}}
	renderer.errs = []error{errors.New("Vulkan device lost"), nil}
	renderer.write = func(path string) error { return os.WriteFile(path, []byte("software"), 0o600) }
	outcome, err := proc.ExecuteRenderSession(context.Background(), prepared, chronon.RenderRequest{OutputPath: prepared.OutputPath}, policy)
	if err != nil || !outcome.SoftwareFallback {
		t.Fatalf("outcome = %+v, err=%v, want a published fallback", outcome, err)
	}
	if data, err := os.ReadFile(prepared.OutputPath); err != nil || string(data) != "software" {
		t.Fatalf("fallback output = %q, err=%v", data, err)
	}
	if prepared.Metrics["chronon_software_fallback"] != 1 {
		t.Fatalf("fallback metric = %v", prepared.Metrics["chronon_software_fallback"])
	}
}

func TestExecuteRenderSessionDoesNotRetryWithoutGPURequirement(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	plan := policyTestPlan()
	policy := sessionPolicy(t, plan, ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc"})
	if policy.GPURequired || policy.SoftwareRetryAllowed {
		t.Fatalf("policy = %+v, want a non-GPU job without a software retry", policy)
	}
	prepared := &PreparedJob{Job: &queue.Job{ID: "session"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "out.mp4"), Metrics: map[string]float64{}}
	renderer.err = errors.New("Vulkan device lost")
	if _, err := proc.ExecuteRenderSession(context.Background(), prepared, chronon.RenderRequest{OutputPath: prepared.OutputPath}, policy); err == nil {
		t.Fatal("session succeeded on a non-GPU job the worker must not retry")
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want only the native attempt", renderer.calls)
	}
}

func TestExecuteRenderSessionRefusesFallbackWhenPolicyForbidsIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ProcessorConfig
	}{
		{"strict", ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", StrictNativeBackend: true}},
		{"segmented", ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", Segmented: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc, _, renderer := newProcessor(t)
			plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
			policy := sessionPolicy(t, plan, tc.cfg)
			prepared := &PreparedJob{Job: &queue.Job{ID: "session"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "out.mp4"), Metrics: map[string]float64{}}
			renderer.err = errors.New("Vulkan device lost")
			if _, err := proc.ExecuteRenderSession(context.Background(), prepared, chronon.RenderRequest{OutputPath: prepared.OutputPath}, policy); err == nil {
				t.Fatal("policy forbids the retry but the session succeeded")
			}
			if renderer.calls != 1 {
				t.Fatalf("renderer calls = %d, want only the native attempt", renderer.calls)
			}
		})
	}
}

func TestCertifyAndHandoffCommitsTailAfterNativeGate(t *testing.T) {
	proc, _, _ := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "caption", Type: "text", Text: "x"})
	policy := sessionPolicy(t, plan, ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc"})
	prepared := &PreparedJob{Job: &queue.Job{ID: "handoff"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "out.mp4"), Metrics: map[string]float64{}}
	observer := newRenderProgressObserver(prepared.Job, nil, "vulkan", "nvenc")
	tail := &stagedUpload{}
	ownership, err := proc.CertifyAndHandoff(prepared, policy, RenderSessionOutcome{Elapsed: 1}, observer, tail)
	if err != nil {
		t.Fatal(err)
	}
	if !ownership.Commit || ownership.Upload != tail {
		t.Fatalf("ownership = %+v, want the tail committed", ownership)
	}
}

func TestCertifyAndHandoffFailsNativeGateWithoutCommittingTail(t *testing.T) {
	proc, _, _ := newProcessor(t)
	plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
	policy := sessionPolicy(t, plan, ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", StrictNativeBackend: true})
	prepared := &PreparedJob{Job: &queue.Job{ID: "gate"}, Plan: plan, OutputPath: filepath.Join(t.TempDir(), "missing.mp4"), Metrics: map[string]float64{}}
	observer := newRenderProgressObserver(prepared.Job, nil, "vulkan", "nvenc")
	ownership, err := proc.CertifyAndHandoff(prepared, policy, RenderSessionOutcome{Elapsed: 1}, observer, &stagedUpload{})
	if err == nil {
		t.Fatal("strict native gate must reject a missing telemetry document")
	}
	if ownership.Commit {
		t.Fatalf("ownership = %+v, a failed gate must not commit the tail", ownership)
	}
}
