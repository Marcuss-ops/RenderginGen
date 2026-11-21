package processor

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

func policyTestPlan(layers ...overlay.Layer) *overlay.Plan {
	return &overlay.Plan{
		Canvas: overlay.Canvas{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, DurationFrames: 90},
		Layers: layers,
		Output: overlay.Output{ProfileID: "VELOX_OVERLAY_VIDEO_ONLY_V3"},
	}
}

func policyTestJob(id string) *queue.Job { return &queue.Job{ID: id} }

// TestResolveExecutionPolicyMatrix covers the nine combinations the audit
// names. Every case also asserts the policy invariants.
func TestResolveExecutionPolicyMatrix(t *testing.T) {
	source := overlay.Layer{ID: "source", Type: "video"}
	composition := overlay.Layer{ID: "caption", Type: "text", Text: "policy"}
	baseConfig := ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native", EncodePreset: ""}

	cases := []struct {
		name   string
		job    *queue.Job
		plan   *overlay.Plan
		cfg    ProcessorConfig
		assert func(*testing.T, RenderExecutionPolicy)
	}{
		{
			name: "source video plus strict native forbids fallback",
			job:  policyTestJob("source-strict"),
			plan: policyTestPlan(source),
			cfg:  func() ProcessorConfig { c := baseConfig; c.StrictNativeBackend = true; return c }(),
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if p.CPUFallbackAllowed || p.SoftwareRetryAllowed || !p.GPURequired {
					t.Fatalf("policy = %+v, want no CPU fallback, no worker retry and GPU required", p)
				}
			},
		},
		{
			name: "composition without source keeps the declared backend",
			job:  policyTestJob("composition"),
			plan: policyTestPlan(composition),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if !p.CompositionRequired || p.VideoSourceRequired || p.Backend != "vulkan" {
					t.Fatalf("policy = %+v, want declared Vulkan composition without source", p)
				}
			},
		},
		{
			name: "segmented P4 job forbids fallback",
			job:  policyTestJob("segmented"),
			plan: policyTestPlan(source),
			cfg:  func() ProcessorConfig { c := baseConfig; c.Segmented = true; return c }(),
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if !p.Segmented || p.CPUFallbackAllowed || p.SoftwareRetryAllowed {
					t.Fatalf("policy = %+v, want segmented without fallback or retry", p)
				}
			},
		},
		{
			name: "final composite uses auto backend",
			job:  policyTestJob("plan:final-composite:scene-1"),
			plan: policyTestPlan(source, composition),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if !p.FinalComposite || p.Backend != "auto" || !p.GPURequired {
					t.Fatalf("policy = %+v, want auto final composite requiring GPU", p)
				}
			},
		},
		{
			name: "canonicalized profile does not move policy into the post pool",
			job:  policyTestJob("canonical"),
			plan: policyTestPlan(composition),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if p.Backend != "vulkan" || !p.CompositionRequired || p.Segmented {
					t.Fatalf("policy = %+v, want unchanged canonical execution policy", p)
				}
			},
		},
		{
			name: "retryable error is admitted by policy",
			job:  policyTestJob("retryable"),
			plan: policyTestPlan(source),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if !softwareRetryableGPUFailure(errors.New("Vulkan device lost during vkWaitForFences")) {
					t.Fatal("classifier must recognize the recoverable failure")
				}
				if !softwareRetryableGPUFailure(chronon.ErrIPCStallTimeout) {
					t.Fatal("classifier must route an IPC stall to the software retry")
				}
				if !p.CPUFallbackAllowed || !p.SoftwareRetryAllowed {
					t.Fatalf("policy = %+v, the non-strict GPU policy must admit the retry", p)
				}
			},
		},
		{
			name: "non retryable error is rejected by classifier",
			job:  policyTestJob("non-retryable"),
			plan: policyTestPlan(source),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if softwareRetryableGPUFailure(errors.New("invalid plan: unknown layer")) {
					t.Fatal("classifier must not admit a contract error")
				}
			},
		},
		{
			name: "explicit frame range uses inclusive last frame",
			job:  &queue.Job{ID: "chunk", ParentJobID: "parent", ChunkIndex: 2, FrameRange: &queue.FrameRange{Start: 10, End: 21}},
			plan: policyTestPlan(source),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if !p.FrameRange.Enabled || p.FrameRange.First != 10 || p.FrameRange.Last != 20 {
					t.Fatalf("range = %+v, want enabled [10,20]", p.FrameRange)
				}
				if !p.ParallelDisjoint {
					t.Fatal("chunk child must carry the parallel-disjoint hint")
				}
			},
		},
		{
			name: "no range renders the whole plan",
			job:  policyTestJob("whole-plan"),
			plan: policyTestPlan(source),
			cfg:  baseConfig,
			assert: func(t *testing.T, p RenderExecutionPolicy) {
				if p.FrameRange.Enabled || p.FrameRange.First != 0 || p.FrameRange.Last != 0 {
					t.Fatalf("range = %+v, want disabled zero range", p.FrameRange)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := ResolveExecutionPolicy(tc.job, tc.plan, tc.cfg)
			if err != nil {
				t.Fatalf("ResolveExecutionPolicy: %v", err)
			}
			if err := policy.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			tc.assert(t, policy)
		})
	}
}

func TestRenderExecutionPolicyValidateSoftwareRetry(t *testing.T) {
	valid := RenderExecutionPolicy{Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: true, SoftwareRetryAllowed: true}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid software retry policy rejected: %v", err)
	}
	for name, mutate := range map[string]func(*RenderExecutionPolicy){
		"no GPU requirement": func(p *RenderExecutionPolicy) { p.GPURequired = false },
		"strict native":      func(p *RenderExecutionPolicy) { p.StrictNative = true },
		"segmented":          func(p *RenderExecutionPolicy) { p.Segmented = true },
		"wire fallback denied": func(p *RenderExecutionPolicy) {
			p.CPUFallbackAllowed = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := valid
			mutate(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatalf("policy = %+v, want Validate to reject it", policy)
			}
		})
	}
}

