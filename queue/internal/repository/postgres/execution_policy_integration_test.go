package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestClaimEnforcesExecutionPolicyOnPostgres certifies the durable half of the
// three-state policy: migration 032 stores execution_policy, the claim query
// hands a gpu_only row only to a worker whose registered inventory names the
// GPU lane, and the claim response echoes the stored policy. The in-memory
// backend has the same test, so the two backends cannot disagree about who may
// run a GPU-demanding render.
func TestClaimEnforcesExecutionPolicyOnPostgres(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	if err := r.Submit(ctx, model.Job{ID: "policy-gpu-only", RequireGPU: boolPtr(true)}); err != nil {
		t.Fatalf("Submit(gpu_only): %v", err)
	}
	stored, err := r.Get(ctx, "policy-gpu-only")
	if err != nil {
		t.Fatalf("Get(gpu_only): %v", err)
	}
	if stored.ExecutionPolicy != model.ExecutionPolicyGPUOnly {
		t.Fatalf("stored execution_policy = %q, want gpu_only", stored.ExecutionPolicy)
	}

	if err := r.Register(ctx, model.Worker{ID: "policy-cpu-worker", Capabilities: []string{"gpu_effect:motion"}}); err != nil {
		t.Fatalf("Register(cpu worker): %v", err)
	}
	job, _, err := r.Claim(ctx, "policy-cpu-worker")
	if err != nil {
		t.Fatalf("Claim(cpu worker): %v", err)
	}
	if job != nil {
		t.Fatalf("gpu_only job claimed by a worker without the GPU capability: %+v", job)
	}
	stillPending, err := r.Get(ctx, "policy-gpu-only")
	if err != nil {
		t.Fatalf("Get after rejected claim: %v", err)
	}
	if stillPending.State != model.StatePending {
		t.Fatalf("gpu_only job state after a rejected claim = %s, want pending", stillPending.State)
	}

	if err := r.Register(ctx, model.Worker{ID: "policy-gpu-worker", Capabilities: []string{client.CapabilityGPU}}); err != nil {
		t.Fatalf("Register(gpu worker): %v", err)
	}
	job, _, err = r.Claim(ctx, "policy-gpu-worker")
	if err != nil {
		t.Fatalf("Claim(gpu worker): %v", err)
	}
	if job == nil || job.ID != "policy-gpu-only" {
		t.Fatalf("gpu_only job not claimed by the GPU worker: %+v", job)
	}
	if job.ExecutionPolicy != model.ExecutionPolicyGPUOnly {
		t.Fatalf("claim response execution_policy = %q, want gpu_only", job.ExecutionPolicy)
	}
}

// TestClaimAcceptsPreferredAndCPUOnlyOnPostgres certifies the gate is a
// GPU-only rule: the other two policies stay claimable by a software worker,
// and the defaulted row reads back as gpu_preferred rather than empty.
func TestClaimAcceptsPreferredAndCPUOnlyOnPostgres(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	if err := r.Submit(ctx, model.Job{ID: "policy-preferred"}); err != nil {
		t.Fatalf("Submit(preferred): %v", err)
	}
	if err := r.Submit(ctx, model.Job{ID: "policy-cpu-only", RequireGPU: boolPtr(false)}); err != nil {
		t.Fatalf("Submit(cpu_only): %v", err)
	}

	for _, want := range []string{"policy-preferred", "policy-cpu-only"} {
		job, _, err := r.Claim(ctx, "policy-software-worker")
		if err != nil {
			t.Fatalf("Claim(%s): %v", want, err)
		}
		if job == nil || job.ID != want {
			t.Fatalf("claim order: got %+v, want %s", job, want)
		}
		if job.ExecutionPolicy == "" {
			t.Fatalf("claim of %s returned an empty execution_policy", want)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
