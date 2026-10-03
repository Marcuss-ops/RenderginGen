// gpu_run.go owns the GPU half of the staged pipeline: the single Chronon
// invocation, its progress observations and the strict-backend receipt gate.
package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
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
	finalComposite := isFinalJobComposite(job)
	renderBackend := p.backend
	if finalComposite {
		// Final-job intermediates must use the full Vulkan compositor so layers
		// that begin after frame zero are preserved.
		renderBackend = "auto"
	}
	encodePreset := p.encodePreset
	metadata := planMetadataOf(prepared.Plan)
	// The chunk contract is half-open [Start, End); Chronon consumes an
	// inclusive last frame. The range is carried EXPLICITLY (RangeEnabled)
	// because a chunk covering exactly frame 0 (Start=0, End=1 -> last=0) is
	// otherwise indistinguishable from "render the whole plan", which would
	// silently over-render a single-frame chunk.
	firstFrame, lastFrame, hasFrameRange := jobFrameRange(job)
	// Only the chunk producer may request the future disjoint execution class.
	// Do not infer it from a range: callers can request a range for correctness
	// or seeking without proving that another job owns the complementary range.
	parallelDisjoint := job != nil && job.ParentJobID != "" && hasFrameRange && job.ChunkIndex >= 0
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
	renderRequest := chronon.RenderRequest{
		PlanPath:            prepared.Workspace.PlanPath(),
		PreparedPackagePath: prepared.Workspace.PreparedPackagePath(),
		// Plans use the canonical assets/<file> namespace. The workspace
		// root (not root/assets) is therefore Chronon's mounted root.
		AssetsRoot:            prepared.Workspace.Root(),
		OutputPath:            prepared.OutputPath,
		AudioSourcePath:       prepared.AudioSourcePath,
		AudioTargetSampleRate: prepared.AudioTargetSampleRate,
		Report:                p.report,
		EncodePreset:          encodePreset,
		// Forward the configured encoder instead of letting the adapter guess:
		// the config value is validated at load, so whatever reaches here is
		// what the CLI is asked to run with.
		HardwareEncoder: p.hardwareEncoder,
		EncoderBackend:  p.encoderBackend,
		// Canonical verification: the worker resolves the policy (single
		// authority, pipeline.receipt_verify) and requests it explicitly from
		// Chronon; Chronon verifies and records what actually ran in the
		// receipt, which FinalizeJob enforces.
		ReceiptVerify: string(p.receiptVerifyLevel()),
		Requirements: chronon.ExecutionRequirements{
			Backend:     renderBackend,
			GPURequired: gpuRequired,
			// Final-job scene composites have a dedicated, namespaced job ID.
			// Their authored layers can start after frame zero, so Chronon must
			// classify and execute the complete Vulkan graph instead of selecting
			// DirectYUV from a frame that has no active overlay yet. Ordinary jobs
			// retain the configured fallback policy.
			CPUFallbackAllowed: !p.strictNativeBackend,
			// This is a semantic composition requirement, not a backend/path
			// selection. Chronon classifies the compiled program after this
			// request crosses the boundary.
			CompositionRequired: compositionRequired,
			VideoSourceRequired: planHasVideoSource(prepared.Plan),
			PacketCopyAllowed:   true,
		},
		FirstFrame:       firstFrame,
		LastFrame:        lastFrame,
		RangeEnabled:     hasFrameRange,
		ParallelDisjoint: parallelDisjoint,
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
				workerlog.ByJobID(job.ID).Infof("progress: stage=chronon_render frames_done=%d frames_total=%d fps=%.2f last_frame_at=%s backend=%s encoder=%s",
					progress.FramesDone, progress.FramesTotal, progress.FPS,
					progress.At.Format(time.RFC3339Nano), renderBackend, p.hardwareEncoder)
			}
			if p.progressTracker != nil {
				p.progressTracker.Observe(job.ID, progress.FramesDone, progress.FramesTotal)
			}
		},
	}
	if err := p.renderer.Render(ctx, renderRequest); err != nil {
		if !gpuRequired || p.strictNativeBackend || !softwareRetryableGPUFailure(err) {
			return fmt.Errorf("processor: render: %w", err)
		}
		workerlog.ByJobID(job.ID).Warnf("Vulkan render failed with a recoverable device/capability error; retrying the same plan through the software renderer: %v", err)
		fallbackRequest := renderRequest
		fallbackRequest.OutputPath = prepared.OutputPath + ".software-fallback.mp4"
		fallbackRequest.EncodePreset = ""
		fallbackRequest.HardwareEncoder = chronon.HardwareEncoderNone
		fallbackRequest.EncoderBackend = "pipe"
		fallbackRequest.Requirements.Backend = "software"
		fallbackRequest.Requirements.GPURequired = false
		fallbackRequest.Requirements.CPUFallbackAllowed = true
		fallbackRequest.Progress = func(progress chronon.RenderProgress) {
			workerlog.ByJobID(job.ID).Infof("progress: stage=chronon_render_software_fallback frames_done=%d frames_total=%d fps=%.2f last_frame_at=%s backend=software encoder=libx264",
				progress.FramesDone, progress.FramesTotal, progress.FPS,
				progress.At.Format(time.RFC3339Nano))
			if p.progressTracker != nil {
				p.progressTracker.Observe(job.ID, progress.FramesDone, progress.FramesTotal)
			}
		}
		if _, statErr := os.Stat(fallbackRequest.OutputPath); statErr == nil {
			return fmt.Errorf("processor: software fallback output already exists; refusing to overwrite %s (original render error: %w)", fallbackRequest.OutputPath, err)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("processor: inspect software fallback output %s: %w", fallbackRequest.OutputPath, statErr)
		}
		if fallbackErr := p.renderer.Render(ctx, fallbackRequest); fallbackErr != nil {
			return fmt.Errorf("processor: render failed on Vulkan (%v) and software fallback failed: %w", err, fallbackErr)
		}
		if _, statErr := os.Stat(prepared.OutputPath); statErr == nil {
			return fmt.Errorf("processor: original render left an output at %s; preserving it and the successful software fallback at %s", prepared.OutputPath, fallbackRequest.OutputPath)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("processor: inspect original output %s before publishing software fallback: %w", prepared.OutputPath, statErr)
		}
		if renameErr := os.Rename(fallbackRequest.OutputPath, prepared.OutputPath); renameErr != nil {
			return fmt.Errorf("processor: publish software fallback %s as %s: %w", fallbackRequest.OutputPath, prepared.OutputPath, renameErr)
		}
		fallbackTiming := fallbackRequest.OutputPath + ".timing.json"
		if _, statErr := os.Stat(fallbackTiming); statErr == nil {
			if renameErr := os.Rename(fallbackTiming, prepared.OutputPath+".timing.json"); renameErr != nil {
				return fmt.Errorf("processor: publish software fallback timing sidecar: %w", renameErr)
			}
		}
		prepared.Metrics[metricnames.ChrononSoftwareFallback] = 1
		workerlog.ByJobID(job.ID).Infof("software fallback completed and was published at %s", filepath.Base(prepared.OutputPath))
	}
	us := float64(time.Since(phaseStart).Microseconds())
	prepared.Metrics[metricnames.RenderMS] = us / 1000
	prepared.Metrics[metricnames.RenderUS] = us
	p.recordPhase(metricnames.RenderStem, phaseStart)
	// Duty-cycle telemetry: the gap this render waited since the previous
	// render ended on this worker. First job reports 0. The same value is
	// published to the optional duty-cycle observer (gpu_gap_hook.go), so the
	// per-job metric and the worker's histogram can never disagree.
	gapUS := p.recordGPUGap(phaseStart)
	prepared.Metrics[metricnames.GPUGapUS] = gapUS
	p.noteGPUGap(time.Duration(gapUS) * time.Microsecond)
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
	if p.strictNativeBackend && hasSourceVideo && !finalComposite {
		// metadata was computed once at the top of RunGPU from this same plan;
		// do not re-derive (and shadow) it here.
		if err := requireNativeVulkan(prepared.OutputPath, metadata.FrameCount); err != nil {
			return fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
		prepared.NativeCertified = true
	}
	return nil
}

// isFinalJobComposite recognizes the local scene-composite jobs emitted by
// PipelineGen's final_job pipeline. The namespace is part of the queue job ID
// contract (e.g. <plan>:final-composite:<scene>); the exception is limited to
// these intermediate scene renders and does not alter other worker traffic.
func isFinalJobComposite(job *queue.Job) bool {
	return job != nil && strings.Contains(job.ID, ":final-composite:")
}

func softwareRetryableGPUFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"device lost",
		"segmentation fault",
		"unsupportedcapability",
		"no legacy-node fallback",
		"native residency violation",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
