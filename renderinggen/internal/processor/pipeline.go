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
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
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
	// NativeCertified is true only when Chronon's native Vulkan/NVENC receipt
	// gate certified a source-video execution. Image/text-only compositions
	// may use the GPU compositor, but their host-frame pipe handoff is not the
	// native-surface contract.
	NativeCertified bool
	totalStart      time.Time
}

// normalizeMaterializedImagePaths makes the concrete Chronon path agree with
// the bytes that were actually downloaded. Some providers publish a JPEG
// behind a URL/metadata ending in .png; Chronon's image loader uses the file
// extension, so leaving that mismatch produces a valid but black render.
//
// It returns the workspace-relative paths it created (the rename targets), the
// plan paths that are proven to exist once it returns nil. PrepareJob folds
// them into the set of paths this stage materialized so
// validateMaterializedPlanAssets does not stat a file that was just written.
func normalizeMaterializedImagePaths(root string, plan *overlay.Plan) (map[string]struct{}, error) {
	if plan == nil {
		return nil, nil
	}
	var renamed map[string]struct{}
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.Type != "image" || layer.Asset == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(layer.Asset))
		data, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("image asset %s: %w", layer.Asset, err)
		}
		var sniff [512]byte
		n, readErr := data.Read(sniff[:])
		data.Close()
		if readErr != nil && n == 0 {
			return nil, fmt.Errorf("image asset %s: %w", layer.Asset, readErr)
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
			return nil, fmt.Errorf("image asset %s rename: %w", layer.Asset, err)
		}
		if renamed == nil {
			renamed = make(map[string]struct{})
		}
		renamed[newAsset] = struct{}{}
		layer.Asset = newAsset
	}
	return renamed, nil
}

