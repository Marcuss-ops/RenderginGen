// render_args.go owns the CLI boundary translation: RenderRequest semantics
// (backends, chunk ranges, audio source, native NVENC contract) to
// chronon3d_cli arguments.
package chronon

import "fmt"

// DefaultHardwareEncoder is the native encoder the GPU-required path uses when
// the worker config does not select one. It is exported because it is the
// SINGLE default: config's profile defaults and validate() read it too, so the
// "which encoder when nothing is configured" rule cannot exist twice (it used
// to be a literal in both packages). It is the only value the native hot-path
// contract currently certifies.
const DefaultHardwareEncoder = "nvenc"

// HardwareEncoderNone disables the GPU handoff: a worker configured with it
// never sets GPURequired, so no native encoder argument reaches Chronon.
const HardwareEncoderNone = "none"

// nativeEncodeSelection is the resolved encoder selection Chronon must honour
// for a render request: the hardware encoder, the encoder backend and the
// GPU hot-path mode.
type nativeEncodeSelection struct {
	HardwareEncoder string
	EncoderBackend  string
	GpuHotPathMode  string
}

// resolveNativeEncodeSelection is the SINGLE authority that maps semantic
// requirements to the concrete encoder selection, shared by the CLI argument
// builder (renderArgs) and the IPC output_spec builder (outputSpecForTransport).
// Two transports deriving this independently is exactly how the strict-native
// contract got lost: the CLI asked for nvenc while the IPC payload asked for
// nothing, so the daemon fell back to the software pipe encoder.
//
// It reports ok=false when the request declares no GPU/encoder requirement at
// all, in which case no encoder argument is emitted and Chronon's own default
// applies unchanged.
func resolveNativeEncodeSelection(req RenderRequest) (nativeEncodeSelection, bool) {
	reqs := req.Requirements
	if reqs.GPURequired {
		// GPU requirements select the native encoder capability. RenderingGen
		// declares that semantic requirement; Chronon owns the compiled-scene
		// decision between DirectYUV and FullGraph.
		//
		// The encoder is the CONFIGURED one (defaulted to nvenc) rather than a
		// hardcoded literal: accepting hardware_encoder at config load and then
		// always passing nvenc made the setting decorative.
		hardware := req.HardwareEncoder
		if hardware == "" {
			hardware = DefaultHardwareEncoder
		}
		// A caller that forbids CPU fallback is the strict-native contract:
		// ask for the native lane EXPLICITLY instead of "auto". With "auto" a
		// composition-only plan (image/text, no source video) resolves to the
		// host-frame FFmpeg pipe lane and encodes with libx264 — precisely the
		// silent software degradation the strict profile forbids.
		hotPath := "auto"
		if !reqs.CPUFallbackAllowed {
			hotPath = "require_gpu_native"
		}
		return nativeEncodeSelection{
			HardwareEncoder: hardware,
			EncoderBackend:  "native",
			GpuHotPathMode:  hotPath,
		}, true
	}
	if reqs.CompositionRequired {
		// Non-strict composition still declares semantics only. Chronon owns
		// the DirectYUV/FullGraph decision after it has compiled the program,
		// and no hardware encoder is requested.
		return nativeEncodeSelection{EncoderBackend: "native", GpuHotPathMode: "auto"}, true
	}
	return nativeEncodeSelection{}, false
}

// validateRenderRequest rejects a RenderRequest the adapter cannot honour
// unambiguously. Today that means an explicit chunk range whose inclusive last
// frame precedes its first frame: renderArgs would emit no
// --start-frame/--end-frame at all and Chronon would render the WHOLE plan — a
// silent over-render that reports success for a chunk that never rendered.
// The worker pipeline validates ranges against the plan (validateFrameRange),
// but any caller reaching a chronon transport directly would skip that check,
// so the rule lives here, on the boundary both transports share.
func validateRenderRequest(req RenderRequest) error {
	if !req.RangeEnabled {
		// Whole-plan render: the coordinates are ignored by design.
		return nil
	}
	if req.FirstFrame < 0 {
		return fmt.Errorf("chronon: invalid frame range: first_frame=%d is negative", req.FirstFrame)
	}
	if req.LastFrame < req.FirstFrame {
		return fmt.Errorf("chronon: invalid frame range: last_frame=%d precedes first_frame=%d (would render the whole plan)", req.LastFrame, req.FirstFrame)
	}
	return nil
}

