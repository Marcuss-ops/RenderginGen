package processor

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

// The staged pipeline splits the serial render path so CPU/IO work overlaps
// the GPU instead of serializing behind it:
//
//	PrepareJob  (CPU: validate, compile, materialize assets, write plan.json)
//	RunGPU      (GPU: the single Chronon invocation)
//	FinalizeJob (CPU: receipt gate, probe, hash, object store, ledger)
//
// The GPU lane runs RunGPU for job N while the prep pool runs PrepareJob for
// job N+1 and the post pool runs FinalizeJob for job N-1. Phase timings are
// identical to the monolithic Render path, so ledger metrics stay comparable.

// PreparedJob carries the state between PrepareJob and RunGPU. The workspace
// is intentionally NOT cleaned up by PrepareJob: ownership transfers to
// RunGPU, whose caller must call Cleanup after FinalizeJob.
type PreparedJob struct {
	Job        *queue.Job
	Workspace  *workspace.Workspace
	Plan       []byte             // concrete Chronon plan (post-compile)
	Stats      overlay.Stats      // semantic counters for the ledger
	InputBytes int64              // materialized input size for the ledger
	Metrics    map[string]float64 // phase metrics accumulated so far
	OutputPath string
	totalStart time.Time
}

// PrepareJob runs the CPU-bound preparation half of the render pipeline:
// validate, compile, asset materialization and plan.json. It returns a
// PreparedJob whose workspace must be cleaned up by the GPU-stage caller.
func (p *Processor) PrepareJob(ctx context.Context, job *queue.Job) (*PreparedJob, error) {
	totalStart := time.Now()
	metrics := make(map[string]float64, 8)
	record := func(phase string, start time.Time) {
		us := float64(time.Since(start).Microseconds())
		metrics[phase+"_ms"] = us / 1000
		metrics[phase+"_us"] = us
		p.recordPhase(phase, start)
	}
	if err := validate(job); err != nil {
		return nil, err
	}
	stats, err := overlay.SemanticStats(job.RenderPlan)
	if err != nil {
		return nil, fmt.Errorf("processor: semantic stats: %w", err)
	}
	plan, compiledAssets, _, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		return nil, err
	}
	compileUS := float64(time.Since(totalStart).Microseconds())
	metrics["overlay_compile_us"] = compileUS
	metrics["overlay_compile_ms"] = compileUS / 1000
	assets, err := mergeAssets(job.Assets, compiledAssets)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.New(p.jobsRoot, job.ID)
	if err != nil {
		return nil, err
	}
	var inputBytes int64
	phaseStart := time.Now()
	if err := ws.MaterializePaths(ctx, func(ctx context.Context, asset queue.AssetRef) (workspace.ResolvedAsset, error) {
		path, size, err := p.store.LocalPath(ctx, asset.Hash)
		if err != nil {
			data, resolveErr := p.resolveAsset(ctx, asset)
			if resolveErr != nil {
				return workspace.ResolvedAsset{}, resolveErr
			}
			inputBytes += int64(len(data))
			path, size, err = p.store.LocalPath(ctx, asset.Hash)
			if err != nil {
				return workspace.ResolvedAsset{}, err
			}
		}
		if size > 0 && inputBytes == 0 {
			inputBytes += size
		}
		return workspace.ResolvedAsset{LocalPath: path, SizeBytes: size}, nil
	}, assets); err != nil {
		_ = ws.Cleanup()
		return nil, err
	}
	record("materialize", phaseStart)

	phaseStart = time.Now()
	metadata := renderMetadataFromPlan(plan)
	renderPlan := plan
	if !p.nativeOutputProfiles && metadata.ProfileID != "" {
		renderPlan = stripOutputProfile(plan)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		_ = ws.Cleanup()
		return nil, err
	}
	record("plan", phaseStart)
	_ = metadata
	return &PreparedJob{
		Job:        job,
		Workspace:  ws,
		Plan:       plan,
		Stats:      stats,
		InputBytes: inputBytes,
		Metrics:    metrics,
		OutputPath: ws.OutputPath("result.mp4"),
		totalStart: totalStart,
	}, nil
}

