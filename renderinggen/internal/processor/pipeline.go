package processor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	Job             *queue.Job
	Workspace       *workspace.Workspace
	Plan            *overlay.Plan      // typed concrete Chronon plan (post-compile, marshaled once at WritePlan)
	Stats           overlay.Stats      // semantic counters for the ledger
	InputBytes      int64              // materialized input size for the ledger
	Metrics         map[string]float64 // phase metrics accumulated so far
	OutputPath      string
	AudioSourcePath string
	totalStart      time.Time
}

// normalizeMaterializedImagePaths makes the concrete Chronon path agree with
// the bytes that were actually downloaded. Some providers publish a JPEG
// behind a URL/metadata ending in .png; Chronon's image loader uses the file
// extension, so leaving that mismatch produces a valid but black render.
func normalizeMaterializedImagePaths(root string, plan *overlay.Plan) error {
	if plan == nil {
		return nil
	}
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.Type != "image" || layer.Asset == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(layer.Asset))
		data, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("image asset %s: %w", layer.Asset, err)
		}
		var sniff [512]byte
		n, readErr := data.Read(sniff[:])
		data.Close()
		if readErr != nil && n == 0 {
			return fmt.Errorf("image asset %s: %w", layer.Asset, readErr)
		}
		contentType := http.DetectContentType(sniff[:n])
		ext := ".png"
		switch contentType {
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		case "image/gif":
			ext = ".gif"
		case "image/png":
		default:
			continue
		}
		if strings.EqualFold(filepath.Ext(layer.Asset), ext) {
			continue
		}
		newAsset := strings.TrimSuffix(layer.Asset, filepath.Ext(layer.Asset)) + ext
		newPath := filepath.Join(root, filepath.FromSlash(newAsset))
		if err := os.Rename(path, newPath); err != nil {
			return fmt.Errorf("image asset %s rename: %w", layer.Asset, err)
		}
		layer.Asset = newAsset
	}
	return nil
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
	// Liveness marker for the stale-workspace sweeper: a prepared workspace
	// can sit idle until a GPU lane picks it up, and materialization or a
	// long render may write nothing new to the workspace directory tree for
	// more than the sweeper horizon. The marker is written at creation (not
	// after materialize) so even a >1h asset download is never swept, and
	// refreshed by the GPU lane for the whole RunGPU stage. Without a valid
	// marker, CleanupStale would RemoveAll a live job's directory.
	if err := ws.WriteLease(time.Now().Add(2 * time.Hour)); err != nil {
		// Fail closed: without the initial marker CleanupStale would treat an
		// active workspace as sweepable (its mtime can predate the sweeper
		// horizon while materialization or the GPU lane is still running), so
		// the liveness invariant must hold from the moment the workspace
		// exists. Occasional refresh failures during RunGPU stay tolerable
		// (the 2h TTL covers them); the missing first marker is not.
		_ = ws.Cleanup()
		return nil, fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
	}
	// inputBytes is the materialized input size, summed single-threaded
	// AFTER MaterializePaths completes from the on-disk files — race-free by
	// construction and accurate for every asset (cache hits and self-heals).
	var inputBytes int64
	phaseStart := time.Now()
	if err := ws.MaterializePaths(ctx, p.resolveAssetStreaming, assets); err != nil {
		_ = ws.Cleanup()
		return nil, err
	}
	// normalizeMaterializedImagePaths mutates the ONE typed plan in place —
	// no JSON round-trip.
	if err := normalizeMaterializedImagePaths(ws.Root(), plan); err != nil {
		_ = ws.Cleanup()
		return nil, err
	}
	// Re-derive the aggregate from the resolver outcomes: the streaming
	// resolver returns each asset's real size, so sum them via the resolved
	// asset list (single-threaded, race-free, counts every asset exactly once).
	for _, a := range assets {
		if info, statErr := os.Stat(filepath.Join(ws.Root(), a.LogicalPath)); statErr == nil {
			inputBytes += info.Size()
		}
	}
	record("materialize", phaseStart)

	// Burn verified ASS subtitles into Chronon text layers before the plan is
	// written. This keeps subtitles in the Vulkan composition and avoids a
	// second full-file ffmpeg encode after NVENC has finished.
	if subtitleHash, burn, ok, subtitleErr := overlay.SubtitleAsset(job.RenderPlan); subtitleErr != nil {
		_ = ws.Cleanup()
		return nil, subtitleErr
	} else if ok && burn {
		var subtitlePath, fontPath string
		for _, asset := range assets {
			if strings.EqualFold(asset.Hash, subtitleHash) {
				subtitlePath = filepath.Join(ws.Root(), asset.LogicalPath)
			}
			if ext := strings.ToLower(filepath.Ext(asset.LogicalPath)); ext == ".ttf" || ext == ".otf" {
				if fontPath == "" {
					fontPath = asset.LogicalPath
				}
			}
		}
		if subtitlePath == "" {
			_ = ws.Cleanup()
			return nil, fmt.Errorf("processor: burn subtitles asset %s was not materialized", subtitleHash)
		}
		if fontPath == "" {
			_ = ws.Cleanup()
			return nil, fmt.Errorf("processor: burn subtitles requires a materialized .ttf or .otf font")
		}
		burnStart := time.Now()
		subtitleCount := 0
		subtitleBytes, readErr := os.ReadFile(subtitlePath)
		if readErr != nil {
			_ = ws.Cleanup()
			return nil, fmt.Errorf("processor: read subtitles %s: %w", subtitlePath, readErr)
		}
		// Style + safe-area box are resolved from the plan's typed subtitle
		// block (SubtitleStyleAsset). The processor never invents typography.
		burnStyle, burnBox, styleErr := overlay.SubtitleStyleAsset(job.RenderPlan)
		if styleErr != nil {
			_ = ws.Cleanup()
			return nil, styleErr
		}
		if burnStyle == nil || burnBox.Width <= 0 || burnBox.Height <= 0 {
			_ = ws.Cleanup()
			return nil, fmt.Errorf("processor: burn subtitles requires a typed subtitle style block (font_size_px, width/height) in the plan")
		}
		if err := overlay.BurnASSIntoPlanTyped(plan, subtitleBytes, fontPath, burnStyle, burnBox); err != nil {
			_ = ws.Cleanup()
			return nil, err
		}
		metrics["subtitle_burn_us"] = float64(time.Since(burnStart).Microseconds())
		metrics["subtitle_burn_ms"] = metrics["subtitle_burn_us"] / 1000
		metrics["subtitle_layers"] = float64(subtitleCount)
		log.Printf("job %s: lowered %d ASS cues into Chronon GPU text layers", job.ID, subtitleCount)
	}

	phaseStart = time.Now()
	metadata := planMetadataOf(plan)
	if !p.nativeOutputProfiles && metadata.ProfileID != "" {
		plan.Output.ProfileID = ""
	}
	renderPlan, marshalErr := plan.Marshal()
	if marshalErr != nil {
		_ = ws.Cleanup()
		return nil, fmt.Errorf("processor: encode render plan: %w", marshalErr)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		_ = ws.Cleanup()
		return nil, err
	}
	record("plan", phaseStart)
	return &PreparedJob{
		Job:             job,
		Workspace:       ws,
		Plan:            plan,
		Stats:           stats,
		InputBytes:      inputBytes,
		Metrics:         metrics,
		OutputPath:      ws.OutputPath("result.mp4"),
		AudioSourcePath: audioSourcePathFromSemantic(job.RenderPlan, ws.Root()),
		totalStart:      totalStart,
	}, nil
}

