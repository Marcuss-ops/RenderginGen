package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestClaimEnforcesExecutionPolicy pins the claim gate of the in-memory
// backend, which must agree with the postgres predicate: a gpu_only job is
// invisible to a worker that cannot prove a GPU lane from its registered
// inventory, and visible to one that can. Claiming it without proof would
// silently hand a GPU-demanding render to the software lane.
func TestClaimEnforcesExecutionPolicy(t *testing.T) {
	repo := New(time.Minute, 3)
	ctx := context.Background()

	if err := repo.Submit(ctx, model.Job{ID: "gpu-only", RequireGPU: boolPtr(true)}); err != nil {
		t.Fatalf("Submit(gpu-only): %v", err)
	}

	// The worker is registered without the GPU capability: it is not proof.
	if err := repo.Register(ctx, model.Worker{ID: "cpu-worker", Capabilities: []string{"gpu_effect:motion"}}); err != nil {
		t.Fatalf("Register(cpu-worker): %v", err)
	}
	job, _, err := repo.Claim(ctx, "cpu-worker")
	if err != nil {
		t.Fatalf("Claim(cpu-worker): %v", err)
	}
	if job != nil {
		t.Fatalf("gpu_only job claimed by a worker without the GPU capability: %+v", job)
	}
	stored, err := repo.Get(ctx, "gpu-only")
	if err != nil {
		t.Fatalf("Get(gpu-only): %v", err)
	}
	if stored.State != model.StatePending {
		t.Fatalf("gpu_only job state = %s, want pending (still queued for a capable worker)", stored.State)
	}

	// A worker that advertises the capability claims it, and the claim echoes
	// the durable policy so the worker never reinterprets require_gpu.
	if err := repo.Register(ctx, model.Worker{ID: "gpu-worker", Capabilities: []string{client.CapabilityGPU}}); err != nil {
		t.Fatalf("Register(gpu-worker): %v", err)
	}
	job, _, err = repo.Claim(ctx, "gpu-worker")
	if err != nil {
		t.Fatalf("Claim(gpu-worker): %v", err)
	}
	if job == nil || job.ID != "gpu-only" {
		t.Fatalf("gpu_only job not claimed by the GPU worker: %+v", job)
	}
	if job.ExecutionPolicy != model.ExecutionPolicyGPUOnly {
		t.Fatalf("claimed job execution_policy = %q, want gpu_only", job.ExecutionPolicy)
	}
}

// TestClaimAcceptsPreferredAndCPUOnlyEverywhere certifies the other two
// policies stay claimable by any worker: the gate is a GPU-only rule, not a
// general capability requirement.
func TestClaimAcceptsPreferredAndCPUOnlyEverywhere(t *testing.T) {
	repo := New(time.Minute, 3)
	ctx := context.Background()

	if err := repo.Submit(ctx, model.Job{ID: "preferred"}); err != nil {
		t.Fatalf("Submit(preferred): %v", err)
	}
	if err := repo.Submit(ctx, model.Job{ID: "cpu-only", RequireGPU: boolPtr(false)}); err != nil {
		t.Fatalf("Submit(cpu-only): %v", err)
	}

	for _, want := range []string{"preferred", "cpu-only"} {
		job, _, err := repo.Claim(ctx, "any-worker")
		if err != nil {
			t.Fatalf("Claim(%s): %v", want, err)
		}
		if job == nil || job.ID != want {
			t.Fatalf("claim order: got %+v, want %s", job, want)
		}
	}
}

// TestSubmitResolvesExecutionPolicyOnce certifies both submit paths store the
// durable policy instead of leaving the column empty for the claim projection
// to guess later.
func TestSubmitResolvesExecutionPolicyOnce(t *testing.T) {
	repo := New(time.Minute, 3)
	ctx := context.Background()

	for _, tc := range []struct {
		id     string
		job    model.Job
		expect model.ExecutionPolicy
	}{
		{id: "derived", job: model.Job{RequireGPU: boolPtr(true)}, expect: model.ExecutionPolicyGPUOnly},
		{id: "omitted", job: model.Job{}, expect: model.ExecutionPolicyGPUPreferred},
		{id: "explicit", job: model.Job{ExecutionPolicy: model.ExecutionPolicyCPUOnly}, expect: model.ExecutionPolicyCPUOnly},
	} {
		job := tc.job
		job.ID = tc.id
		if err := repo.Submit(ctx, job); err != nil {
			t.Fatalf("Submit(%s): %v", tc.id, err)
		}
		stored, err := repo.Get(ctx, tc.id)
		if err != nil {
			t.Fatalf("Get(%s): %v", tc.id, err)
		}
		if stored.ExecutionPolicy != tc.expect {
			t.Fatalf("stored execution_policy for %s = %q, want %q", tc.id, stored.ExecutionPolicy, tc.expect)
		}
	}

	contradiction := model.Job{ID: "contradiction", RequireGPU: boolPtr(true), ExecutionPolicy: model.ExecutionPolicyCPUOnly}
	if err := repo.Submit(ctx, contradiction); err == nil {
		t.Fatal("a contradictory execution_policy was stored instead of rejected")
	}
}

func boolPtr(v bool) *bool { return &v }
