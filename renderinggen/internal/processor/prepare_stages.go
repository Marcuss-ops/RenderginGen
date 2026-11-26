// prepare_stages.go owns the phase functions of the CPU prepare half. They
// split PrepareJob's single body into the stages it actually has: compile,
// workspace materialization, subtitle burn + overlay-package rebuild, and plan
// serialization. The stage ORDER and the error taxonomy are part of the
// observable pipeline contract (pipeline_test.go pins them): a plan is gated
// before a workspace is created, assets are proven before the plan is written,
// and ownership of the workspace transfers only on the single success return.
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// addCompileTime accumulates one half of the split semantic compile so the
// single scene_compile ledger phase keeps describing it as a whole.
func (st *prepareState) addCompileTime(start time.Time) {
	st.compileDuration += time.Since(start)
}

// recordItemCounts books the cardinality counters from the ONE compile (or
// resolve) pass that produced this job's stats, so the artifact metrics can
// never drift from the layers that were emitted.
func (st *prepareState) recordItemCounts() {
	st.metrics[metricnames.OverlayItemCount] = float64(st.stats.SemanticItemCount)
	if st.stats.SemanticItemCount > 1 {
		st.metrics[metricnames.OverlayRenderBatch] = 1
	}
}

// prepareState is the mutable working set of one PrepareJob pass. Every stage
// takes it by pointer and mutates only its own fields, so a stage can be read
// in isolation instead of following a 350-line function's locals.
type prepareState struct {
	job        *queue.Job
	metrics    map[string]float64
	record     func(phase string, start time.Time)
	totalStart time.Time

	// compile
	result           overlay.CompileResult
	compiledSemantic bool
	// resolve is the un-lowered semantic plan of an ordinary job. It is
	// lowered exactly once, after the assets are materialized and probed, so
	// the document is never resolved twice and the plan is never compiled
	// blind and then recompiled. A prepared-package job leaves it nil: its
	// plan was compiled (with sizes) by render.prepare and is used verbatim.
	resolve *overlay.PreparedResolve
	// compileDuration accumulates the resolve and lower wall time, so the
	// scene_compile ledger phase keeps describing the whole semantic compile
	// after it was split in two.
	compileDuration time.Duration
	stats           overlay.Stats
	plan            *overlay.Plan
	compiledAssets  []overlay.Asset
	preparedPackage overlay.PreparedPackage
	assets          []queue.AssetRef

	// workspace
	ws           *workspace.Workspace
	renamed      map[string]string
	packageStale bool
	inputBytes   int64

	// plan serialization
	profileID             string
	audioPath             string
	audioTargetSampleRate int
	audioInert            bool
	segments              []chronon.OutputSegment
	segmentItemIDs        []string
}

