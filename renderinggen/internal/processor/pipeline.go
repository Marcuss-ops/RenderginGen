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
//
// File layout: pipeline.go owns the prepare half, gpu_run.go the GPU half,
// finalize_job.go + store_artifact.go + record_artifact.go the post half,
// receipt_verify.go the verification policy, native_gate.go the strict native
// receipt gate and staged_render.go the serial driver.
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

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

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

// validateMaterializedPlanAssets is the fail-closed boundary immediately
// before Chronon. MaterializePaths validates the queue manifest, but the
// compiler and image-extension normalization can change the concrete paths
// referenced by the typed plan. Check the plan itself so Chronon can never be
// the first component to report a missing asset.
func validateMaterializedPlanAssets(root string, plan *overlay.Plan) error {
	if plan == nil {
		return fmt.Errorf("processor: render plan is nil before Chronon")
	}
	seen := make(map[string]struct{})
	for _, layer := range plan.Layers {
		asset := strings.TrimSpace(layer.Asset)
		if asset == "" {
			continue
		}
		if _, ok := seen[asset]; ok {
			continue
		}
		seen[asset] = struct{}{}
		if filepath.IsAbs(asset) || filepath.Clean(asset) != asset || strings.HasPrefix(asset, "../") || asset == ".." {
			return fmt.Errorf("processor: Chronon asset path %q is not workspace-relative", asset)
		}
		path := filepath.Join(root, filepath.FromSlash(asset))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("processor: asset %q missing before Chronon: %w", asset, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("processor: asset %q is not a regular file before Chronon", asset)
		}
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
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after lease marker failure: %v", job.ID, cerr)
		}
		return nil, fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
	}
	var inputBytes int64
	phaseStart := time.Now()
	// Capture resolver sizes directly from the streaming resolver instead of
	// re-statting every materialized file after MaterializePaths. The resolver
	// already returns ResolvedAsset.SizeBytes (from L2/ContextPath or L3 header)
	// so a second os.Stat loop is pure duplicate I/O.
	resolvedSizes := make(map[string]int64, len(assets))
	wrappedResolve := func(rCtx context.Context, a queue.AssetRef) (workspace.ResolvedAsset, error) {
		res, rErr := p.resolveAssetStreaming(rCtx, a)
		if rErr == nil {
			resolvedSizes[a.LogicalPath] = res.SizeBytes
		}
		return res, rErr
	}
	if err := ws.MaterializePaths(ctx, wrappedResolve, assets); err != nil {
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after materialize failure: %v", job.ID, cerr)
		}
		return nil, err
	}
	for _, a := range assets {
		inputBytes += resolvedSizes[a.LogicalPath]
	}
	// normalizeMaterializedImagePaths mutates the ONE typed plan in place —
	// no JSON round-trip.
	if err := normalizeMaterializedImagePaths(ws.Root(), plan); err != nil {
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after image normalize failure: %v", job.ID, cerr)
		}
		return nil, err
	}
	if err := validateMaterializedPlanAssets(ws.Root(), plan); err != nil {
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after asset validation failure: %v", job.ID, cerr)
		}
		return nil, err
	}
	record("materialize", phaseStart)

	// Burn verified ASS subtitles into Chronon text layers before the plan is
	// written. This keeps subtitles in the Vulkan composition and avoids a
	// second full-file ffmpeg encode after NVENC has finished.
	if subtitleHash, burn, ok, subtitleErr := overlay.SubtitleAsset(job.RenderPlan); subtitleErr != nil {
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after subtitle asset check failure: %v", job.ID, cerr)
		}
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
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after missing subtitle path: %v", job.ID, cerr)
			}
			return nil, fmt.Errorf("processor: burn subtitles asset %s was not materialized", subtitleHash)
		}
		if fontPath == "" {
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after missing font: %v", job.ID, cerr)
			}
			return nil, fmt.Errorf("processor: burn subtitles requires a materialized .ttf or .otf font")
		}
		burnStart := time.Now()
		subtitleBytes, readErr := os.ReadFile(subtitlePath)
		if readErr != nil {
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after subtitle read failure: %v", job.ID, cerr)
			}
			return nil, fmt.Errorf("processor: read subtitles %s: %w", subtitlePath, readErr)
		}
		// Style + safe-area box are resolved from the plan's typed subtitle
		// block (SubtitleStyleAsset). The processor never invents typography.
		burnStyle, burnBox, styleErr := overlay.SubtitleStyleAsset(job.RenderPlan)
		if styleErr != nil {
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after subtitle style failure: %v", job.ID, cerr)
			}
			return nil, styleErr
		}
		if burnStyle == nil || burnBox.Width <= 0 || burnBox.Height <= 0 {
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after subtitle style validation: %v", job.ID, cerr)
			}
			return nil, fmt.Errorf("processor: burn subtitles requires a typed subtitle style block (font_size_px, width/height) in the plan")
		}
		subtitleCount, burnErr := overlay.BurnASSIntoPlanTyped(plan, subtitleBytes, fontPath, burnStyle, burnBox)
		if burnErr != nil {
			if cerr := ws.Cleanup(); cerr != nil {
				log.Printf("job %s: workspace cleanup after subtitle burn failure: %v", job.ID, cerr)
			}
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
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after plan marshal failure: %v", job.ID, cerr)
		}
		return nil, fmt.Errorf("processor: encode render plan: %w", marshalErr)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		if cerr := ws.Cleanup(); cerr != nil {
			log.Printf("job %s: workspace cleanup after write plan failure: %v", job.ID, cerr)
		}
		return nil, err
	}
	record("plan", phaseStart)
	audioPath, warnInert := audioSourcePathFromPlan(plan, job.RenderPlan, ws.Root())
	if warnInert {
		metrics["audio_inert_params"] = 1
		log.Printf("job %s: audio codec/sample_rate/channels are inert (Chronon copies source audio, no transcode); mode=%q codec=%q sr=%d ch=%d",
			job.ID, plan.Output.Audio.Mode, plan.Output.Audio.Codec, plan.Output.Audio.SampleRate, plan.Output.Audio.Channels)
	}
	return &PreparedJob{
		Job:             job,
		Workspace:       ws,
		Plan:            plan,
		Stats:           stats,
		InputBytes:      inputBytes,
		Metrics:         metrics,
		OutputPath:      ws.OutputPath("result.mp4"),
		AudioSourcePath: audioPath,
		totalStart:      totalStart,
	}, nil
}
