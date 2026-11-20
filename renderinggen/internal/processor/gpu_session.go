// gpu_session.go owns one render session: the single engine invocation, the
// software retry and the post-render certification/handoff. RunGPU orchestrates
// these stages; it does not carry their local state.
package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// RenderSessionOutcome reports whether the session had to publish a software
// fallback and how long the session took, including the fallback when used.
type RenderSessionOutcome struct {
	SoftwareFallback bool
	Elapsed          time.Duration
}

// ExecuteRenderSession invokes the renderer once and applies the worker's
// software retry only when the resolved policy permits it. No fallback
// condition is re-invented here.
func (p *Processor) ExecuteRenderSession(ctx context.Context, prepared *PreparedJob, req chronon.RenderRequest, policy RenderExecutionPolicy) (RenderSessionOutcome, error) {
	started := time.Now()
	renderer := p.renderer
	if policy.Backend == "software" && p.softwareRenderer != nil && len(req.OutputSegments) == 0 {
		// Chain camera pans keep overlapping animated full-canvas images and
		// are intentionally routed to the CLI/libx264 lane; the IPC daemon's
		// host-export path cannot read those composed frames back.
		renderer = p.softwareRenderer
		workerlog.ByJobID(prepared.Job.ID).Infof("rendering on the software CLI lane (libx264) for this plan")
	}
	err := renderer.Render(ctx, req)
	if err == nil {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, nil
	}
	if !policy.SoftwareRetryAllowed || !softwareRetryableGPUFailure(err) {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: render: %w", err)
	}
	workerlog.ByJobID(prepared.Job.ID).Warnf("Vulkan render failed with a recoverable device/capability error; retrying the same plan through the software renderer: %v", err)

	fallback := req
	fallback.OutputPath = prepared.OutputPath + ".software-fallback.mp4"
	fallback.EncodePreset = ""
	fallback.HardwareEncoder = chronon.HardwareEncoderNone
	fallback.EncoderBackend = "pipe"
	fallback.Requirements.Backend = "software"
	fallback.Requirements.GPURequired = false
	fallback.Requirements.CPUFallbackAllowed = true
	fallbackObserver := newRenderProgressObserver(prepared.Job, p.progressTracker, "software", "libx264")
	fallbackObserver.logEvery = 0 // the fallback lane historically logged every milestone
	fallback.Progress = func(progress chronon.RenderProgress) {
		fallbackObserver.Observe(progress)
	}

	if _, statErr := os.Stat(fallback.OutputPath); statErr == nil {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: software fallback output already exists; refusing to overwrite %s (original render error: %w)", fallback.OutputPath, err)
	} else if !os.IsNotExist(statErr) {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: inspect software fallback output %s: %w", fallback.OutputPath, statErr)
	}
	if fallbackErr := p.renderer.Render(ctx, fallback); fallbackErr != nil {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: render failed on Vulkan (%v) and software fallback failed: %w", err, fallbackErr)
	}
	if _, statErr := os.Stat(prepared.OutputPath); statErr == nil {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: original render left an output at %s; preserving it and the successful software fallback at %s", prepared.OutputPath, fallback.OutputPath)
	} else if !os.IsNotExist(statErr) {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: inspect original output %s before publishing software fallback: %w", prepared.OutputPath, statErr)
	}
	if renameErr := os.Rename(fallback.OutputPath, prepared.OutputPath); renameErr != nil {
		return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: publish software fallback %s as %s: %w", fallback.OutputPath, prepared.OutputPath, renameErr)
	}
	fallbackTiming := fallback.OutputPath + ".timing.json"
	if _, statErr := os.Stat(fallbackTiming); statErr == nil {
		if renameErr := os.Rename(fallbackTiming, prepared.OutputPath+".timing.json"); renameErr != nil {
			return RenderSessionOutcome{Elapsed: time.Since(started)}, fmt.Errorf("processor: publish software fallback timing sidecar: %w", renameErr)
		}
	}
	prepared.Metrics[metricnames.ChrononSoftwareFallback] = 1
	workerlog.ByJobID(prepared.Job.ID).Infof("software fallback completed and was published at %s", filepath.Base(prepared.OutputPath))
	return RenderSessionOutcome{SoftwareFallback: true, Elapsed: time.Since(started)}, nil
}