// RunGPU performs the single Chronon invocation for a prepared job plus the
// strict-backend receipt gate. This is the only stage that touches the GPU.
func (p *Processor) RunGPU(ctx context.Context, prepared *PreparedJob) error {
	phaseStart := time.Now()
	job := prepared.Job
	metadata := planMetadataOf(prepared.Plan)
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
		Requirements: chronon.ExecutionRequirements{
			Backend:            p.backend,
			GPURequired:        gpuRequired,
			CPUFallbackAllowed: !p.strictNativeBackend,
			// A plain source clip can use Chronon's direct-YUV NVDEC→NVENC
			// path. Only plans with an authored visual layer require the full
			// Vulkan graph/native-surface handoff; treating every source clip as
			// a composition makes Chronon reject otherwise valid decoder frames.
			CompositionRequired: planHasVisualOverlay(prepared.Plan),
			VideoSourceRequired: planHasVideoSource(prepared.Plan),
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

// FinalizeJob runs the CPU-bound post half of the render pipeline: validation
// probes, output hashing (Chronon receipt first), object-store upload and the
// artifact ledger row.
func (p *Processor) FinalizeJob(ctx context.Context, prepared *PreparedJob) (queue.Artifact, error) {
	job := prepared.Job
	metrics := prepared.Metrics
	outputPath := prepared.OutputPath
	plan := prepared.Plan
	metadata := planMetadataOf(plan)
	var probe *media.ProbeResult
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probeStart := time.Now()
		probed, err := media.ProbeFile(ctx, outputPath)
		if err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
		}
		if err := media.ValidateDecoded(ctx, outputPath); err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: full decode gate: %w", err)
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
		if planHasVisualOverlay(plan) && deepVisualValidationEnabled() {
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

// deepVisualValidationEnabled reports whether the sampled ffmpeg visual
// validation should run for this job. Opt-in via RENDERINGGEN_DEEP_VISUAL=1:
// CI and certification runs enable it; the production hot path relies on the
// Chronon receipt gate (requireNativeVulkan + media receipt) and pays no
// extra ffmpeg processes per render.
func deepVisualValidationEnabled() bool {
	return os.Getenv("RENDERINGGEN_DEEP_VISUAL") == "1"
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

// PutGPURenderEnd stores the completion time of the most recent render.
// Exported so the concurrent GPU lane in the worker main can record render
// ends exactly like the serial StagedRender path does.
func PutGPURenderEnd(renderEnd time.Time) {
	gpuGap.mu.Lock()
	gpuGap.lastRenderEnd = renderEnd
	gpuGap.mu.Unlock()
}

// putGPURenderEnd is the internal alias used by StagedRender.
func putGPURenderEnd(renderEnd time.Time) { PutGPURenderEnd(renderEnd) }

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
