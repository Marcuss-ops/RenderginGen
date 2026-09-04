package chronon

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Capabilities is Chronon's self-declared build + environment capability set:
// the single capability authority in the system. Chronon declares what its
// binary was compiled with and which runtime encoders are actually reachable;
// RenderingGen only consumes the declaration and refuses work it cannot
// satisfy. There is deliberately no second capability detector in Go.
//
// Source of truth: `chronon3d_cli doctor --json` →
// { "ready": bool, "capabilities": {...}, "checks": [...] }.
type Capabilities struct {
	Vulkan       bool `json:"vulkan"`
	CUDAInterop  bool `json:"cuda_interop"`
	DirectYUV    bool `json:"direct_yuv"`
	NativeFFmpeg bool `json:"native_ffmpeg"`
	NVDEC        bool `json:"nvdec"`
	NVENC        bool `json:"nvenc"`
	// IPC reports the src/ipc FlatBuffers subsystem (CHRONON3D_ENABLE_IPC).
	// It does NOT gate the daemon socket protocol (chronon_ipc.hpp): that
	// self-contained CHN3-framed server (PREFETCH_ASSET | PREPARE_PLAN |
	// RENDER_OVERLAY | RENDER_JOB | ASSEMBLE_SEGMENTS | STATUS | SHUTDOWN)
	// is compiled into every chronon3d_cli, so mode=ipc workers must not
	// require this flag.
	IPC      bool   `json:"ipc"`
	BuildSHA string `json:"build_sha"`

	// ready is the doctor's overall verdict: no check is in the Fail state.
	// checks keeps the individual check results so Validate can demand
	// runtime evidence (the declared nvenc flag is build-time only; the
	// h264_nvenc runtime probe lives in checks[encoder.nvenc]).
	ready  bool
	checks map[string]string
}

// doctorJSON is the subset of `doctor --json` output this package consumes.
type doctorJSON struct {
	Ready        bool          `json:"ready"`
	Capabilities *Capabilities `json:"capabilities"`
	Checks       []struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"checks"`
}

// doctorTimeout bounds one `doctor --json` invocation. The command probes
// ffmpeg encoders on PATH; on a healthy host it completes well under a
// second, but the budget is generous because this runs once at startup.
const doctorTimeout = 30 * time.Second

// Capabilities runs `doctor --json` through this client's binary and returns
// Chronon's declared capabilities. An error here is a handshake failure: the
// caller must not accept work, because every later failure mode (missing
// CUDA interop, missing NVENC) is strictly worse than refusing to start.
func (c *Client) Capabilities(ctx context.Context) (*Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, c.Binary(), "doctor", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("chronon capabilities: doctor --json: %w", err)
	}
	var doc doctorJSON
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("chronon capabilities: decode doctor output: %w", err)
	}
	if doc.Capabilities == nil {
		return nil, fmt.Errorf("chronon capabilities: doctor output has no capabilities block (binary too old?)")
	}
	caps := doc.Capabilities
	caps.ready = doc.Ready
	caps.checks = make(map[string]string, len(doc.Checks))
	for _, check := range doc.Checks {
		caps.checks[check.ID] = check.Status
	}
	return caps, nil
}

// Requirements is what this worker's configuration demands from Chronon
// before the worker may register as ready. Fields mirror the semantic
// configuration knobs (backend, hardware encoder, strict native path) — not
// Chronon implementation details.
type Requirements struct {
	Vulkan       bool
	CUDAInterop  bool
	DirectYUV    bool
	NativeFFmpeg bool
	NVDEC        bool
	NVENC        bool
}

// GPUHotPathRequirements returns the requirement set for the native GPU hot
// path (NVDEC decode → CUDA composite → NVENC encode). It must be kept in
// sync with Processor.RunGPU's gpuRequired derivation: both express "the
// strict native backend / vulkan+hardware-encoder configuration", and RunGPU
// enforces the path with --gpu-hot-path-mode require_direct_yuv.
func GPUHotPathRequirements() Requirements {
	return Requirements{
		Vulkan:       true,
		CUDAInterop:  true,
		DirectYUV:    true,
		NativeFFmpeg: true,
		NVDEC:        true,
		NVENC:        true,
	}
}

// VulkanOnlyRequirements returns the requirement set for a Vulkan render
// backend without the native encode path.
func VulkanOnlyRequirements() Requirements {
	return Requirements{Vulkan: true}
}

// Validate reports every declared capability that fails to satisfy req, plus
// a doctor overall verdict failure. An empty error means the worker may
// accept work; a non-empty error lists all unsatisfied requirements at once
// so a mis-provisioned host is fixed in one round instead of one failure per
// missing capability.
func (caps *Capabilities) Validate(req Requirements) error {
	if caps == nil {
		return fmt.Errorf("chronon capabilities: no capabilities declared")
	}
	if !caps.ready {
		return fmt.Errorf("chronon doctor reports the environment NOT ready (run `chronon3d_cli doctor` for details)")
	}

	type check struct {
		req    bool
		name   string
		has    bool
		reason string
	}
	missing := []check{
		{req.Vulkan, "vulkan", caps.Vulkan, "not compiled (CHRONON3D_ENABLE_VULKAN=OFF)"},
		{req.CUDAInterop, "cuda_interop", caps.CUDAInterop, "not compiled (CHRONON3D_ENABLE_CUDA_INTEROP=OFF)"},
		{req.DirectYUV, "direct_yuv", caps.DirectYUV, "not compiled (requires vulkan + cuda_interop)"},
		{req.NativeFFmpeg, "native_ffmpeg", caps.NativeFFmpeg, "not compiled (CHRONON3D_ENABLE_NATIVE_FFMPEG=OFF)"},
		{req.NVDEC, "nvdec", caps.NVDEC, "not compiled (requires native_ffmpeg)"},
		{req.NVENC, "nvenc", caps.NVENC, "not compiled"},
	}
	var unmet []string
	for _, m := range missing {
		if m.req && !m.has {
			unmet = append(unmet, m.name+" ("+m.reason+")")
		}
	}
	// Runtime NVENC evidence: the declared nvenc flag is build-time only, so
	// an nvenc requirement additionally demands the h264_nvenc probe to have
	// passed on this host (skip = no NVIDIA/NVENC reachable → must fail here,
	// not mid-render).
	if req.NVENC && caps.checks["encoder.nvenc"] != "pass" {
		unmet = append(unmet, fmt.Sprintf("nvenc runtime probe (encoder.nvenc check status %q, want \"pass\")", caps.checks["encoder.nvenc"]))
	}
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf(
		"worker requirements not satisfied: %s (rebuild Chronon with the canonical video preset: cmake --preset linux-video-release)",
		strings.Join(unmet, "; "))
}
