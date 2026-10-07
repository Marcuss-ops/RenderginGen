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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
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
	// AudioTargetSampleRate is the OUTPUT audio rate the sealed plan declares
	// (0 when it declares none). Forwarded to Chronon's mux, which transcodes
	// ONLY when the source rate differs, so a matching source keeps the
	// byte-identical copy path.
	AudioTargetSampleRate int
	// NativeCertified is true only when Chronon's native Vulkan/NVENC receipt
	// gate certified a source-video execution. Image/text-only compositions
	// may use the GPU compositor, but their host-frame pipe handoff is not the
	// native-surface contract.
	NativeCertified bool
	totalStart      time.Time
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
	// Bind the job to its MASTER run id before anything else can log: every
	// later line for this job (here, in the GPU lane and in the post pool) is
	// then greppable by the parent_job_id the master stamped on the child job
	// (see internal/workerlog). Binding is idempotent, so the serial Render
	// path and the concurrent pools can both do it. It happens AFTER validate
	// because a nil/invalid job never reaches the pipeline (validate rejects
	// it), and binding would dereference it.
	workerlog.BindJob(job.ParentJobID, job.ID)
	// One compile pass produces the plan AND the ledger counters, so the
	// artifact metrics can never drift from the layers that were emitted.
	compileStart := time.Now()
	result, err := overlay.CompileSemantic(job.RenderPlan)
	if err != nil {
		return nil, err
	}
	record(metricnames.PrepareSceneCompileStem, compileStart)
	stats, plan, compiledAssets, preparedPackage := result.Stats, result.Plan, result.Assets, result.Prepared
	// A template_id that resolved to no registry row is NOT an error (historical
	// documents and the compatibility aliases must keep rendering), but it must
	// never be invisible either: it is exactly the shape of a producer rename or
	// a dropped alias, both of which degrade the overlay to a preset-less text
	// primitive. The compile pass already classified it — this is the worker's
	// single observable projection of that fact.
	if len(result.UnknownTemplates) > 0 {
		metrics[metricnames.UnknownTemplates] = float64(len(result.UnknownTemplates))
		workerlog.ByJobID(job.ID).Warnf("%d item template_id(s) resolved to no registry row and were compiled as preset-less primitives: %s",
			len(result.UnknownTemplates), strings.Join(result.UnknownTemplates, ", "))
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
	// This stage OWNS the workspace until it hands the prepared job to the GPU
	// lane. Registering the cleanup guard once makes "an error path forgot to
	// remove the workspace" impossible: the historical shape repeated an
	// explicit cleanupWorkspace call on ~20 return sites, so a new error path
	// that omitted it leaked a scratch directory — invisible except as RAM
	// exhaustion on the (frequently tmpfs) jobs root. Ownership transfers only
	// on the single success return, which clears owned first.
	owned := true
	defer func() {
		if owned {
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
		return nil, fmt.Errorf("processor: establish workspace lease for %s: %w", job.ID, err)
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
	var inputBytes int64
	phaseStart := time.Now()
	matStart := time.Now()
	// Capture resolver sizes directly from the streaming resolver instead of
	// re-statting every materialized file after MaterializePaths. The resolver
	// already returns ResolvedAsset.SizeBytes (from L2/ContextPath or L3 header)
	// so a second os.Stat loop is pure duplicate I/O.
	resolvedSizes := make(map[string]int64, len(assets))
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
	if err := ws.MaterializePaths(ctx, wrappedResolve, assets); err != nil {
		return nil, err
	}
	record(metricnames.PrepareAssetResolveStem, time.Now().Add(-totalResolveDur))
	for _, a := range assets {
		inputBytes += resolvedSizes[a.LogicalPath]
	}
	// normalizeMaterializedImagePaths mutates the ONE typed plan in place —
	// no JSON round-trip.
	renamed, err := normalizeMaterializedImagePaths(ws.Root(), plan)
	if err != nil {
		return nil, err
	}
	if len(renamed) > 0 {
		// Image sniffing may normalize a producer extension (for example
		// .jpeg to .jpg). The concrete plan now points at the renamed path;
		// rebuild the prepared sidecar from that same plan so Chronon's strict
		// package-to-layer validation sees identical logical paths.
		preparedPackage, err = overlay.PreparePackage(plan, preparedPackage.Language, finalPreparedAssets(compiledAssets, renamed))
		if err != nil {
			return nil, fmt.Errorf("processor: rebuild prepared overlay package after image path normalization: %w", err)
		}
	}
	if err := fitEntityImageLayersToAssets(ws.Root(), plan); err != nil {
		return nil, err
	}
	if err := validateMapRasterAssets(ws.Root(), plan); err != nil {
		return nil, err
	}
	if err := materializeBuiltinFonts(ws.Root(), plan); err != nil {
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
		return nil, err
	}
	record(metricnames.PrepareMaterializeStem, matStart)
	prefetchStart := time.Now()
	p.prefetchWarmAssets(ctx, job.ID, ws.Root(), assets)
	record(metricnames.PreparePrefetchStem, prefetchStart)
	// The phase name is the metric-name stem; "asset_materialize" matches the
	// artifact ledger's column and projection (asset_materialize_us), so one
	// phase has ONE name across the wire, the mirror and PostgreSQL.
	record(metricnames.AssetMaterializeStem, phaseStart)

	// Burn verified ASS subtitles into Chronon text layers before the plan is
	// written. This keeps subtitles in the Vulkan composition and avoids a
	// second full-file ffmpeg encode after NVENC has finished.
	if subtitleHash, burn, ok, subtitleErr := overlay.SubtitleAsset(job.RenderPlan); subtitleErr != nil {
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
			return nil, fmt.Errorf("processor: burn subtitles asset %s was not materialized", subtitleHash)
		}
		if fontPath == "" {
			return nil, fmt.Errorf("processor: burn subtitles requires a materialized .ttf or .otf font")
		}
		burnStart := time.Now()
		subtitleBytes, readErr := os.ReadFile(subtitlePath)
		if readErr != nil {
			return nil, fmt.Errorf("processor: read subtitles %s: %w", subtitlePath, readErr)
		}
		// Style + safe-area box are resolved from the plan's typed subtitle
		// block (SubtitleStyleAsset). The processor never invents typography.
		burnStyle, burnBox, styleErr := overlay.SubtitleStyleAsset(job.RenderPlan)
		if styleErr != nil {
			return nil, styleErr
		}
		if burnStyle == nil || burnBox.Width <= 0 || burnBox.Height <= 0 {
			return nil, fmt.Errorf("processor: burn subtitles requires a typed subtitle style block (font_size_px, width/height) in the plan")
		}
		subtitleCount, burnErr := overlay.BurnASSIntoPlanTyped(plan, subtitleBytes, fontPath, burnStyle, burnBox)
		if burnErr != nil {
			return nil, burnErr
		}
		// Burn-in appends concrete Chronon text layers after CompileSemantic
		// created the initial package. Rebuild the immutable sidecar from the
		// final plan so its overlay bindings remain 1:1 with plan.Layers; the
		// Chronon boundary validates this relationship before GPU compilation.
		preparedPackage, err = overlay.PreparePackage(plan, preparedPackage.Language, finalPreparedAssets(compiledAssets, renamed))
		if err != nil {
			return nil, fmt.Errorf("processor: rebuild prepared overlay package after subtitle burn: %w", err)
		}
		record(metricnames.PrepareBurnStem, burnStart)
		metrics[metricnames.SubtitleBurnUS] = float64(time.Since(burnStart).Microseconds())
		metrics[metricnames.SubtitleBurnMS] = metrics[metricnames.SubtitleBurnUS] / 1000
		// The cue count is the number of subtitle_cue_ layers actually present
		// in the plan after lowering (0 is possible when every cue was skipped
		// as empty or degenerate).
		metrics[metricnames.SubtitleLayers] = float64(subtitleCount)
		workerlog.ByJobID(job.ID).Infof("lowered %d ASS cues into Chronon GPU text layers", subtitleCount)
	}

	phaseStart = time.Now()
	marshalStart := time.Now()
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
		workerlog.ByJobID(job.ID).Warnf("output profile %q stripped (native_output_profiles=false); published artifact carries no profile_id", metadata.ProfileID)
	}
	renderPlan, marshalErr := plan.Marshal()
	if marshalErr != nil {
		return nil, fmt.Errorf("processor: encode render plan: %w", marshalErr)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		return nil, err
	}
	preparedBytes, preparedErr := json.Marshal(preparedPackage)
	if preparedErr != nil {
		return nil, fmt.Errorf("processor: encode prepared overlay package: %w", preparedErr)
	}
	if err := ws.WritePreparedPackage(preparedBytes); err != nil {
		return nil, fmt.Errorf("processor: write prepared overlay package: %w", err)
	}
	record(metricnames.PrepareMarshalStem, marshalStart)
	record(metricnames.PlanStem, phaseStart)
	audioPath, warnInert := audioSourcePathFromPlan(plan, ws.Root())
	audioTargetSampleRate := audioTargetSampleRateFromPlan(plan)
	if warnInert {
		metrics[metricnames.AudioInertParams] = 1
		if plan.Output.Audio != nil && !audioModeCopyOnly(plan.Output.Audio.Mode) {
			// The caller asked for an audio policy the worker cannot execute
			// (the native mux copies the source stream). Name it separately:
			// "parameters ignored" and "the requested audio mode was not
			// applied" are different operational facts, and only the second
			// one changes what the published artifact sounds like.
			metrics[metricnames.AudioModeUnsupported] = 1
			workerlog.ByJobID(job.ID).Warnf("requested audio mode %q is NOT applied (native mux copies the source stream); the artifact carries the source audio unchanged",
				plan.Output.Audio.Mode)
		}
		workerlog.ByJobID(job.ID).Warnf("audio codec/sample_rate/channels are inert (Chronon copies source audio, no transcode); mode=%q codec=%q sr=%d ch=%d",
			plan.Output.Audio.Mode, plan.Output.Audio.Codec, plan.Output.Audio.SampleRate, plan.Output.Audio.Channels)
	}
	record(metricnames.PrepareTotalStem, totalStart)
	owned = false
	return &PreparedJob{
		Job:                   job,
		Workspace:             ws,
		Plan:                  plan,
		Stats:                 stats,
		InputBytes:            inputBytes,
		Metrics:               metrics,
		OutputPath:            ws.OutputPath("result.mp4"),
		AudioSourcePath:       audioPath,
		AudioTargetSampleRate: audioTargetSampleRate,
		totalStart:            totalStart,
	}, nil
}