// RunGPU performs the single Chronon invocation for a prepared job plus the
// strict-backend receipt gate. This is the only stage that touches the GPU.
func (p *Processor) RunGPU(ctx context.Context, prepared *PreparedJob) error {
	phaseStart := time.Now()
	job := prepared.Job
	metadata := renderMetadataFromPlan(prepared.Plan)
	gpuRequired := p.strictNativeBackend ||
		(p.backend == "vulkan" && p.hardwareEncoder != "" && p.hardwareEncoder != "none")
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
		AssetsRoot: prepared.Workspace.Root(),
		OutputPath: prepared.OutputPath,
		Report:     p.report,
		Requirements: chronon.ExecutionRequirements{
			GPURequired:         gpuRequired,
			CPUFallbackAllowed:  !p.strictNativeBackend,
			// A plain source clip can use Chronon's direct-YUV NVDEC→NVENC
			// path. Only plans with an authored visual layer require the full
			// Vulkan graph/native-surface handoff; treating every source clip as
			// a composition makes Chronon reject otherwise valid decoder frames.
			CompositionRequired: hasVisualOverlay(prepared.Plan),
			PacketCopyAllowed:   true,
		},
		FirstFrame:  frameStart(job),
		LastFrame:   frameEndInclusive(job),
		Output:      chronon.OutputSpec{Codec: "h264"},
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
	prepared.Metrics["gpu_gap_us"] = takeGPUGap(phaseStart)
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
	if p.strictNativeBackend {
		metadata := renderMetadataFromPlan(prepared.Plan)
		if err := requireNativeVulkan(prepared.OutputPath, metadata.FrameCount); err != nil {
			return fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
	}
	return nil
}

// FinalizeJob runs the CPU-bound post half of the render pipeline: validation
// probes, output hashing (Chronon receipt first), object-store upload and the
// artifact ledger row.
func (p *Processor) FinalizeJob(ctx context.Context, prepared *PreparedJob) (queue.Artifact, error) {
	job := prepared.Job
	metrics := prepared.Metrics
	outputPath := prepared.OutputPath
	plan := prepared.Plan
	metadata := renderMetadataFromPlan(plan)
	var probe *media.ProbeResult
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probeStart := time.Now()
		probed, err := media.ProbeFile(ctx, outputPath)
		if err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
		}
		if metadata.ProfileID != "" {
			profile, err := media.ResolveProfile(metadata.ProfileID)
			if err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile: %w", err)
			}
			if err := profile.ValidateProbe(probed); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile certification: %w", err)
			}
		} else if job.JobType == queue.JobTypeOverlayRender {
			if err := probed.ValidateOverlay(metadata.Width, metadata.Height, metadata.FPSNum, metadata.FPSDen); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay media contract: %w", err)
			}
		}
		if hasVisualOverlay(plan) {
			if err := probed.ValidateVisible(ctx, outputPath); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: visual output gate: %w", err)
			}
		}
		probe = &probed
		probeUS := float64(time.Since(probeStart).Microseconds())
		metrics["probe_us"] = probeUS
		metrics["probe_ms"] = probeUS / 1000
	}
	return p.storeArtifact(ctx, job.ID, outputPath, plan, metrics, prepared.totalStart, probe, prepared.Stats, prepared.InputBytes,
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "")
}

// gpuGap tracks the wall-clock gap between the end of one GPU render and the
// start of the next on this worker. It is the primary duty-cycle KPI: the
// true enemy is not render duration but the dead time between renders. The
// gap is recorded as a per-job metric (gpu_gap_us) on the following job.
var gpuGap struct {
	mu            sync.Mutex
	lastRenderEnd time.Time
}

// takeGPUGap closes the gap since the previous render end (0 on first use)
// and records this render's end for the next job.
func takeGPUGap(renderStart time.Time) float64 {
	gpuGap.mu.Lock()
	defer gpuGap.mu.Unlock()
	var gapUS float64
	if !gpuGap.lastRenderEnd.IsZero() && renderStart.After(gpuGap.lastRenderEnd) {
		gapUS = float64(renderStart.Sub(gpuGap.lastRenderEnd).Microseconds())
	}
	return gapUS
}

// putGPURenderEnd stores the completion time of the most recent render.
func putGPURenderEnd(renderEnd time.Time) {
	gpuGap.mu.Lock()
	gpuGap.lastRenderEnd = renderEnd
	gpuGap.mu.Unlock()
}

// StagedRender runs the three stages serially for one job; used when a caller
// wants the staged pipeline semantics without the concurrent worker pools
// (single-job mode, tests). Metrics and behavior match Render.
func (p *Processor) StagedRender(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	prepared, err := p.PrepareJob(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
		defer func() {
			if err := prepared.Workspace.Cleanup(); err != nil {
				log.Printf("job %s: workspace cleanup: %v", job.ID, err)
			}
		}()
	}
	if err := p.RunGPU(ctx, prepared); err != nil {
		return queue.Artifact{}, err
	}
	putGPURenderEnd(time.Now())
	return p.FinalizeJob(ctx, prepared)
}
