package processor

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// TestExecutionPolicyGPUOnlyRejectsASoftwareWorker is the worker-side admission
// half of the end-to-end policy: the queue refuses to hand a gpu_only job to a
// worker that does not advertise the GPU capability, and this is the second
// line of that defence. A worker configured for the software backend must fail
// closed on a gpu_only job instead of rendering it without a GPU — the silent
// downgrade the three-state contract exists to remove.
// TestJobExecutionPolicyNormalizesLegacyRows pins the worker-side reading of
// the durable field the log and the resolver share: an empty policy can only
// come from a row written before migration 032, and it means gpu_preferred —
// the historical behaviour, never "no policy".
func TestJobExecutionPolicyNormalizesLegacyRows(t *testing.T) {
	legacy := policyTestJob("legacy-row")
	if got := jobExecutionPolicy(legacy); got != queue.ExecutionPolicyGPUPreferred {
		t.Fatalf("empty execution_policy = %q, want gpu_preferred", got)
	}
	explicit := policyTestJob("explicit-row")
	explicit.ExecutionPolicy = queue.ExecutionPolicyCPUOnly
	if got := jobExecutionPolicy(explicit); got != queue.ExecutionPolicyCPUOnly {
		t.Fatalf("explicit execution_policy = %q, want cpu_only", got)
	}
	if got := jobExecutionPolicy(nil); got != queue.ExecutionPolicyGPUPreferred {
		t.Fatalf("nil job execution_policy = %q, want gpu_preferred", got)
	}
}

func TestExecutionPolicyGPUOnlyRejectsASoftwareWorker(t *testing.T) {
	plan := policyTestPlan(overlay.Layer{ID: "source", Type: "video"})
	cfg := ProcessorConfig{Backend: "software", HardwareEncoder: chronon.HardwareEncoderNone, EncoderBackend: "pipe"}
	job := policyTestJob("gpu-only-cpu-worker")
	job.ExecutionPolicy = queue.ExecutionPolicyGPUOnly
	_, err := ResolveExecutionPolicy(job, plan, cfg)
	if err == nil {
		t.Fatal("a software-configured worker accepted a gpu_only job")
	}
	if !strings.Contains(err.Error(), "software backend") {
		t.Fatalf("error %q does not name the refused lane", err)
	}
}