// TestBuildChrononRequestParity pins the wire projection of three fixtures.
// The baseline is the pre-refactor RunGPU request: same backend, encoder
// selection, requirements, range and output contract.
func TestBuildChrononRequestParity(t *testing.T) {
	proc, _, _ := newProcessorWith(t, Options{})
	fixtures := []struct {
		name     string
		job      *queue.Job
		plan     *overlay.Plan
		cfg      ProcessorConfig
		expected chronon.ExecutionRequirements
		rangeOn  bool
		first    int64
		last     int64
	}{
		{
			name:     "source only",
			job:      policyTestJob("source"),
			plan:     policyTestPlan(overlay.Layer{ID: "source", Type: "video"}),
			cfg:      ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc"},
			expected: chronon.ExecutionRequirements{Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: true, VideoSourceRequired: true, PacketCopyAllowed: true},
		},
		{
			name:     "composition pipe",
			job:      policyTestJob("composition"),
			plan:     policyTestPlan(overlay.Layer{ID: "caption", Type: "text", Text: "x"}),
			cfg:      ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "pipe"},
			expected: chronon.ExecutionRequirements{Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: true, CompositionRequired: true, PacketCopyAllowed: true},
		},
		{
			name:     "strict chunk",
			job:      &queue.Job{ID: "chunk", ParentJobID: "parent", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 30, End: 60}},
			plan:     policyTestPlan(overlay.Layer{ID: "source", Type: "video"}),
			cfg:      ProcessorConfig{Backend: "vulkan", HardwareEncoder: "nvenc", StrictNativeBackend: true},
			expected: chronon.ExecutionRequirements{Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: false, VideoSourceRequired: true, PacketCopyAllowed: true},
			rangeOn:  true,
			first:    30,
			last:     59,
		},
	}
	for i, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := workspace.New(proc.jobsRoot, tc.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Cleanup()
			prepared := &PreparedJob{
				Job: tc.job, Workspace: ws, Plan: tc.plan,
				OutputPath: ws.Root() + "/result.mp4", Metrics: map[string]float64{},
			}
			policy, err := ResolveExecutionPolicy(tc.job, tc.plan, tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			req, err := proc.BuildChrononRequest(prepared, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(req.Requirements, tc.expected) {
				t.Fatalf("fixture %d requirements = %+v, want %+v", i, req.Requirements, tc.expected)
			}
			if req.RangeEnabled != tc.rangeOn || req.FirstFrame != tc.first || req.LastFrame != tc.last {
				t.Fatalf("fixture %d range = %v [%d,%d], want %v [%d,%d]", i, req.RangeEnabled, req.FirstFrame, req.LastFrame, tc.rangeOn, tc.first, tc.last)
			}
			if req.PlanPath != ws.PlanPath() || req.OutputPath != prepared.OutputPath {
				t.Fatalf("fixture %d paths = %q/%q", i, req.PlanPath, req.OutputPath)
			}
		})
	}
}

// TestResolveExecutionPolicyRoutesStripPlansToSoftware pins the explicit
// capability contract: the policy reads Plan.Features, not transient per-layer
// metadata, so the compiler's statement that a plan keeps overlapping animated
// full-canvas surfaces is the single input. A final composite stays on its auto
// Vulkan contract even if it somehow carries the flag.
func TestResolveExecutionPolicyRoutesStripPlansToSoftware(t *testing.T) {
	plan := policyTestPlan(overlay.Layer{ID: "chain:seg0:image", Type: "image", Asset: "assets/a.png"})
	plan.Features.OverlappingFullCanvasStrip = true
	policy, err := ResolveExecutionPolicy(policyTestJob("strip"), plan, ProcessorConfig{
		Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native",
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Backend != "software" || policy.HardwareEncoder != chronon.HardwareEncoderNone || policy.EncoderBackend != "pipe" {
		t.Fatalf("strip policy = %+v, want software/none/pipe", policy)
	}
	if policy.GPURequired {
		t.Fatalf("strip policy requires a GPU: %+v", policy)
	}
	if !policy.CPUFallbackAllowed || policy.SoftwareRetryAllowed {
		t.Fatalf("strip policy fallback/retry = %v/%v, want true/false", policy.CPUFallbackAllowed, policy.SoftwareRetryAllowed)
	}

	// The capability must NOT be inferred from a layer that merely looks like a
	// strip member; only the explicit feature selects the software lane.
	inferred := policyTestPlan(overlay.Layer{ID: "chain:seg0:image", Type: "image", Asset: "assets/a.png"})
	if policy, err := ResolveExecutionPolicy(policyTestJob("no-strip"), inferred, ProcessorConfig{
		Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native",
	}); err != nil {
		t.Fatal(err)
	} else if policy.Backend != "vulkan" {
		t.Fatalf("default policy backend = %q, want the declared vulkan", policy.Backend)
	}

	final := policyTestPlan(overlay.Layer{ID: "final:image", Type: "image", Asset: "assets/a.png"})
	final.Features.OverlappingFullCanvasStrip = true
	if policy, err := ResolveExecutionPolicy(policyTestJob("plan:final-composite:scene-1"), final, ProcessorConfig{
		Backend: "vulkan", HardwareEncoder: "nvenc", EncoderBackend: "native",
	}); err != nil {
		t.Fatal(err)
	} else if policy.Backend != "auto" || !policy.GPURequired {
		t.Fatalf("final composite strip policy = %+v, want the auto Vulkan contract", policy)
	}
}
