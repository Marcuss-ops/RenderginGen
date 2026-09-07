// gpu_run.go owns the GPU half of the staged pipeline: the single Chronon
// invocation, its progress observations and the strict-backend receipt gate.
package processor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

// RunGPU performs the single Chronon invocation for a prepared job plus the
// strict-backend receipt gate. This is the only stage that touches the GPU.
func (p *Processor) RunGPU(ctx context.Context, prepared *PreparedJob) error {
	phaseStart := time.Now()
	job := prepared.Job
	metadata := planMetadataOf(prepared.Plan)
	// The chunk contract is half-open [Start, End); Chronon consumes an
	// inclusive last frame. The range is carried EXPLICITLY (RangeEnabled)
	// because a chunk covering exactly frame 0 (Start=0, End=1 -> last=0) is
	// otherwise indistinguishable from "render the whole plan", which would
	// silently over-render a single-frame chunk.
	firstFrame, lastFrame, hasFrameRange := jobFrameRange(job)
	// Native NVENC is required for source-video jobs. Image/text-only plans
	// still use the Vulkan compositor, but Chronon's pipe encoder is the
	// supported output path and reports a software encoder by design.
	hasSourceVideo := planHasVideoSource(prepared.Plan)
	gpuRequired := hasSourceVideo && (p.strictNativeBackend ||
		(p.backend == "vulkan" && p.hardwareEncoder != "" && p.hardwareEncoder != "none"))
	// Render progress: every '[video] N/M frames' milestone the renderer
	// prints is logged (where did the 12 minutes go) and, when a shared
	// tracker is installed, fed into it so health and the queue pusher can
	// report live position. The last milestone also lands in the ledger
	// metrics as render_frames_done/total + render_fps.
	var lastProgress chronon.RenderProgress
	sawProgress := false
	if err := p.renderer.Render(ctx, chronon.RenderRequest{
		PlanPath: prepared.Workspace.PlanPath(),
		// Plans use the canonical assets/<file> namespace. The workspace
		// root (not root/assets) is therefore Chronon's mounted root.
		AssetsRoot:      prepared.Workspace.Root(),
		OutputPath:      prepared.OutputPath,
		AudioSourcePath: prepared.AudioSourcePath,
		Report:          p.report,
		EncodePreset:    p.encodePreset,
		// Canonical verification: the worker resolves the policy (single
		// authority, RENDERINGGEN_RECEIPT_VERIFY) and requests it explicitly
		// from Chronon; Chronon verifies and records what actually ran in the
		// receipt, which FinalizeJob enforces.
		ReceiptVerify: string(renderVerificationLevel()),
		Requirements: chronon.ExecutionRequirements{
			Backend:            p.backend,
			GPURequired:        gpuRequired,
			CPUFallbackAllowed: !p.strictNativeBackend,
			// Clip renders always need a foreground/background composition. The
			// direct-YUV path is reserved for a genuinely video-only render; it
			// cannot preserve the foreground when the background is supplied as
			// a second media input outside the concrete layer list.
			// DirectYUV owns the video composition path, including multiple
			// video layers plus the supported text/image overlays. Keep the
			// general graph for authored compositions without a video source.
			CompositionRequired: !hasSourceVideo && planHasVisualOverlay(prepared.Plan),
			VideoSourceRequired: planHasVideoSource(prepared.Plan),
			PacketCopyAllowed:   true,
		},
		FirstFrame:   firstFrame,
		LastFrame:    lastFrame,
		RangeEnabled: hasFrameRange,
		Output:       chronon.OutputSpec{Codec: "h264"},
		TotalFrames:  int64(metadata.FrameCount),
		Progress: func(progress chronon.RenderProgress) {
			sawProgress = true
			lastProgress = progress
			log.Printf("job %s progress: stage=chronon_render frames_done=%d frames_total=%d fps=%.2f last_frame_at=%s backend=%s encoder=%s",
				job.ID, progress.FramesDone, progress.FramesTotal, progress.FPS,
				progress.At.Format(time.RFC3339Nano), p.backend, p.hardwareEncoder)
			if p.progressTracker != nil {
				p.progressTracker.Observe(job.ID, progress.FramesDone, progress.FramesTotal)
			}
		},
	}); err != nil {
		return fmt.Errorf("processor: render: %w", err)
	}
	us := float64(time.Since(phaseStart).Microseconds())
	prepared.Metrics["render_ms"] = us / 1000
	prepared.Metrics["render_us"] = us
	p.recordPhase("render", phaseStart)
	// Duty-cycle telemetry: the gap this render waited since the previous
	// render ended on this worker. First job reports 0.
	prepared.Metrics["gpu_gap_us"] = p.recordGPUGap(phaseStart)
	// Frame-level observability: the final frame position the renderer
	// reported plus its average fps (0 when the renderer printed no frame
	// milestones — never silently confused with real progress).
	if sawProgress && lastProgress.FramesDone > 0 {
		prepared.Metrics["render_frames_done"] = float64(lastProgress.FramesDone)
		prepared.Metrics["render_frames_total"] = float64(lastProgress.FramesTotal)
		fps := lastProgress.FPS
		if fps <= 0 {
			if elapsed := time.Since(phaseStart).Seconds(); elapsed > 0 {
				fps = float64(lastProgress.FramesDone) / elapsed
			}
		}
		if fps > 0 {
			prepared.Metrics["render_fps"] = fps
		}
	}
	if p.progressTracker != nil {
		p.progressTracker.Forget(job.ID)
	}
	// The native Vulkan/NVENC receipt gate applies to source-video jobs. An
	// image/text-only composition intentionally uses Chronon's Vulkan
	// compositor with the supported software pipe encoder, so requiring an
	// NVENC receipt there would reject a valid authored entity card.
	if p.strictNativeBackend && planHasVideoSource(prepared.Plan) {
		metadata := planMetadataOf(prepared.Plan)
		if err := requireNativeVulkan(prepared.OutputPath, metadata.FrameCount); err != nil {
			return fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
	}
	return nil
}