// prefetchWarmAssets primes Chronon's persistent image/video cache from the
// already verified workspace. The background and at most three image assets
// are selected deterministically, preserving every asset in the plan while
// bounding warm-up work on high-cardinality scenes.
//
// The requests are issued CONCURRENTLY and NOT awaited. A warm-up failure is
// diagnostic only (the materialized files remain authoritative for Render), yet
// the previous shape blocked the prepare stage on up to four IPC round-trips:
// an optional cache warm-up was charged to the prepare→GPU handoff — the very
// interval RecordGPULaneWait exists to measure — and it delayed the lane from
// starting on a job whose render did not depend on it. Each request is bounded
// by the IPC client's own service timeout, so the detached goroutines cannot
// accumulate.
func (p *Processor) prefetchWarmAssets(ctx context.Context, jobID, root string, assets []queue.AssetRef) {
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
	// context.WithoutCancel detaches the warm-up from the prepare deadline so a
	// cancelled prepare (lost lease, shutdown) does not abort a cache write into
	// Chronon's own process while still keeping the request bounded by the IPC
	// client's service timeout.
	warmCtx := context.WithoutCancel(ctx)
	for _, path := range selected {
		path := path
		go func() {
			if err := p.assetPrefetcher.PrefetchAsset(warmCtx, path); err != nil {
				workerlog.ByJobID(jobID).Infof("chronon asset warm-up skipped: path=%s err=%v", path, err)
				return
			}
			workerlog.ByJobID(jobID).Infof("chronon asset warm-up complete: path=%s", path)
		}()
	}
}
