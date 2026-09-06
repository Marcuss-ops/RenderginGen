package processor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		subtitleCount, burnErr := overlay.BurnASSIntoPlanTyped(plan, subtitleBytes, fontPath, burnStyle, burnBox)
		if burnErr != nil {
			_ = ws.Cleanup()
			return nil, burnErr
		}
		metrics["subtitle_burn_us"] = float64(time.Since(burnStart).Microseconds())
		metrics["subtitle_burn_ms"] = metrics["subtitle_burn_us"] / 1000
		// The cue count is the number of subtitle_cue_ layers actually present
		// in the plan after lowering (0 is possible when every cue was skipped
		// as empty or degenerate).
		metrics["subtitle_layers"] = float64(subtitleCount)
		log.Printf("job %s: lowered %d ASS cues into Chronon GPU text layers", job.ID, subtitleCount)
	}

	phaseStart = time.Now()
	metadata := planMetadataOf(plan)
	if !p.nativeOutputProfiles && metadata.ProfileID != "" {
		// The executed plan diverges from the accepted job's plan by design
		// (legacy runtimes reject unknown output properties), but the
		// divergence must never be silent: downstream consumers need to
		// distinguish "no profile requested" from "profile stripped by this
		// worker's config", and an operator needs to see that the strip
		// happened on every job it affects.
		plan.Output.ProfileID = ""
		metrics["profile_stripped_by_config"] = 1
		log.Printf("job %s: output profile %q stripped (native_output_profiles=false); published artifact carries no profile_id", job.ID, metadata.ProfileID)
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
		probeUS := float64(time.Since(probeStart).Microseconds())
		metrics["probe_us"] = probeUS
		metrics["probe_ms"] = probeUS / 1000

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
	}
	// Media decodability is verified by Chronon — the canonical verifier of
	// the artifact Chronon itself produced — inside its receipt
	// (normal/certify run the full decode passes there; fast skips re-decode
	// by design). RenderingGen requests the policy and enforces the receipt
	// result; it never re-decodes the output a second time. This runs for
	// every finalized job, independent of overlay/profile probing.
	if err := p.enforceReceiptVerification(outputPath); err != nil {
		return queue.Artifact{}, err
	}
	return p.storeArtifact(ctx, job.ID, outputPath, plan, metrics, prepared.totalStart, probe, prepared.Stats, prepared.InputBytes,
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "")
}

type renderVerifyLevel string

const (
	renderVerifyFast    renderVerifyLevel = "fast"
	renderVerifyNormal  renderVerifyLevel = "normal"
	renderVerifyCertify renderVerifyLevel = "certify"
)

// renderVerificationLevel is the SINGLE verification-policy authority on the
// worker. fast (the production default) verifies container/stream metadata
// without re-decoding the freshly muxed output; normal performs one complete
// decode; certify is the explicit correctness mode used by CI/golden
// benchmarks. Only RENDERINGGEN_RECEIPT_VERIFY is read here — the historical
// CHRONON_RECEIPT_VERIFY alias is deliberately NOT consulted, so the worker
// and its Chronon subprocess cannot run different policies. The resolved
// level is forwarded explicitly to the CLI (chronon.RenderRequest.
// ReceiptVerify -> CHRONON_RECEIPT_VERIFY on the subprocess env) by RunGPU.
func renderVerificationLevel() renderVerifyLevel {
	switch renderVerifyLevel(os.Getenv("RENDERINGGEN_RECEIPT_VERIFY")) {
	case renderVerifyNormal:
		return renderVerifyNormal
	case renderVerifyCertify:
		return renderVerifyCertify
	default:
		return renderVerifyFast
	}
}

// enforceReceiptVerification makes the worker enforce — never duplicate —
// Chronon's canonical output verification. Under normal/certify the receipt
// is mandatory evidence: Chronon promised a full decode there, and a missing
// receipt means the canonical verifier did not run, so the artifact is
// rejected (fail closed) rather than silently accepted on the worker's own
// probe. Under fast a missing receipt is tolerated: the gate is the
// encoder/muxer success plus the profile/overlay probe validation below. When
// the receipt is present the aggregate verification status must be pass at
// every policy — Chronon's media checks (container, codec, pixel format,
// resolution, fps, audio, optional decode) are the verdict.
func (p *Processor) enforceReceiptVerification(outputPath string) error {
	receipt, err := chronon.ReadMediaReceipt(outputPath)
	policy := renderVerificationLevel()
	if err != nil {
		if policy == renderVerifyFast {
			// Fast never promises a decode; the output is certified by the
			// in-process encoder/muxer success and the probe validation below.
			return nil
		}
		return fmt.Errorf("processor: canonical verification did not run (policy=%s): %w", policy, err)
	}
	if !receipt.VerificationPassed() {
		return fmt.Errorf("processor: Chronon receipt verification failed (policy=%s status=%q)", policy, receipt.Verification.Status)
	}
	return nil
}

// deepVisualValidationEnabled reports whether the sampled ffmpeg visual
// validation should run for this job. Opt-in via RENDERINGGEN_DEEP_VISUAL=1:
// CI and certification runs enable it; the production hot path relies on the
// Chronon receipt gate (requireNativeVulkan + media receipt) and pays no
// extra ffmpeg processes per render.
func deepVisualValidationEnabled() bool {
	return os.Getenv("RENDERINGGEN_DEEP_VISUAL") == "1"
}

// The GPU duty-cycle KPI (gpu_gap_us) is scoped to the Processor instance, not
// to package-level state. The worker's GPU lanes and the serial StagedRender
// path share one Processor, so "the gap between one render's end and the next
// render's start on this worker" is measured per processor — never polluted by
// other Processors (e.g. tests running in the same process), and never
// misattributed across workers.

// recordGPUGap closes the gap since the previous render end recorded on this
// processor (0 on first use). RunGPU calls it with this render's start.
func (p *Processor) recordGPUGap(renderStart time.Time) float64 {
	p.gpuGapMu.Lock()
	defer p.gpuGapMu.Unlock()
	var gapUS float64
	if !p.gpuGapLastRenderEnd.IsZero() && renderStart.After(p.gpuGapLastRenderEnd) {
		gapUS = float64(renderStart.Sub(p.gpuGapLastRenderEnd).Microseconds())
	}
	return gapUS
}

// PutGPURenderEnd stores the completion time of the most recent render on this
// processor. Exported so the concurrent GPU lane in the worker main can record
// render ends exactly like the serial StagedRender path does.
func (p *Processor) PutGPURenderEnd(renderEnd time.Time) {
	p.gpuGapMu.Lock()
	p.gpuGapLastRenderEnd = renderEnd
	p.gpuGapMu.Unlock()
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
	p.PutGPURenderEnd(time.Now())
	return p.FinalizeJob(ctx, prepared)
}
