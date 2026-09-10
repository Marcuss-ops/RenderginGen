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
	// Native NVENC is required for every visual job on the strict Vulkan
	// profile. DirectYUV is reserved for a video-only plan; an image/text
	// composition must use the native FullGraph surface path as well.
	hasSourceVideo := planHasVideoSource(prepared.Plan)
	compositionRequired := planHasVisualOverlay(prepared.Plan)
	gpuRequired := (hasSourceVideo || compositionRequired) && (p.strictNativeBackend ||
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
			// DirectYUV is only for a genuinely video-only render. Any authored
			// image/text/color layer requires the FullGraph so it can finish on a
			// native Vulkan surface and feed NVENC without CPU readback.
			CompositionRequired: compositionRequired,
			VideoSourceRequired: planHasVideoSource(prepared.Plan),
			PacketCopyAllowed:   true,
		},
		FirstFrame:   firstFrame,
		LastFrame:    lastFrame,
		RangeEnabled: hasFrameRange,
		// Forward the complete immutable canvas contract to Chronon. The daemon
		// uses these fields both for encoder configuration and for the canonical
		// render receipt; sending only the codec leaves the receipt without an
		// expected frame rate and makes a valid render fail closed at finalize.
		Output: chronon.OutputSpec{
			Codec:  "h264",
			Width:  uint32(metadata.Width),
			Height: uint32(metadata.Height),
			FPSNum: uint32(metadata.FPSNum),
			FPSDen: uint32(metadata.FPSDen),
		},
		TotalFrames: int64(metadata.FrameCount),
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
	// The native Vulkan/NVENC receipt gate applies to both source-video jobs
	// and authored image/text compositions. A successful Vulkan label alone is
	// insufficient: the bounded telemetry must prove Vulkan frames, NVENC
	// frames, and zero software/readback fallback.
	if p.strictNativeBackend && (hasSourceVideo || compositionRequired) {
		metadata := planMetadataOf(prepared.Plan)
		if err := requireNativeVulkan(prepared.OutputPath, metadata.FrameCount); err != nil {
			return fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
	}
	return nil
}
