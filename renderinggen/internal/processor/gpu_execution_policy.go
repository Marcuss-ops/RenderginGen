// gpu_execution_policy.go owns the immutable execution policy of one render.
//
// RunGPU used to derive the backend, encoder, frame range, fallback permission
// and native-gate applicability in several disconnected places. This type is
// the single value those decisions come from: the resolver computes it once,
// BuildChrononRequest projects it onto the wire and the session/certification
// stages read it instead of recombining local booleans.
package processor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// ErrExecutionPolicyIncompatible marks a job whose durable execution policy
// cannot be served for its plan. It is a typed sentinel so the rejection is
// distinguishable from an infrastructure failure: the job can never run as
// declared, and retrying it unchanged is pointless.
var ErrExecutionPolicyIncompatible = errors.New("execution_policy incompatible")

// FrameRange is the inclusive Chronon frame window of a chunk. Enabled is
// load-bearing: a chunk covering exactly frame 0 has First=Last=0 and is
// otherwise indistinguishable from "render the whole plan".
type FrameRange struct {
	First   int64
	Last    int64
	Enabled bool
}

// RenderExecutionPolicy is the complete, immutable execution identity of one
// render. Backend is the value requested from Chronon, not the renderer's
// physical decision; Chronon still classifies the compiled program.
type RenderExecutionPolicy struct {
	// Backend is forwarded as requirements.backend (auto | software | vulkan).
	Backend string
	// HardwareEncoder selects the native encoder (nvenc or none).
	HardwareEncoder string
	// EncoderBackend selects the encoder route (native or pipe).
	EncoderBackend string
	// EncodePreset is the resolved NVENC tier forwarded to the engine.
	EncodePreset string
	// GPURequired means Chronon must execute on a GPU; it is a requirement,
	// not a promise that the selected device can satisfy it.
	GPURequired bool
	// CPUFallbackAllowed permits Chronon's own software fallback on the wire.
	// The worker's separate retry permission is SoftwareRetryAllowed.
	CPUFallbackAllowed bool
	// SoftwareRetryAllowed permits ExecuteRenderSession to retry a recoverable
	// GPU failure through the software lane. It is stricter than the wire
	// fallback: the job must require a GPU and must not be strict-native or
	// segmented.
	SoftwareRetryAllowed bool
	// CompositionRequired marks a plan with an authored visual layer.
	CompositionRequired bool
	// VideoSourceRequired marks a plan whose source clip is video.
	VideoSourceRequired bool
	// FinalComposite identifies PipelineGen's per-scene final composite job.
	FinalComposite bool
	// Segmented marks the P4 one-output-per-item session. A segmented session
	// never retries through the single-output software lane.
	Segmented bool
	// StrictNative marks the gpu-vulkan-native worker profile.
	StrictNative bool
	// ParallelDisjoint is the trusted scheduler hint for a chunk child.
	ParallelDisjoint bool
	// ParentJobID is the chunk parent; it is kept so ParallelDisjoint can be
	// validated without re-reading the job.
	ParentJobID string
	// FrameRange is the explicit chunk window, if any.
	FrameRange FrameRange
}

// ProcessorConfig is the narrow configuration surface the policy resolver
// needs. It is deliberately separate from Options: the resolver must stay pure
// and cannot read store, logger or publication collaborators.
type ProcessorConfig struct {
	Backend             string
	HardwareEncoder     string
	EncoderBackend      string
	EncodePreset        string
	StrictNativeBackend bool
	// Segmented is a property of the prepared job (its per-item windows),
	// not of the plan document, so the caller supplies it to keep the
	// resolver pure.
	Segmented bool
}

// executionPolicyConfig projects the execution-relevant Processor fields.
func (p *Processor) executionPolicyConfig(prepared *PreparedJob) ProcessorConfig {
	if p == nil {
		return ProcessorConfig{}
	}
	return ProcessorConfig{
		Backend:             p.backend,
		HardwareEncoder:     p.hardwareEncoder,
		EncoderBackend:      p.encoderBackend,
		EncodePreset:        p.encodePreset,
		StrictNativeBackend: p.strictNativeBackend,
		Segmented:           prepared != nil && len(prepared.Segments) > 0,
	}
}

