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

// renderArgs builds the chronon3d_cli arguments for the render subcommand.
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
	if req.Requirements.GPURequired {
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
		args = append(args, "--hardware", hardware)
		args = append(args, "--encoder-backend", "native")
		if req.EncodePreset != "" {
			// Explicit NVENC preset (e.g. "p2"): the throughput tier the
			// worker is configured with. The native branch is the only
			// path where the preset targets h264_nvenc; the pipe branch
			// uses x264 vocabulary and must never receive pN presets.
			args = append(args, "--encode-preset", req.EncodePreset)
		}
		// GPURequired is a semantic capability request. Chronon classifies
		// the compiled program and chooses DirectYUV or FullGraph.
		args = append(args, "--gpu-hot-path-mode", "auto")
	} else if req.Requirements.CompositionRequired {
		// Non-strict composition still declares semantics only. Chronon owns
		// the DirectYUV/FullGraph decision after it has compiled the program.
		args = append(args, "--encoder-backend", "native", "--gpu-hot-path-mode", "auto")
	}
	if req.Output.PipePixFmt != "" {
		args = append(args, "--pipe-pixfmt", req.Output.PipePixFmt)
	}
	if req.AudioSourcePath != "" {
		// Chronon's native A/V mux path uses --gop-source for the source audio
		// stream. The worker passes an absolute workspace path, so the CLI can
		// open it after materialization.
		args = append(args, "--gop-source", req.AudioSourcePath)
	}
	if req.RangeEnabled && req.LastFrame >= req.FirstFrame {
		// Only an explicit range is emitted. Without RangeEnabled the plan
		// renders in full — even when both coordinates happen to be zero (a
		// single-frame chunk starting at frame 0 must reach Chronon as
		// --start-frame 0 --end-frame 0, never as "whole plan").
		args = append(args, "--start-frame", fmt.Sprint(req.FirstFrame), "--end-frame", fmt.Sprint(req.LastFrame))
	}
	return args
}
