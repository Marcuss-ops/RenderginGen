package client

import "testing"

func boolPtr(v bool) *bool { return &v }

// TestDeriveExecutionPolicy pins the one interpretation of the legacy
// tri-state RequireGPU input: omission means the historical GPU-preferred
// behaviour, true demands the GPU lane and false releases the demand.
func TestDeriveExecutionPolicy(t *testing.T) {
	cases := []struct {
		name     string
		require  *bool
		expected ExecutionPolicy
	}{
		{name: "omitted", require: nil, expected: ExecutionPolicyGPUPreferred},
		{name: "true", require: boolPtr(true), expected: ExecutionPolicyGPUOnly},
		{name: "false", require: boolPtr(false), expected: ExecutionPolicyCPUOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveExecutionPolicy(tc.require); got != tc.expected {
				t.Fatalf("DeriveExecutionPolicy(%v) = %q, want %q", tc.require, got, tc.expected)
			}
		})
	}
}

// TestResolveExecutionPolicyRejectsContradiction certifies the fail-closed
// ingress rule: when a body carries both spellings and they disagree, the
// queue refuses it instead of picking a winner. A caller that meant GPU-only
// must never be served by a job stored as cpu_only.
func TestResolveExecutionPolicyRejectsContradiction(t *testing.T) {
	if _, err := ResolveExecutionPolicy(boolPtr(true), ExecutionPolicyCPUOnly); err == nil {
		t.Fatal("require_gpu=true with execution_policy=cpu_only was accepted")
	}
	if _, err := ResolveExecutionPolicy(boolPtr(false), ExecutionPolicyGPUOnly); err == nil {
		t.Fatal("require_gpu=false with execution_policy=gpu_only was accepted")
	}
}

// TestResolveExecutionPolicyAcceptsAgreementAndExplicitPolicy covers the two
// non-contradictory spellings: an explicit policy that agrees with RequireGPU,
// and a policy-only body from a producer that never learned the boolean.
func TestResolveExecutionPolicyAcceptsAgreementAndExplicitPolicy(t *testing.T) {
	got, err := ResolveExecutionPolicy(boolPtr(true), ExecutionPolicyGPUOnly)
	if err != nil || got != ExecutionPolicyGPUOnly {
		t.Fatalf("agreeing spellings = (%q, %v), want (gpu_only, nil)", got, err)
	}
	got, err = ResolveExecutionPolicy(nil, ExecutionPolicyCPUOnly)
	if err != nil || got != ExecutionPolicyCPUOnly {
		t.Fatalf("policy-only body = (%q, %v), want (cpu_only, nil)", got, err)
	}
	got, err = ResolveExecutionPolicy(nil, "")
	if err != nil || got != ExecutionPolicyGPUPreferred {
		t.Fatalf("empty body = (%q, %v), want (gpu_preferred, nil)", got, err)
	}
	if _, err := NormalizeExecutionPolicy("gpu"); err == nil {
		t.Fatal("unknown execution_policy \"gpu\" was accepted")
	}
}

// TestHasCapabilityTrimsAndFoldsCase keeps the claim filter tolerant of the
// registration spelling without accepting an empty capability, which would
// otherwise match every inventory.
func TestHasCapabilityTrimsAndFoldsCase(t *testing.T) {
	if !HasCapability([]string{"gpu_effect:motion", " GPU "}, CapabilityGPU) {
		t.Fatal("capability inventory naming \" GPU \" was not recognised")
	}
	if HasCapability([]string{"gpu_effect:motion"}, CapabilityGPU) {
		t.Fatal("a GPU-effect capability was mistaken for the GPU lane")
	}
	if HasCapability([]string{""}, "") {
		t.Fatal("empty capability matched")
	}
}