// ResolveExecutionPolicy derives the one policy for a job and plan. It is
// pure: no I/O, no logging, no goroutines and no mutation of plan or job.
func ResolveExecutionPolicy(job *queue.Job, plan *overlay.Plan, cfg ProcessorConfig) (RenderExecutionPolicy, error) {
	policy := RenderExecutionPolicy{
		Backend:         cfg.Backend,
		HardwareEncoder: cfg.HardwareEncoder,
		EncoderBackend:  cfg.EncoderBackend,
		StrictNative:    cfg.StrictNativeBackend,
		FinalComposite:  isFinalJobComposite(job),
		Segmented:       cfg.Segmented,
	}
	// A final composite is an intermediate scene render: it must execute the
	// complete Vulkan graph so layers starting after frame zero survive.
	if policy.FinalComposite {
		policy.Backend = "auto"
	}
	policy.EncodePreset = encodePresetForJob(cfg.EncodePreset, policy.FinalComposite)

	first, last, hasRange := jobFrameRange(job)
	policy.FrameRange = FrameRange{First: first, Last: last, Enabled: hasRange}
	policy.ParentJobID = ""
	if job != nil {
		policy.ParentJobID = job.ParentJobID
	}
	policy.ParallelDisjoint = job != nil && job.ParentJobID != "" && hasRange && job.ChunkIndex >= 0

	policy.VideoSourceRequired = planHasVideoSource(plan)
	policy.CompositionRequired = planHasVisualOverlay(plan)
	executionPolicy := jobExecutionPolicy(job)
	requiresSoftwareEncode := plan != nil && plan.Features.RequiresSoftwareEncode()
	gpuLaneRequested := (policy.VideoSourceRequired || policy.CompositionRequired) &&
		(cfg.StrictNativeBackend || chronon.StrictNativeRequired(cfg.Backend, cfg.HardwareEncoder))

	// The job's durable execution policy decides the lane. It is resolved by
	// the queue from the producer's require_gpu and returned on every claim,
	// so the worker never reinterprets an omitted boolean: the three states
	// below are the only answers.
	switch {
	case executionPolicy.RequiresGPU():
		// GPU mandatory, no CPU fallback and no software retry. A chain
		// camera-pan plan cannot execute on the native residency gate; the
		// historical behaviour silently downgraded it, which would break the
		// producer's guarantee, so a gpu_only job carrying such a plan fails
		// closed instead.
		if requiresSoftwareEncode {
			return RenderExecutionPolicy{}, fmt.Errorf("processor: %w: job %s demands gpu_only but the plan requires the software encoder; refusing to downgrade", ErrExecutionPolicyIncompatible, jobLabel(job))
		}
		// Defense in depth for the claim filter: a software-configured worker
		// must never serve a gpu_only job. That worker should not have claimed
		// it (it does not advertise the GPU capability), but failing closed
		// here turns a mis-advertised lane into an explicit error instead of a
		// render that silently ran without a GPU.
		if strings.EqualFold(cfg.Backend, "software") {
			return RenderExecutionPolicy{}, fmt.Errorf("processor: %w: job %s demands gpu_only but this worker is configured for the software backend; route it to a GPU worker", ErrExecutionPolicyIncompatible, jobLabel(job))
		}
		policy.StrictNative = true
		policy.GPURequired = true
		policy.CPUFallbackAllowed = false
		policy.SoftwareRetryAllowed = false
	case executionPolicy == queue.ExecutionPolicyCPUOnly:
		// The producer released the GPU demand: the job is routable on the
		// software lane and never forces a device. A final composite owns the
		// auto Vulkan contract, so it cannot be CPU-only.
		if policy.FinalComposite {
			return RenderExecutionPolicy{}, fmt.Errorf("processor: %w: job %s is a final composite and must run the full Vulkan graph; cpu_only is not valid for it", ErrExecutionPolicyIncompatible, jobLabel(job))
		}
		policy.Backend = "software"
		policy.HardwareEncoder = chronon.HardwareEncoderNone
		policy.EncoderBackend = "pipe"
		policy.EncodePreset = ""
		policy.StrictNative = false
		policy.GPURequired = false
		policy.CPUFallbackAllowed = !policy.Segmented
		policy.SoftwareRetryAllowed = false
	default:
		// gpu_preferred: the historical behaviour. A chain camera-pan keeps
		// overlapping animated full-canvas image surfaces, which the native
		// residency gate cannot materialize, so only those plans take the
		// software (libx264) lane and the rest of the worker keeps NVENC. The
		// overlay compiler states the requirement as a plan capability; the
		// policy never inspects individual layers. Final composites are
		// excluded: they own the auto Vulkan contract and are validated below.
		if !policy.FinalComposite && requiresSoftwareEncode {
			policy.Backend = "software"
			policy.HardwareEncoder = chronon.HardwareEncoderNone
			policy.EncoderBackend = "pipe"
			policy.EncodePreset = ""
			policy.StrictNative = false
		}
		policy.GPURequired = gpuLaneRequested
		if policy.FinalComposite {
			policy.GPURequired = true
		}
		if policy.Backend == "software" {
			policy.GPURequired = false
		}
		// The wire fallback and the worker retry are distinct permissions. A
		// strict-native job and a segmented session fail closed instead of
		// renaming one output; a non-GPU job never enters the software retry
		// lane. Both values live here so the request and the session cannot
		// drift apart.
		policy.CPUFallbackAllowed = !cfg.StrictNativeBackend && !policy.Segmented
		policy.SoftwareRetryAllowed = policy.GPURequired && !cfg.StrictNativeBackend && !policy.Segmented
	}

	if err := policy.Validate(); err != nil {
		return RenderExecutionPolicy{}, err
	}
	return policy, nil
}

