package model

import (
	"strings"
	"testing"
)

// TestExecutionPolicyVocabularyMatchesSQLConstraint pins the durable policy
// vocabulary to the CHECK constraint added by migration 032, exactly like the
// job states and attempt statuses are pinned. A policy the Go side accepts but
// SQL rejects is a submit that fails at runtime with a constraint violation
// instead of at the boundary with a readable error; the reverse is a routing
// value no worker can interpret.
func TestExecutionPolicyVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 3)
	for _, policy := range AllExecutionPolicies() {
		want = append(want, string(policy))
	}
	got := latestCheck(t, "render_jobs_execution_policy_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("render_jobs_execution_policy_check %v != Go AllExecutionPolicies() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestDerivedExecutionPolicyPrefersStoredValue certifies that the claim path
// reads the stored policy when the column is present and only falls back to the
// legacy boolean for rows that predate it.
func TestDerivedExecutionPolicyPrefersStoredValue(t *testing.T) {
	requireGPU := true
	stored := ExecutionPolicyCPUOnly
	job := Job{RequireGPU: &requireGPU, ExecutionPolicy: stored}
	if got := DerivedExecutionPolicy(job); got != stored {
		t.Fatalf("DerivedExecutionPolicy = %q, want stored %q", got, stored)
	}

	job = Job{RequireGPU: &requireGPU}
	if got := DerivedExecutionPolicy(job); got != ExecutionPolicyGPUOnly {
		t.Fatalf("DerivedExecutionPolicy without a column value = %q, want gpu_only", got)
	}

	job = Job{}
	if got := DerivedExecutionPolicy(job); got != ExecutionPolicyGPUPreferred {
		t.Fatalf("DerivedExecutionPolicy of an empty job = %q, want gpu_preferred", got)
	}
}

// TestSameSubmitRequestComparesExecutionPolicy keeps the policy inside the
// immutable request identity: a retry that changes GPU demand is a different
// render, not a replay.
func TestSameSubmitRequestComparesExecutionPolicy(t *testing.T) {
	base := Job{ID: "j1", ExecutionPolicy: ExecutionPolicyGPUPreferred}
	other := Job{ID: "j1", ExecutionPolicy: ExecutionPolicyGPUOnly}
	if SameSubmitRequest(base, other) {
		t.Fatal("submits that disagree about execution_policy were treated as the same request")
	}
}