// renderArgs builds the chronon3d_cli arguments for the render subcommand.
// Callers must have run validateRenderRequest first (Client.Render does); this
// function only translates an already-valid request.
func renderArgs(req RenderRequest) []string {
	// Backend selection is an implementation detail of the CLI adapter. The
	// public request carries only semantic requirements; Chronon chooses the
	// concrete backend at this boundary.
	backend := req.Requirements.Backend
	if backend == "" {
		backend = "auto"
	}
	if backend == "auto" && (req.Requirements.GPURequired || req.Requirements.CompositionRequired) {
		backend = "vulkan"
	}
	args := []string{
		"render",
		"--plan", req.PlanPath,
		"--assets-root", req.AssetsRoot,
		"--backend", backend,
		"-o", req.OutputPath,
	}
	if req.Report {
		// Emit the execution report and telemetry JSONL (render_ms, encode_ms,
		// cache_hits/misses) used by the performance benchmark.
		args = append(args, "--report")
	}
	if selection, ok := resolveNativeEncodeSelection(req); ok {
		if selection.HardwareEncoder != "" {
			args = append(args, "--hardware", selection.HardwareEncoder)
		}
		args = append(args, "--encoder-backend", selection.EncoderBackend)
		if req.EncodePreset != "" && selection.HardwareEncoder != "" {
			// Explicit NVENC preset (e.g. "p2"): the throughput tier the
			// worker is configured with. Only the native branch targets
			// h264_nvenc; the pipe branch uses x264 vocabulary and must never
			// receive pN presets (libx264 rejects "p2" outright).
			args = append(args, "--encode-preset", req.EncodePreset)
		}
		// GPURequired is a semantic capability request. Chronon classifies
		// the compiled program and chooses DirectYUV or FullGraph.
		args = append(args, "--gpu-hot-path-mode", selection.GpuHotPathMode)
	}
	// OutputSpec asymmetry (deliberate, do not "fix" by adding flags): on the
	// CLI transport the canvas contract — codec/width/height/fps — already
	// lives in plan.json, which the CLI reads itself; translating those fields
	// to arguments here would be at best a no-op. Only the pipe pixel format,
	// which has no plan-level home, becomes a CLI argument. The IPC transport
	// instead forwards the COMPLETE OutputSpec in the job payload
	// (outputSpecForTransport) because the daemon consumes it directly. The
	// part that must never drift — the resolved native encoder selection —
	// comes from the shared resolveNativeEncodeSelection authority on both
	// transports.
	if req.Output.PipePixFmt != "" {
		args = append(args, "--pipe-pixfmt", req.Output.PipePixFmt)
	}
	if req.AudioSourcePath != "" {
		// Chronon's native A/V mux path uses --gop-source for the source audio
		// stream. The worker passes an absolute workspace path, so the CLI can
		// open it after materialization.
		args = append(args, "--gop-source", req.AudioSourcePath)
	}
	if req.RangeEnabled {
		// Only an explicit, VALIDATED range is emitted. Without RangeEnabled the
		// plan renders in full — even when both coordinates happen to be zero (a
		// single-frame chunk starting at frame 0 must reach Chronon as
		// --start-frame 0 --end-frame 0, never as "whole plan"). An inverted
		// range never reaches here: validateRenderRequest rejects it before the
		// command is built.
		args = append(args, "--start-frame", fmt.Sprint(req.FirstFrame), "--end-frame", fmt.Sprint(req.LastFrame))
	}
	return args
}