// jobExecutionPolicy reads the durable policy off the claimed job. An empty
// value can only come from a row written before migration 032; it means the
// historical GPU-preferred behaviour, which is also the column default.
func jobExecutionPolicy(job *queue.Job) queue.ExecutionPolicy {
	if job == nil || strings.TrimSpace(string(job.ExecutionPolicy)) == "" {
		return queue.ExecutionPolicyGPUPreferred
	}
	return job.ExecutionPolicy
}

func jobLabel(job *queue.Job) string {
	if job == nil || job.ID == "" {
		return "<unknown>"
	}
	return job.ID
}

// Validate asserts the invariants the rest of the pipeline relies on.
func (p RenderExecutionPolicy) Validate() error {
	if strings.TrimSpace(p.Backend) != p.Backend {
		return fmt.Errorf("processor: execution policy backend %q is not trimmed", p.Backend)
	}
	if p.Segmented && p.CPUFallbackAllowed {
		return fmt.Errorf("processor: segmented execution must not allow CPU fallback")
	}
	if p.SoftwareRetryAllowed {
		if !p.GPURequired {
			return fmt.Errorf("processor: software retry requires a GPU requirement")
		}
		if p.StrictNative || p.Segmented {
			return fmt.Errorf("processor: strict-native and segmented execution must not retry through software")
		}
		if !p.CPUFallbackAllowed {
			return fmt.Errorf("processor: software retry requires CPU fallback permission")
		}
	}
	if p.FinalComposite {
		if p.Backend != "auto" {
			return fmt.Errorf("processor: final composite backend %q must be auto", p.Backend)
		}
		if !p.GPURequired {
			return fmt.Errorf("processor: final composite must require the GPU")
		}
	}
	if p.StrictNative && p.CompositionRequired && p.VideoSourceRequired && p.CPUFallbackAllowed {
		return fmt.Errorf("processor: strict native source composition must not allow CPU fallback")
	}
	if !p.FrameRange.Enabled && (p.FrameRange.First != 0 || p.FrameRange.Last != 0) {
		return fmt.Errorf("processor: disabled frame range must be zero")
	}
	if p.FrameRange.Enabled && p.FrameRange.Last < p.FrameRange.First {
		return fmt.Errorf("processor: frame range [%d,%d] is inverted", p.FrameRange.First, p.FrameRange.Last)
	}
	if p.ParallelDisjoint {
		if !p.FrameRange.Enabled {
			return fmt.Errorf("processor: parallel disjoint execution requires an explicit frame range")
		}
		if p.ParentJobID == "" {
			return fmt.Errorf("processor: parallel disjoint execution requires a parent job")
		}
	}
	return nil
}
