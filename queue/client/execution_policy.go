package client

import (
	"fmt"
	"strings"
)

// ExecutionPolicy is the durable, three-state execution demand of one render
// job. It is the canonical definition: the queue stores it on the row, the
// worker resolves its backend from it, and every producer aliases this type
// instead of re-declaring the vocabulary.
//
// The three states exist because "GPU preferred" and "GPU mandatory" are
// different contracts. Before this field the only producer input was
// RequireGPU *bool, which the queue decoded and dropped, so a producer asking
// for a guaranteed GPU render could be silently served by the software lane.
type ExecutionPolicy string

const (
	// ExecutionPolicyGPUOnly (from require_gpu: true) demands the GPU lane. A
	// plan that cannot execute on the GPU — one the compiler marks as needing
	// the software encoder — fails closed instead of being downgraded.
	ExecutionPolicyGPUOnly ExecutionPolicy = "gpu_only"
	// ExecutionPolicyGPUPreferred (require_gpu omitted) is the historical
	// behaviour: the worker uses the GPU when it can and may fall back to the
	// software lane when its own configuration permits it.
	ExecutionPolicyGPUPreferred ExecutionPolicy = "gpu_preferred"
	// ExecutionPolicyCPUOnly (require_gpu: false) releases the GPU demand: the
	// job is routable on a software-only lane and never forces GPU execution.
	ExecutionPolicyCPUOnly ExecutionPolicy = "cpu_only"
)

// CapabilityGPU is the capability a worker advertises when it can execute the
// GPU lane. The claim path requires it from the worker inventory before it
// hands out an ExecutionPolicyGPUOnly job: a worker that cannot prove a GPU
// must not claim GPU-only work, and an inventory that predates the capability
// is not proof.
const CapabilityGPU = "gpu"

// AllExecutionPolicies returns the canonical policy vocabulary. It is the list
// the render_jobs_execution_policy_check SQL constraint must match.
func AllExecutionPolicies() []ExecutionPolicy {
	return []ExecutionPolicy{
		ExecutionPolicyGPUOnly,
		ExecutionPolicyGPUPreferred,
		ExecutionPolicyCPUOnly,
	}
}

// NormalizeExecutionPolicy validates one policy value. The empty string is the
// historical omission and means ExecutionPolicyGPUPreferred, which is also the
// column default, so a job submitted by a producer that predates the field
// keeps its exact historical behaviour.
func NormalizeExecutionPolicy(policy ExecutionPolicy) (ExecutionPolicy, error) {
	trimmed := ExecutionPolicy(strings.TrimSpace(string(policy)))
	if trimmed == "" {
		return ExecutionPolicyGPUPreferred, nil
	}
	for _, candidate := range AllExecutionPolicies() {
		if trimmed == candidate {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("unknown execution_policy %q (want one of %s)", policy, joinPolicies(AllExecutionPolicies()))
}

// DeriveExecutionPolicy maps the producer's tri-state RequireGPU input onto the
// stored policy. It is the only place the legacy boolean is interpreted, so the
// queue, the worker and the docs cannot disagree about what an omission means.
func DeriveExecutionPolicy(requireGPU *bool) ExecutionPolicy {
	if requireGPU == nil {
		return ExecutionPolicyGPUPreferred
	}
	if *requireGPU {
		return ExecutionPolicyGPUOnly
	}
	return ExecutionPolicyCPUOnly
}

// ResolveExecutionPolicy is the ingress rule for a submit body that may carry
// both spellings. An omitted policy derives from RequireGPU; an explicit
// policy must agree with an explicit RequireGPU and fails closed when it does
// not, because guessing a winner would let a producer's two declarations mean
// two different renders.
func ResolveExecutionPolicy(requireGPU *bool, explicit ExecutionPolicy) (ExecutionPolicy, error) {
	if strings.TrimSpace(string(explicit)) == "" {
		return DeriveExecutionPolicy(requireGPU), nil
	}
	normalized, err := NormalizeExecutionPolicy(explicit)
	if err != nil {
		return "", err
	}
	if requireGPU != nil {
		if derived := DeriveExecutionPolicy(requireGPU); derived != normalized {
			return "", fmt.Errorf("execution_policy %q contradicts require_gpu %t (want %q)", normalized, *requireGPU, derived)
		}
	}
	return normalized, nil
}

// RequiresGPU reports whether the policy forbids the software lane.
func (p ExecutionPolicy) RequiresGPU() bool { return p == ExecutionPolicyGPUOnly }

func joinPolicies(policies []ExecutionPolicy) string {
	parts := make([]string, len(policies))
	for i, p := range policies {
		parts[i] = string(p)
	}
	return strings.Join(parts, ", ")
}

// HasCapability reports whether the inventory names capability. Names are
// compared trimmed and case-insensitively so a worker that registers " GPU"
// is not mistaken for a worker without one.
func HasCapability(capabilities []string, capability string) bool {
	want := strings.TrimSpace(capability)
	if want == "" {
		return false
	}
	for _, advertised := range capabilities {
		if strings.EqualFold(strings.TrimSpace(advertised), want) {
			return true
		}
	}
	return false
}
