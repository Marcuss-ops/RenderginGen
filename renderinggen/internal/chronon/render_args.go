// render_args.go owns the CLI boundary translation: RenderRequest semantics
// (backends, chunk ranges, audio source, native NVENC contract) to
// chronon3d_cli arguments.
package chronon

import "fmt"

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
		// GPU requirements must select the complete native handoff. Passing
		// only --hardware leaves Chronon in its auto/direct-yuv resolver, which
		// can feed a decoded host surface to the native encoder. RenderingGen
		// has already declared the semantic requirement, so make that contract
		// explicit at the CLI boundary.
		args = append(args, "--hardware", "nvenc")
		// DirectYUV is valid only for a genuinely video-only plan. Any
		// composition, including image/text-only or video background +
		// foreground, must use the complete Vulkan graph so every visual input
		// is preserved and the final surface can reach NVENC natively.
		hotPath := "require_direct_yuv"
		if req.Requirements.CompositionRequired {
			args = append(args, "--encoder-backend", "native")
			if req.EncodePreset != "" {
				args = append(args, "--encode-preset", req.EncodePreset)
			}
			hotPath = "require_gpu_native"
		} else {
			args = append(args, "--encoder-backend", "native")
			if req.EncodePreset != "" {
				// Explicit NVENC preset (e.g. "p2"): the throughput tier the
				// worker is configured with. The native branch is the only
				// path where the preset targets h264_nvenc; the pipe branch
				// uses x264 vocabulary and must never receive pN presets.
				args = append(args, "--encode-preset", req.EncodePreset)
			}
		}
		args = append(args, "--gpu-hot-path-mode", hotPath)
	} else if req.Requirements.CompositionRequired {
		// Non-strict Vulkan composition: keep the same FullGraph shape and let
		// Chronon select the configured fallback encoder.
		args = append(args, "--encoder-backend", "native", "--gpu-hot-path-mode", "require_gpu_native")
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
