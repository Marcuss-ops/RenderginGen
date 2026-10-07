//go:build certification

// Goal 3 end-to-end harness — background (image + video) and text watermark.
//
// This file renders REAL clips with the real chronon3d_cli on the GPU-native
// lane (vulkan + nvenc native, no CPU fallback), from the exact semantic
// contract PipelineGen submits, and then certifies the artefacts:
//
//	structural  — ffprobe geometry/fps/frame count/duration
//	decode      — the whole bitstream decodes
//	A/V         — an audio stream is present and its duration matches the video
//	pixel       — the background plate is really composited at the canvas border,
//	              and the watermark really changes the frame
//	determinism — the same plan rendered twice produces identical frames
//
// It is opt-in like the rest of the runtime certification suite: it skips when
// chronon3d_cli is unavailable, and it needs the GPU (the strict native lane
// fails closed without it). The rendered MP4s are written to a STABLE directory
// so a human can inspect them:
//
//	GOAL3_RENDER_OUT_DIR overrides it; otherwise
//	<repo>/refactored/ops/benchmarks/goal3-e2e-<YYYYMMDD>/

package overlay