// prepareCompile loads the immutable prepared package or RESOLVES the semantic
// plan, then gates the result before any workspace, asset download or GPU lane
// is spent. An ordinary job is only resolved here: lowering happens once, after
// the bytes exist, in lowerPreparedPlan.
func (p *Processor) prepareCompile(ctx context.Context, st *prepareState) error {
	job := st.job
	if job.PreparedRenderPackage != nil {
		// A prepared package is the compiler's immutable output for the whole
		// family: its plan was already compiled with the real image sizes by
		// render.prepare. Verify its source identities and reuse it verbatim —
		// recompiling it here discarded the package and cost a full compile.
		packageStart := time.Now()
		pkg, err := p.loadPreparedRenderPackage(ctx, job)
		if err != nil {
			return fmt.Errorf("processor: load prepared render package: %w", err)
		}
		st.plan = &overlay.Plan{}
		if err := json.Unmarshal(pkg.Plan, st.plan); err != nil {
			return fmt.Errorf("processor: decode compiled plan from prepared package: %w", err)
		}
		// The capability contract is transient on the render wire, so a package
		// loaded on another worker recovers it from the sidecar. An older
		// package carries the zero value and keeps the historical behaviour.
		st.plan.Features = pkg.Overlay.Features
		st.stats, st.preparedPackage = pkg.Stats, pkg.Overlay
		st.compiledAssets = make([]overlay.Asset, len(pkg.Assets))
		for i, asset := range pkg.Assets {
			st.compiledAssets[i] = overlay.Asset{Hash: asset.Hash, LogicalPath: asset.LogicalPath}
		}
		st.record(metricnames.PrepareSceneCompileStem, packageStart)
		// The package plan was compiled (with sizes) by render.prepare; reusing
		// it is the whole point of the immutable package.
		st.metrics[metricnames.OverlayFullCompileCount] = 0
	} else {
		// Resolve once. The same pass classifies the template_ids and the
		// audio policy, so the gates below run before a byte is downloaded.
		compileStart := time.Now()
		resolved, err := p.resolveSemanticForPrepare(job.RenderPlan)
		if err != nil {
			return err
		}
		st.addCompileTime(compileStart)
		st.resolve = resolved
		st.compiledSemantic = true
	}
	gatePlan := st.plan
	if st.resolve != nil {
		gatePlan = st.resolve.PlanShell()
	} else {
		// A prepared package already carries the compiler's counters.
		st.recordItemCounts()
	}
	if err := validateAudioMode(gatePlan); err != nil {
		return fmt.Errorf("processor: %w", err)
	}
	// UnknownTemplates is empty for every plan the compiler accepts: a non-empty
	// template_id that resolves to no registry row and no legacy alias is
	// REJECTED by resolveSemanticItems, not lowered as a preset-less primitive.
	// This check is the belt to that suspenders, so a future relaxation of the
	// compiler's gate can never become a rendered downgrade — a template_id the
	// resolve pass could not place is a prepare-boundary failure, before any
	// workspace, download, font stage or GPU lane is spent.
	if st.resolve != nil {
		if unknown := st.resolve.UnknownTemplates(); len(unknown) > 0 {
			st.metrics[metricnames.UnknownTemplates] = float64(len(unknown))
			return fmt.Errorf("processor: %d item template_id(s) resolved to no registry row and must not be rendered: %s",
				len(unknown), strings.Join(unknown, ", "))
		}
	}
	// The chunk contract is validated against the plan that will actually be
	// rendered, before any asset is downloaded: an out-of-range chunk is a
	// producer bug that must not burn a GPU lane and a lease to surface. The
	// sealed plan is validated again after lowering, where its duration is
	// authoritative.
	if err := validateFrameRange(job, gatePlan); err != nil {
		return err
	}
	// The compile half is one declared phase pair (overlay_compile_us/_ms,
	// the artifact ledger's columns) AND one Prometheus phase series: the same
	// record() that writes both spellings also feeds the worker's phase
	// histogram, so overlay_compile can never be visible in the ledger but
	// missing from `renderinggen_worker_phase_duration_seconds`.
	st.record(metricnames.OverlayCompileStem, st.totalStart)
	compiledAssets := st.compiledAssets
	if st.resolve != nil {
		compiledAssets = st.resolve.Assets()
	}
	assets, err := mergeAssets(job.Assets, compiledAssets)
	if err != nil {
		return err
	}
	st.assets = assets
	return nil
}

// prepareWorkspace creates the per-job directory, proves every referenced asset
// was materialized, and hands ownership of the workspace to the caller. On any
// failure INSIDE this stage it removes the scratch directory itself; PrepareJob
// installs the same guard from the moment this returns, so "an error path forgot
// to clean up" is impossible in both halves.
func (p *Processor) prepareWorkspace(ctx context.Context, st *prepareState) (err error) {
	job := st.job
	ws, err := workspace.New(p.jobsRoot, job.ID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			p.cleanupWorkspace(ws, job.ID)
		}
	}()
	if err := p.initializePrepareWorkspace(ws, job); err != nil {
		return err
	}
	phaseStart := time.Now()
	matStart := time.Now()
	if err := p.materializePrepareAssets(ctx, ws, st); err != nil {
		return err
	}
	if err := p.lowerPreparedPlan(ws, st); err != nil {
		return err
	}
	if err := p.normalizePreparedPlanAssets(ws, st); err != nil {
		return err
	}
	if err := validateMapRasterAssets(ws.Root(), st.plan); err != nil {
		return err
	}
	if err := p.prescalePreparedPlan(ctx, ws, st); err != nil {
		return err
	}
	if err := p.finishPrepareWorkspace(ctx, ws, st, phaseStart, matStart); err != nil {
		return err
	}
	st.ws = ws
	return nil
}

