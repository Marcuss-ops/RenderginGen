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
	stats            overlay.Stats
	plan             *overlay.Plan
	compiledAssets   []overlay.Asset
	preparedPackage  overlay.PreparedPackage
	assets           []queue.AssetRef

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

// prepareCompile loads the immutable prepared package or runs the semantic
// compiler, then gates the result before any workspace, asset download or GPU
// lane is spent.
func (p *Processor) prepareCompile(ctx context.Context, st *prepareState) error {
	job := st.job
	if job.PreparedRenderPackage != nil {
		// A prepared package is the compiler's immutable output for the whole
		// family. Verify its source identities before using it, then stage its
		// content-addressed assets into this worker's own workspace.
		packageStart := time.Now()
		pkg, err := p.loadPreparedRenderPackage(ctx, job)
		if err != nil {
			return fmt.Errorf("processor: load prepared render package: %w", err)
		}
		st.plan = &overlay.Plan{}
		if err := json.Unmarshal(pkg.Plan, st.plan); err != nil {
			return fmt.Errorf("processor: decode compiled plan from prepared package: %w", err)
		}
		st.stats, st.preparedPackage = pkg.Stats, pkg.Overlay
		st.compiledAssets = make([]overlay.Asset, len(pkg.Assets))
		for i, asset := range pkg.Assets {
			st.compiledAssets[i] = overlay.Asset{Hash: asset.Hash, LogicalPath: asset.LogicalPath}
		}
		st.record(metricnames.PrepareSceneCompileStem, packageStart)
	} else {
		// One compile pass produces the plan AND the ledger counters, so the
		// artifact metrics can never drift from the layers that were emitted.
		compileStart := time.Now()
		result, err := p.compileSemantic(job.RenderPlan)
		if err != nil {
			return err
		}
		st.record(metricnames.PrepareSceneCompileStem, compileStart)
		st.compiledSemantic = true
		st.result = result
		st.stats, st.plan, st.compiledAssets = result.Stats, result.Plan, result.Assets
		st.preparedPackage = result.Prepared
	}
	st.metrics[metricnames.OverlayItemCount] = float64(st.stats.SemanticItemCount)
	if st.stats.SemanticItemCount > 1 {
		st.metrics[metricnames.OverlayRenderBatch] = 1
	}
	// UnknownTemplates is empty for every plan the compiler accepts: a non-empty
	// template_id that resolves to no registry row and no legacy alias is
	// REJECTED by resolveSemanticItems, not lowered as a preset-less primitive.
	// This check is the belt to that suspenders, so a future relaxation of the
	// compiler's gate can never become a rendered downgrade — a template_id the
	// compile pass could not place is a prepare-boundary failure, before any
	// workspace, download, font stage or GPU lane is spent.
	if st.compiledSemantic && len(st.result.UnknownTemplates) > 0 {
		st.metrics[metricnames.UnknownTemplates] = float64(len(st.result.UnknownTemplates))
		return fmt.Errorf("processor: %d item template_id(s) resolved to no registry row and must not be rendered: %s",
			len(st.result.UnknownTemplates), strings.Join(st.result.UnknownTemplates, ", "))
	}
	// The chunk contract is validated against the plan that will actually be
	// rendered, before any asset is downloaded: an out-of-range chunk is a
	// producer bug that must not burn a GPU lane and a lease to surface.
	if err := validateFrameRange(job, st.plan); err != nil {
		return err
	}
	// The compile half is one declared phase pair (overlay_compile_us/_ms,
	// the artifact ledger's columns) AND one Prometheus phase series: the same
	// record() that writes both spellings also feeds the worker's phase
	// histogram, so overlay_compile can never be visible in the ledger but
	// missing from `renderinggen_worker_phase_duration_seconds`.
	st.record(metricnames.OverlayCompileStem, st.totalStart)
	assets, err := mergeAssets(job.Assets, st.compiledAssets)
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
	// Liveness marker for the stale-workspace sweeper: a prepared workspace
	// can sit idle until a GPU lane picks it up, and materialization or a
	// long render may write nothing new to the workspace directory tree for
	// more than the sweeper horizon. The marker is written at creation (not
	// after materialize) so even a >1h asset download is never swept, and
	// refreshed by the GPU lane for the whole RunGPU stage. Without a valid
	// marker, CleanupStale would RemoveAll a live job's directory.
	//
	// The window comes from pipeline.workspace_lease_ttl (see
	// processor.Options.WorkspaceLeaseTTL); config guarantees the TTL exceeds
	// the refresh period, so one missed refresh can never make a live render
	// sweepable.
	if err := ws.WriteLease(time.Now().Add(p.workspaceLeaseDuration())); err != nil {
		// Fail closed: without the initial marker CleanupStale would treat an
		// active workspace as sweepable (its mtime can predate the sweeper
		// horizon while materialization or the GPU lane is still running), so
		// the liveness invariant must hold from the moment the workspace
		// exists. Occasional refresh failures during RunGPU stay tolerable
		// (the TTL covers them); the missing first marker is not.
		return fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
	}
	// Per-job log file, live inside the workspace this job owns: it is what an
	// operator (and the collector) reads while the job runs, and what survives
	// on disk when pipeline.keep_workspace is set. The durable copy under the
	// jobs root was attached at claim, so this file starts at the prepare stage
	// while the durable one already holds the claim lines.
	if _, logErr := workerlog.AttachJobLog(job.ID, filepath.Join(ws.Root(), "worker.log")); logErr != nil {
		// Fail-open: a job is never failed because a diagnostic file could not
		// be created, but the reason is recorded on the job's own record.
		workerlog.ByJobID(job.ID).Warnf("per-job workspace log unavailable: %v", logErr)
	}
	phaseStart := time.Now()
	matStart := time.Now()
	// Capture resolver sizes directly from the streaming resolver instead of
	// re-statting every materialized file after MaterializePaths. The resolver
	// already returns ResolvedAsset.SizeBytes (from L2/ContextPath or L3 header)
	// so a second os.Stat loop is pure duplicate I/O.
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
	// normalizeMaterializedImagePaths mutates the ONE typed plan in place —
	// no JSON round-trip.
	renamed, err := normalizeMaterializedImagePaths(ws.Root(), st.plan)
	if err != nil {
		return err
	}
	st.renamed = renamed
	if len(renamed) > 0 {
		// Image sniffing may normalize a producer extension (for example
		// .jpeg to .jpg), so the concrete plan now points at the renamed path
		// and the compiler's package describes the old one.
		workerlog.ByJobID(job.ID).Infof("normalized %d image asset extension(s) to match the verified bytes", len(renamed))
	}
	// Everything from here to the package build can change what the prepared
	// sidecar must contain: the normalized asset path, an entity image's fitted
	// size (part of the preparation identity) and the burned subtitle layers.
	// The mutations are RECORDED and the package is rebuilt exactly ONCE from
	// the final plan. The historical shape rebuilt it after the renames and
	// again after the burn, so a job that did both paid for two full builds and
	// the sidecar in between described a plan state no frame ever rendered.
	st.packageStale = len(renamed) > 0
	fitChanged, err := fitEntityImageLayersToAssets(ws.Root(), st.plan)
	if err != nil {
		return err
	}
	st.packageStale = st.packageStale || fitChanged
	if err := validateMapRasterAssets(ws.Root(), st.plan); err != nil {
		return err
	}
	if err := materializeBuiltinFonts(ws.Root(), st.plan); err != nil {
		return err
	}
	// Every path this stage created: each manifest asset MaterializePaths
	// resolved (it returns nil only when all of them landed) plus each rename
	// target normalization just wrote. The gate below still stats anything the
	// plan references that is NOT in here, so an asset the producer referenced
	// but never materialized is still caught before Chronon.
	proven := make(map[string]struct{}, len(st.assets)+len(renamed))
	for _, a := range st.assets {
		proven[a.LogicalPath] = struct{}{}
	}
	for name := range renamed {
		proven[name] = struct{}{}
	}
	if err := validateMaterializedPlanAssets(ws.Root(), st.plan, proven); err != nil {
		return err
	}
	st.record(metricnames.PrepareMaterializeStem, matStart)
	prefetchStart := time.Now()
	p.prefetchWarmAssets(ctx, job.ID, ws.Root(), st.assets)
	st.record(metricnames.PreparePrefetchStem, prefetchStart)
	// The phase name is the metric-name stem; "asset_materialize" matches the
	// artifact ledger's column and projection (asset_materialize_us), so one
	// phase has ONE name across the wire, the mirror and PostgreSQL.
	st.record(metricnames.AssetMaterializeStem, phaseStart)
	st.ws = ws
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
			if fontPath == "" {
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
		if st.plan.Output.Audio != nil && !audioModeCopyOnly(st.plan.Output.Audio.Mode) {
			// The caller asked for an audio policy the worker cannot execute
			// (the native mux copies the source stream). Name it separately:
			// "parameters ignored" and "the requested audio mode was not
			// applied" are different operational facts, and only the second
			// one changes what the published artifact sounds like.
			st.metrics[metricnames.AudioModeUnsupported] = 1
			workerlog.ByJobID(st.job.ID).Warnf("requested audio mode %q is NOT applied (native mux copies the source stream); the artifact carries the source audio unchanged",
				st.plan.Output.Audio.Mode)
		}
		workerlog.ByJobID(st.job.ID).Warnf("audio codec/sample_rate/channels are inert (Chronon copies source audio, no transcode); mode=%q codec=%q sr=%d ch=%d",
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
