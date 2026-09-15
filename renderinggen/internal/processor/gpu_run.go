// gpu_run.go owns the GPU half of the staged pipeline: the single Chronon
// invocation, its progress observations and the strict-backend receipt gate.
package processor

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// progressLogInterval bounds how often a render's frame milestones reach the
// log. Every milestone still updates the progress tracker and the stall
// heartbeat; only the log line is sampled, because the standard logger's mutex
// is shared by every GPU lane and a 24-60fps render emits tens of thousands of
// milestone lines. The first and the final milestone are always logged.
const progressLogInterval = 10 * time.Second

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
	// profile. The worker reports semantic source/overlay facts; Chronon owns
	// the compiled-program choice between DirectYUV and FullGraph.
	hasSourceVideo := planHasVideoSource(prepared.Plan)
	compositionRequired := planHasVisualOverlay(prepared.Plan)
	if compositionRequired && !hasSourceVideo {
		// Chronon owns this physical choice: Vulkan composition followed by a
		// host-frame pipe handoff. It is intentionally distinct from the
		// native source-video/NVENC surface lane.
		prepared.Metrics[metricnames.ChrononGPUCompositionPipe] = 1
	}
	gpuRequired := (hasSourceVideo || compositionRequired) && (p.strictNativeBackend ||
		chronon.StrictNativeRequired(p.backend, p.hardwareEncoder))
	// Render progress: every '[video] N/M frames' milestone the renderer
	// prints is sampled into the log (where did the 12 minutes go) and, when a
	// shared tracker is installed, fed into it so health and the queue pusher
	// can report live position. The last milestone also lands in the ledger
	// metrics as render_frames_done/total + render_fps.
	//
	// The observation is mutex-guarded because the renderer invokes Progress
	// from BOTH its stdout and stderr streaming goroutines: unsynchronised
	// writes to sawProgress/lastProgress were a data race, and the final
	// milestone could be dropped or read torn.
	var progressMu sync.Mutex
	var lastProgress chronon.RenderProgress
	sawProgress := false
	var lastProgressLogAt time.Time
	if err := p.renderer.Render(ctx, chronon.RenderRequest{
		PlanPath:            prepared.Workspace.PlanPath(),
		PreparedPackagePath: prepared.Workspace.PreparedPackagePath(),
		// Plans use the canonical assets/<file> namespace. The workspace
		// root (not root/assets) is therefore Chronon's mounted root.
		AssetsRoot:      prepared.Workspace.Root(),
		OutputPath:      prepared.OutputPath,
		AudioSourcePath: prepared.AudioSourcePath,
		Report:          p.report,
		EncodePreset:    p.encodePreset,
		// Forward the configured encoder instead of letting the adapter guess:
		// the config value is validated at load, so whatever reaches here is
		// what the CLI is asked to run with.
		HardwareEncoder: p.hardwareEncoder,
		// Canonical verification: the worker resolves the policy (single
		// authority, RENDERINGGEN_RECEIPT_VERIFY) and requests it explicitly
		// from Chronon; Chronon verifies and records what actually ran in the
		// receipt, which FinalizeJob enforces.
		ReceiptVerify: string(renderVerificationLevel()),
		Requirements: chronon.ExecutionRequirements{
			Backend:            p.backend,
			GPURequired:        gpuRequired,
			CPUFallbackAllowed: !p.strictNativeBackend,
			// This is a semantic composition requirement, not a backend/path
			// selection. Chronon classifies the compiled program after this
			// request crosses the boundary.
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
			Codec:      "h264",
			Width:      uint32(metadata.Width),
			Height:     uint32(metadata.Height),
			FPSNum:     uint32(metadata.FPSNum),
			FPSDen:     uint32(metadata.FPSDen),
			PipePixFmt: p.pipePixFmt,
		},
		TotalFrames: int64(metadata.FrameCount),
		Progress: func(progress chronon.RenderProgress) {
			progressMu.Lock()
			sawProgress = true
			lastProgress = progress
			final := progress.FramesTotal > 0 && progress.FramesDone >= progress.FramesTotal
			logNow := final || lastProgressLogAt.IsZero() || time.Since(lastProgressLogAt) >= progressLogInterval
			if logNow {
				lastProgressLogAt = time.Now()
			}
			progressMu.Unlock()
			if logNow {
				log.Printf("job %s progress: stage=chronon_render frames_done=%d frames_total=%d fps=%.2f last_frame_at=%s backend=%s encoder=%s",
					job.ID, progress.FramesDone, progress.FramesTotal, progress.FPS,
					progress.At.Format(time.RFC3339Nano), p.backend, p.hardwareEncoder)
			}
			if p.progressTracker != nil {
				p.progressTracker.Observe(job.ID, progress.FramesDone, progress.FramesTotal)
			}
		},
	}); err != nil {
		return fmt.Errorf("processor: render: %w", err)
	}
	us := float64(time.Since(phaseStart).Microseconds())
	prepared.Metrics[metricnames.RenderMS] = us / 1000
	prepared.Metrics[metricnames.RenderUS] = us
	p.recordPhase(metricnames.RenderStem, phaseStart)
	// Duty-cycle telemetry: the gap this render waited since the previous
	// render ended on this worker. First job reports 0.
	prepared.Metrics[metricnames.GPUGapUS] = p.recordGPUGap(phaseStart)
	progressMu.Lock()
	observedProgress := sawProgress
	finalProgress := lastProgress
	progressMu.Unlock()
	// Frame-level observability: the final frame position the renderer
	// reported plus its average fps (0 when the renderer printed no frame
	// milestones — never silently confused with real progress).
	if observedProgress && finalProgress.FramesDone > 0 {
		prepared.Metrics[metricnames.RenderFramesDone] = float64(finalProgress.FramesDone)
		prepared.Metrics[metricnames.RenderFramesTotal] = float64(finalProgress.FramesTotal)
		fps := finalProgress.FPS
		if fps <= 0 {
			if elapsed := time.Since(phaseStart).Seconds(); elapsed > 0 {
				fps = float64(finalProgress.FramesDone) / elapsed
			}
		}
		if fps > 0 {
			prepared.Metrics[metricnames.RenderFPS] = fps
		}
	}
	if p.progressTracker != nil {
		p.progressTracker.Forget(job.ID)
	}
	// The native Vulkan/NVENC receipt gate applies to source-video jobs. An
	// image/text-only composition is a different valid Chronon plan: GPU
	// composition plus host-frame pipe handoff, with no native video surface to
	// certify. Do not demand NVENC surface counters from that plan.
	if p.strictNativeBackend && hasSourceVideo {
		// metadata was computed once at the top of RunGPU from this same plan;
		// do not re-derive (and shadow) it here.
		if err := requireNativeVulkan(prepared.OutputPath, metadata.FrameCount); err != nil {
			return fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
		prepared.NativeCertified = true
	}
	return nil
}