// initializePrepareWorkspace establishes the liveness marker and the per-job
// diagnostic log before any materialized bytes exist. A missing lease marker
// fails closed: without it CleanupStale could sweep a live job.
func (p *Processor) initializePrepareWorkspace(ws *workspace.Workspace, job *queue.Job) error {
	if err := ws.WriteLease(time.Now().Add(p.workspaceLeaseDuration())); err != nil {
		return fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
	}
	if _, logErr := workerlog.AttachJobLog(job.ID, filepath.Join(ws.Root(), "worker.log")); logErr != nil {
		// Fail-open: a job is never failed because a diagnostic file could not
		// be created, but the reason is recorded on the job's own record.
		workerlog.ByJobID(job.ID).Warnf("per-job workspace log unavailable: %v", logErr)
	}
	return nil
}

func (p *Processor) materializePrepareAssets(ctx context.Context, ws *workspace.Workspace, st *prepareState) error {
	resolvedSizes := make(map[string]int64, len(st.assets))
	var resolvedSizesMu sync.Mutex
	var totalResolveDur time.Duration
	wrappedResolve := func(rCtx context.Context, a queue.AssetRef) (workspace.ResolvedAsset, error) {
		rStart := time.Now()
		res, rErr := p.resolveAssetStreaming(rCtx, a)
		rDur := time.Since(rStart)
		if rErr == nil {
			resolvedSizesMu.Lock()
			resolvedSizes[a.LogicalPath] = res.SizeBytes
			totalResolveDur += rDur
			resolvedSizesMu.Unlock()
		}
		return res, rErr
	}
	if err := ws.MaterializePaths(ctx, wrappedResolve, st.assets); err != nil {
		return err
	}
	st.record(metricnames.PrepareAssetResolveStem, time.Now().Add(-totalResolveDur))
	for _, a := range st.assets {
		st.inputBytes += resolvedSizes[a.LogicalPath]
	}
	return nil
}

// lowerPreparedPlan is the ONE lowering pass of an ordinary job. Every asset is
// already materialized, so the images whose geometry derives from their source
// are probed and the plan is lowered once with the real dimensions — the
// historical blind compile plus sized recompile is gone.
//
// A prepared-package job has nothing to lower: its plan was compiled by
// render.prepare and is reused verbatim, so the full compile count for it is
// zero.
func (p *Processor) lowerPreparedPlan(ws *workspace.Workspace, st *prepareState) error {
	if st.resolve == nil {
		return nil
	}
	sizes, err := p.probePendingImageSizes(ws.Root(), st.resolve)
	if err != nil {
		return err
	}
	lowerStart := time.Now()
	result, err := st.resolve.LowerAndSeal(sizes)
	st.addCompileTime(lowerStart)
	if err != nil {
		return fmt.Errorf("processor: lower prepared plan: %w", err)
	}
	// The ledger's scene_compile phase covers the whole semantic compile, so
	// it is recorded with the accumulated resolve+lower duration.
	st.record(metricnames.PrepareSceneCompileStem, time.Now().Add(-st.compileDuration))
	st.result = result
	st.plan = result.Plan
	st.stats = result.Stats
	st.preparedPackage = result.Prepared
	st.compiledAssets = result.Assets
	st.metrics[metricnames.OverlayFullCompileCount] = 1
	st.recordItemCounts()
	// The sealed plan knows the duration the item end times produce, which the
	// pre-materialize gate could not: re-validate the chunk contract here.
	return validateFrameRange(st.job, st.plan)
}

// normalizePreparedPlanAssets makes the plan's image paths agree with the
// materialized bytes. It runs after lowering (the paths exist only then) and
// before the package is rebuilt, because a rename changes its asset manifest.
func (p *Processor) normalizePreparedPlanAssets(ws *workspace.Workspace, st *prepareState) error {
	renamed, err := normalizeMaterializedImagePaths(ws.Root(), st.plan)
	if err != nil {
		return err
	}
	st.renamed = renamed
	if len(renamed) > 0 {
		// Image sniffing may normalize a producer extension (for example
		// .jpeg to .jpg), so the concrete plan now points at the renamed path.
		workerlog.ByJobID(st.job.ID).Infof("normalized %d image asset extension(s) to match the verified bytes", len(renamed))
	}
	// The package is rebuilt once from the final plan; the renames and the
	// subtitle burn both make the previous build stale.
	st.packageStale = len(renamed) > 0
	return nil
}