// TailOwnership is the explicit handoff of a staged upload. Commit is true only
// after the native gate passed and the output is final; the caller then either
// transfers the upload to PreparedJob or aborts it.
type TailOwnership struct {
	Upload *stagedUpload
	Commit bool
}

// CertifyAndHandoff records the render metrics, enforces the native gate and
// decides the tail-upload ownership. It never aborts the tail itself: the
// caller owns that decision, so no flag is captured by a closure.
func (p *Processor) CertifyAndHandoff(prepared *PreparedJob, policy RenderExecutionPolicy, outcome RenderSessionOutcome, observer *RenderProgressObserver, tail *stagedUpload) (TailOwnership, error) {
	phaseStart := time.Now().Add(-outcome.Elapsed)
	us := float64(outcome.Elapsed.Microseconds())
	prepared.Metrics[metricnames.RenderMS] = us / 1000
	prepared.Metrics[metricnames.RenderUS] = us
	p.recordPhase(metricnames.RenderStem, phaseStart)
	gapUS := p.recordGPUGap(phaseStart)
	prepared.Metrics[metricnames.GPUGapUS] = gapUS
	p.noteGPUGap(time.Duration(gapUS) * time.Microsecond)

	lastProgress, sawProgress := observer.Snapshot()
	metadata := planMetadataOf(prepared.Plan)
	renderedFrames := int64(metadata.FrameCount)
	if policy.FrameRange.Enabled {
		renderedFrames = policy.FrameRange.Last - policy.FrameRange.First + 1
	}
	renderedProgress := progressSummary(lastProgress.FramesDone, lastProgress.FramesTotal, lastProgress.FPS)
	if !sawProgress || lastProgress.FramesTotal <= 0 {
		renderedProgress = "not_reported"
	}
	workerlog.ByJobID(prepared.Job.ID).Statef("rendered", "elapsed_ms=%.1f frames=%d frames_done=%d frames_total=%d fps=%.2f progress=%s output=%s",
		us/1000, renderedFrames, lastProgress.FramesDone, lastProgress.FramesTotal, lastProgress.FPS,
		renderedProgress, filepath.Base(prepared.OutputPath))
	if sawProgress && lastProgress.FramesDone > 0 {
		prepared.Metrics[metricnames.RenderFramesDone] = float64(lastProgress.FramesDone)
		prepared.Metrics[metricnames.RenderFramesTotal] = float64(lastProgress.FramesTotal)
		fps := lastProgress.FPS
		if fps <= 0 {
			if elapsed := outcome.Elapsed.Seconds(); elapsed > 0 {
				fps = float64(lastProgress.FramesDone) / elapsed
			}
		}
		if fps > 0 {
			prepared.Metrics[metricnames.RenderFPS] = fps
		}
	}
	observer.Forget()

	if policy.StrictNative && policy.VideoSourceRequired && !policy.FinalComposite {
		if len(prepared.Segments) > 0 {
			if err := requireNativeVulkanTelemetry(prepared.OutputPath, int(renderedFrames)); err != nil {
				return TailOwnership{}, fmt.Errorf("processor: gpu-vulkan-native batch gate: %w", err)
			}
		} else if err := requireNativeVulkan(prepared.OutputPath, int(renderedFrames)); err != nil {
			return TailOwnership{}, fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
		prepared.NativeCertified = true
	}
	if tail != nil {
		return TailOwnership{Upload: tail, Commit: true}, nil
	}
	return TailOwnership{}, nil
}