// validateMaterializedPlanAssets is the fail-closed boundary immediately
// before Chronon. MaterializePaths validates the queue manifest, but the
// compiler and image-extension normalization can change the concrete paths
// referenced by the typed plan. Check the plan itself so Chronon can never be
// the first component to report a missing asset.
//
// materialized is the set of workspace-relative paths this prepare stage itself
// created (every resolved manifest asset plus every normalize rename target).
// MaterializePaths returns nil only when all of them landed and the renames
// succeeded, so a plan path in that set existed moments ago and re-statting it
// is one syscall per unique asset of pure duplicate I/O. A plan path NOT in the
// set was not produced here, so it is still verified on the filesystem: the
// guarantee that Chronon is never the first to see a missing asset is
// unchanged for exactly the cases that can actually be missing.
func validateMaterializedPlanAssets(root string, plan *overlay.Plan, materialized map[string]struct{}) error {
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
		if _, proven := materialized[asset]; proven {
			continue
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
		// The two spellings are the declared phase pair; the vocabulary test
		// walks every recordPhase stem and fails on one that is not declared.
	}
	if err := validate(job); err != nil {
		return nil, err
	}
	// One compile pass produces the plan AND the ledger counters, so the
	// artifact metrics can never drift from the layers that were emitted.
	result, err := overlay.CompileSemantic(job.RenderPlan)
	if err != nil {
		return nil, err
	}
	stats, plan, compiledAssets := result.Stats, result.Plan, result.Assets
	// A template_id that resolved to no registry row is NOT an error (historical
	// documents and the compatibility aliases must keep rendering), but it must
	// never be invisible either: it is exactly the shape of a producer rename or
	// a dropped alias, both of which degrade the overlay to a preset-less text
	// primitive. The compile pass already classified it — this is the worker's
	// single observable projection of that fact.
	if len(result.UnknownTemplates) > 0 {
		metrics[metricnames.UnknownTemplates] = float64(len(result.UnknownTemplates))
		log.Printf("job %s: %d item template_id(s) resolved to no registry row and were compiled as preset-less primitives: %s",
			job.ID, len(result.UnknownTemplates), strings.Join(result.UnknownTemplates, ", "))
	}
	// The chunk contract is validated against the plan that will actually be
	// rendered, before any asset is downloaded: an out-of-range chunk is a
	// producer bug that must not burn a GPU lane and a lease to surface.
	if err := validateFrameRange(job, plan); err != nil {
		return nil, err
	}
	compileUS := float64(time.Since(totalStart).Microseconds())
	metrics[metricnames.OverlayCompileUS] = compileUS
	metrics[metricnames.OverlayCompileMS] = compileUS / 1000
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
		p.cleanupWorkspace(ws, job.ID)
		return nil, fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
	}
	var inputBytes int64
	phaseStart := time.Now()
	// Capture resolver sizes directly from the streaming resolver instead of
	// re-statting every materialized file after MaterializePaths. The resolver
	// already returns ResolvedAsset.SizeBytes (from L2/ContextPath or L3 header)
	// so a second os.Stat loop is pure duplicate I/O.
	resolvedSizes := make(map[string]int64, len(assets))
	var resolvedSizesMu sync.Mutex
	wrappedResolve := func(rCtx context.Context, a queue.AssetRef) (workspace.ResolvedAsset, error) {
		res, rErr := p.resolveAssetStreaming(rCtx, a)
		if rErr == nil {
			resolvedSizesMu.Lock()
			resolvedSizes[a.LogicalPath] = res.SizeBytes
			resolvedSizesMu.Unlock()
		}
		return res, rErr
	}
	if err := ws.MaterializePaths(ctx, wrappedResolve, assets); err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, err
	}
	for _, a := range assets {
		inputBytes += resolvedSizes[a.LogicalPath]
	}
	// normalizeMaterializedImagePaths mutates the ONE typed plan in place —
	// no JSON round-trip.
	renamed, err := normalizeMaterializedImagePaths(ws.Root(), plan)
	if err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, err
	}
	// Every path this stage created: each manifest asset MaterializePaths
	// resolved (it returns nil only when all of them landed) plus each rename
	// target normalization just wrote. The gate below still stats anything the
	// plan references that is NOT in here, so an asset the producer referenced
	// but never materialized is still caught before Chronon.
	proven := make(map[string]struct{}, len(assets)+len(renamed))
	for _, a := range assets {
		proven[a.LogicalPath] = struct{}{}
	}
	for name := range renamed {
		proven[name] = struct{}{}
	}
	if err := validateMaterializedPlanAssets(ws.Root(), plan, proven); err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, err
	}
	p.prefetchWarmAssets(ctx, ws.Root(), assets)
	// The phase name is the metric-name stem; "asset_materialize" matches the
	// artifact ledger's column and projection (asset_materialize_us), so one
	// phase has ONE name across the wire, the mirror and PostgreSQL.
	record(metricnames.AssetMaterializeStem, phaseStart)

	// Burn verified ASS subtitles into Chronon text layers before the plan is
	// written. This keeps subtitles in the Vulkan composition and avoids a
	// second full-file ffmpeg encode after NVENC has finished.
	if subtitleHash, burn, ok, subtitleErr := overlay.SubtitleAsset(job.RenderPlan); subtitleErr != nil {
		p.cleanupWorkspace(ws, job.ID)
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
			p.cleanupWorkspace(ws, job.ID)
			return nil, fmt.Errorf("processor: burn subtitles asset %s was not materialized", subtitleHash)
		}
		if fontPath == "" {
			p.cleanupWorkspace(ws, job.ID)
			return nil, fmt.Errorf("processor: burn subtitles requires a materialized .ttf or .otf font")
		}
		burnStart := time.Now()
		subtitleBytes, readErr := os.ReadFile(subtitlePath)
		if readErr != nil {
			p.cleanupWorkspace(ws, job.ID)
			return nil, fmt.Errorf("processor: read subtitles %s: %w", subtitlePath, readErr)
		}
		// Style + safe-area box are resolved from the plan's typed subtitle
		// block (SubtitleStyleAsset). The processor never invents typography.
		burnStyle, burnBox, styleErr := overlay.SubtitleStyleAsset(job.RenderPlan)
		if styleErr != nil {
			p.cleanupWorkspace(ws, job.ID)
			return nil, styleErr
		}
		if burnStyle == nil || burnBox.Width <= 0 || burnBox.Height <= 0 {
			p.cleanupWorkspace(ws, job.ID)
			return nil, fmt.Errorf("processor: burn subtitles requires a typed subtitle style block (font_size_px, width/height) in the plan")
		}
		subtitleCount, burnErr := overlay.BurnASSIntoPlanTyped(plan, subtitleBytes, fontPath, burnStyle, burnBox)
		if burnErr != nil {
			p.cleanupWorkspace(ws, job.ID)
			return nil, burnErr
		}
		metrics[metricnames.SubtitleBurnUS] = float64(time.Since(burnStart).Microseconds())
		metrics[metricnames.SubtitleBurnMS] = metrics[metricnames.SubtitleBurnUS] / 1000
		// The cue count is the number of subtitle_cue_ layers actually present
		// in the plan after lowering (0 is possible when every cue was skipped
		// as empty or degenerate).
		metrics[metricnames.SubtitleLayers] = float64(subtitleCount)
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
		metrics[metricnames.ProfileStrippedByConfig] = 1
		log.Printf("job %s: output profile %q stripped (native_output_profiles=false); published artifact carries no profile_id", job.ID, metadata.ProfileID)
	}
	renderPlan, marshalErr := plan.Marshal()
	if marshalErr != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, fmt.Errorf("processor: encode render plan: %w", marshalErr)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, err
	}
	record(metricnames.PlanStem, phaseStart)
	audioPath, warnInert := audioSourcePathFromPlan(plan, ws.Root())
	if warnInert {
		metrics[metricnames.AudioInertParams] = 1
		if plan.Output.Audio != nil && !audioModeCopyOnly(plan.Output.Audio.Mode) {
			// The caller asked for an audio policy the worker cannot execute
			// (the native mux copies the source stream). Name it separately:
			// "parameters ignored" and "the requested audio mode was not
			// applied" are different operational facts, and only the second
			// one changes what the published artifact sounds like.
			metrics[metricnames.AudioModeUnsupported] = 1
			log.Printf("job %s: requested audio mode %q is NOT applied (native mux copies the source stream); the artifact carries the source audio unchanged",
				job.ID, plan.Output.Audio.Mode)
		}
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

// prefetchWarmAssets primes Chronon's persistent image/video cache from the
// already verified workspace. The background and at most three image assets
// are selected deterministically, preserving every asset in the plan while
// bounding warm-up work on high-cardinality scenes. The selected IPC prefetch
// requests run concurrently: they are independent content-addressed assets and
// serial round-trips only extend the prepare→GPU handoff gap. A warm-up failure
// is diagnostic only: the materialized files remain authoritative for Render.
func (p *Processor) prefetchWarmAssets(ctx context.Context, root string, assets []queue.AssetRef) {
	if p == nil || p.assetPrefetcher == nil {
		return
	}
	selected := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	images := 0
	for _, asset := range assets {
		ext := strings.ToLower(filepath.Ext(asset.LogicalPath))
		isVideo := ext == ".mp4" || ext == ".mov" || ext == ".webm"
		isImage := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !isVideo && !isImage {
			continue
		}
		if isImage {
			if images >= 3 {
				continue
			}
			images++
		}
		path := filepath.Join(root, filepath.FromSlash(asset.LogicalPath))
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		selected = append(selected, path)
	}
	var wg sync.WaitGroup
	wg.Add(len(selected))
	for _, path := range selected {
		path := path
		go func() {
			defer wg.Done()
			if err := p.assetPrefetcher.PrefetchAsset(ctx, path); err != nil {
				log.Printf("chronon asset warm-up skipped: path=%s err=%v", path, err)
				return
			}
			log.Printf("chronon asset warm-up complete: path=%s", path)
		}()
	}
	wg.Wait()
}