func (p *Processor) prescalePreparedPlan(ctx context.Context, ws *workspace.Workspace, st *prepareState) error {
	prescaleStart := time.Now()
	sourceHashes := make(map[string]string, len(st.assets)+len(st.renamed))
	for _, asset := range st.assets {
		if asset.Hash != "" {
			sourceHashes[asset.LogicalPath] = asset.Hash
		}
	}
	// st.renamed is newPath -> producerPath; the plan references newPath while
	// st.assets still carries the producer path and its hash.
	for newPath, producerPath := range st.renamed {
		if hash := sourceHashes[producerPath]; hash != "" {
			sourceHashes[newPath] = hash
		}
	}
	prescaled, err := prescalePlanImages(ws.Root(), st.plan, filepath.Join(p.jobsRoot, ".prescaled-images"), sourceHashes)
	if err != nil {
		return err
	}
	if err := p.applyPrescaledImages(ctx, st, prescaled); err != nil {
		return err
	}
	if len(prescaled) > 0 {
		var before, after int64
		cached := 0
		for _, image := range prescaled {
			before += image.OriginalBytes
			after += int64(image.Bytes)
			if image.Cached {
				cached++
			}
		}
		workerlog.ByJobID(st.job.ID).Infof(
			"pre-scaled %d image(s) before Chronon: %d bytes -> %d bytes, cache_hits=%d, %dms",
			len(prescaled), before, after, cached, time.Since(prescaleStart).Milliseconds())
	}
	return materializeBuiltinFonts(ws.Root(), st.plan)
}

func (p *Processor) finishPrepareWorkspace(ctx context.Context, ws *workspace.Workspace, st *prepareState, phaseStart, matStart time.Time) error {
	proven := make(map[string]struct{}, len(st.assets)+len(st.renamed))
	for _, a := range st.assets {
		proven[a.LogicalPath] = struct{}{}
	}
	for name := range st.renamed {
		proven[name] = struct{}{}
	}
	if err := validateMaterializedPlanAssets(ws.Root(), st.plan, proven); err != nil {
		return err
	}
	st.record(metricnames.PrepareMaterializeStem, matStart)
	prefetchStart := time.Now()
	p.prefetchWarmAssets(ctx, st.job.ID, ws.Root(), st.assets)
	st.record(metricnames.PreparePrefetchStem, prefetchStart)
	st.record(metricnames.AssetMaterializeStem, phaseStart)
	return nil
}

// prepareSubtitles burns verified ASS subtitles into Chronon text layers before
// the plan is written. It keeps subtitles in the Vulkan composition and avoids
// a second full-file ffmpeg encode after NVENC has finished.
func (p *Processor) prepareSubtitles(st *prepareState) error {
	if !st.compiledSemantic || !st.result.Subtitle.Present || !st.result.Subtitle.Burn {
		return nil
	}
	job := st.job
	// The subtitle contract rides on the compile result (extracted by the SAME
	// decode pass that lowered the plan), so the raw semantic document is
	// never re-read on the prepare path.
	sub := st.result.Subtitle
	var subtitlePath, fontPath string
	for _, asset := range st.assets {
		if strings.EqualFold(asset.Hash, sub.Hash) {
			subtitlePath = filepath.Join(st.ws.Root(), asset.LogicalPath)
		}
		if ext := strings.ToLower(filepath.Ext(asset.LogicalPath)); ext == ".ttf" || ext == ".otf" {
			// The plan's declared subtitle font WINS: font_ref is the caller's
			// typography choice. The first-materialized fallback survives only
			// for plans that predate the field (empty FontHash).
			if sub.FontHash != "" && strings.EqualFold(asset.Hash, sub.FontHash) {
				fontPath = asset.LogicalPath
			} else if sub.FontHash == "" && fontPath == "" {
				fontPath = asset.LogicalPath
			}
		}
	}
	if subtitlePath == "" {
		return fmt.Errorf("processor: burn subtitles asset %s was not materialized", sub.Hash)
	}
	if fontPath == "" {
		return fmt.Errorf("processor: burn subtitles requires a materialized .ttf or .otf font")
	}
	burnStart := time.Now()
	subtitleBytes, err := os.ReadFile(subtitlePath)
	if err != nil {
		return fmt.Errorf("processor: read subtitles %s: %w", subtitlePath, err)
	}
	// Style + safe-area box are resolved from the plan's typed subtitle
	// block (ResolveSubtitleStyle, the compile-result view of
	// SubtitleStyleAsset). The processor never invents typography.
	burnStyle, burnBox, err := st.result.ResolveSubtitleStyle()
	if err != nil {
		return err
	}
	if burnStyle == nil || burnBox.Width <= 0 || burnBox.Height <= 0 {
		return fmt.Errorf("processor: burn subtitles requires a typed subtitle style block (font_size_px, width/height) in the plan")
	}
	subtitleCount, err := overlay.BurnASSIntoPlanTyped(st.plan, subtitleBytes, fontPath, burnStyle, burnBox)
	if err != nil {
		return err
	}
	// Burn-in appends concrete Chronon text layers to the plan, so the
	// sidecar must be rebuilt from the final plan: its overlay bindings have
	// to stay 1:1 with plan.Layers, which the Chronon boundary validates
	// before GPU compilation. The single rebuild below does it once.
	st.packageStale = true
	st.record(metricnames.PrepareBurnStem, burnStart)
	st.metrics[metricnames.SubtitleBurnUS] = float64(time.Since(burnStart).Microseconds())
	st.metrics[metricnames.SubtitleBurnMS] = st.metrics[metricnames.SubtitleBurnUS] / 1000
	// The cue count is the number of subtitle_cue_ layers actually present
	// in the plan after lowering (0 is possible when every cue was skipped
	// as empty or degenerate).
	st.metrics[metricnames.SubtitleLayers] = float64(subtitleCount)
	workerlog.ByJobID(job.ID).Infof("lowered %d ASS cues into Chronon GPU text layers", subtitleCount)
	return nil
}

// rebuildPreparedPackage rebuilds the sidecar exactly once from the final plan,
// when a rename, a fitted image box or the subtitle burn changed its identity.
func (p *Processor) rebuildPreparedPackage(st *prepareState) error {
	if !st.packageStale {
		return nil
	}
	preparedPackage, err := overlay.PreparePackage(st.plan, st.preparedPackage.Language, finalPreparedAssets(st.compiledAssets, st.renamed))
	if err != nil {
		return fmt.Errorf("processor: rebuild prepared overlay package from the final plan: %w", err)
	}
	st.preparedPackage = preparedPackage
	return nil
}

// preparePlanOutput serializes the ONE typed plan, writes the two workspace
// documents, and resolves the audio/segment facts RunGPU and the publisher need.
func (p *Processor) preparePlanOutput(st *prepareState) error {
	phaseStart := time.Now()
	marshalStart := time.Now()
	st.profileID = planMetadataOf(st.plan).ProfileID
	if !p.nativeOutputProfiles && st.profileID != "" {
		// The executed plan diverges from the accepted job's plan by design
		// (legacy runtimes reject unknown output properties), but the
		// divergence must never be silent: downstream consumers need to
		// distinguish "no profile requested" from "profile stripped by this
		// worker's config", and an operator needs to see that the strip
		// happened on every job it affects.
		st.plan.Output.ProfileID = ""
		st.metrics[metricnames.ProfileStrippedByConfig] = 1
		workerlog.ByJobID(st.job.ID).Warnf("output profile %q stripped (native_output_profiles=false); published artifact carries no profile_id", st.profileID)
	}
	renderPlan, err := st.plan.Marshal()
	if err != nil {
		return fmt.Errorf("processor: encode render plan: %w", err)
	}
	if err := st.ws.WritePlan(renderPlan); err != nil {
		return err
	}
	dumpCompiledPlan(st.job.ID, renderPlan)
	preparedBytes, err := json.Marshal(st.preparedPackage)
	if err != nil {
		return fmt.Errorf("processor: encode prepared overlay package: %w", err)
	}
	if err := st.ws.WritePreparedPackage(preparedBytes); err != nil {
		return fmt.Errorf("processor: write prepared overlay package: %w", err)
	}
	st.record(metricnames.PrepareMarshalStem, marshalStart)
	st.record(metricnames.PlanStem, phaseStart)
	st.audioPath, st.audioInert = audioSourcePathFromPlan(st.plan, st.ws.Root())
	st.audioTargetSampleRate = audioTargetSampleRateFromPlan(st.plan)
	// P4 batch superjob: a multi-item plan whose semantic item windows TILE
	// the canvas becomes ONE render session with one MP4 per item (the job's
	// OutputPath doubles as the session's whole-timeline mirror, so the fused
	// clip keeps its ordinary artifact contract). The trigger is deliberately
	// narrow — a transport that can carry per-item windows, whole-plan renders
	// only, no muxed source audio, no final-job composite — and everything
	// else keeps the historical single-output behavior unchanged.
	if st.job.FrameRange == nil && st.audioPath == "" && !isFinalJobComposite(st.job) {
		if segRenderer, ok := p.renderer.(chronon.OutputSegmentRenderer); ok && segRenderer.SupportsOutputSegments() {
			if segmentPlan, tileable := planSegmentOutputs(
				st.plan.ItemWindows, st.plan.Canvas.DurationFrames,
				filepath.Dir(st.ws.OutputPath("result.mp4"))); tileable {
				st.segments, st.segmentItemIDs = segmentPlan.Segments, segmentPlan.ItemIDs
				workerlog.ByJobID(st.job.ID).Infof(
					"P4 segmented output: %d semantic item windows tile the canvas — one session, %d per-item MP4s + the fused whole-timeline clip",
					len(st.segmentItemIDs), len(st.segments))
			}
		}
	}
	if st.audioInert {
		st.metrics[metricnames.AudioInertParams] = 1
		// CR-16: this is a per-clip factory fact, not an anomaly. At Warn it
		// produced one line per clip for a policy the plan already declares;
		// Debug keeps it available without flooding production logs.
		workerlog.ByJobID(st.job.ID).Debugf("audio codec/sample_rate/channels are inert (Chronon copies source audio, no transcode); mode=%q codec=%q sr=%d ch=%d",
			st.plan.Output.Audio.Mode, st.plan.Output.Audio.Codec, st.plan.Output.Audio.SampleRate, st.plan.Output.Audio.Channels)
	}
	return nil
}

// preparedJob projects the finished state into the value the GPU stage owns.
func (st *prepareState) preparedJob() *PreparedJob {
	return &PreparedJob{
		Job:                   st.job,
		Workspace:             st.ws,
		Plan:                  st.plan,
		ResolvedAssets:        append([]queue.AssetRef(nil), st.assets...),
		PreparedOverlay:       st.preparedPackage,
		Stats:                 st.stats,
		InputBytes:            st.inputBytes,
		Metrics:               st.metrics,
		OutputPath:            st.ws.OutputPath("result.mp4"),
		AudioSourcePath:       st.audioPath,
		AudioTargetSampleRate: st.audioTargetSampleRate,
		// Same condition as the OutputSpec flag in RunGPU: a SILENT artifact
		// (no source audio to mux) is the fragmented MP4 whose bytes are final
		// while appended — the container and the tail-upload lane must never
		// diverge, or the lane would stream a file whose header is backpatched.
		//
		// Canonicalized profiles are the exception: their bytes are rewritten
		// AFTER Chronon returns (the canonical avcC is an x264 identity, not an
		// NVENC one), so the render output must be a classic MP4 that can be
		// atomically replaced.
		// Streaming a fragmented file that is about to be re-encoded would
		// install bytes the receipt no longer describes.
		FragmentedMP4:  st.audioPath == "" && !isCanonicalizedOutputProfile(st.profileID),
		Segments:       st.segments,
		SegmentItemIDs: st.segmentItemIDs,
		totalStart:     st.totalStart,
	}
}
