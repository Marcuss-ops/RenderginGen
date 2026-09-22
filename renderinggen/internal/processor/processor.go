// Package processor implements the RenderingGen job pipeline. A claimed job is
// validated as renderinggen.job.v1, its assets are materialized into a
// per-job workspace, the render plan is written to plan.json, Chronon renders
// it, and the output is hashed and published to the artifact store. The caller
// completes or fails the job on the queue using the returned artifact/error.
//
// File layout: processor.go owns the Processor state and the coarse pipeline
// entry points, materialize.go the asset materialization, render_selection.go
// and native_gate.go the render-path classification/receipt gates,
// publish_drive.go and store_artifact.go the publication phases and
// record_artifact.go the artifact ledger mirror.
package processor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// Processor orchestrates a single render job:
//
//	validate -> workspace -> resolve/materialize -> plan.json -> render
//	-> hash -> storage.Put -> (caller completes) -> cleanup
type Processor struct {
	jobsRoot        string
	backend         string
	chrononVersion  string
	storeURL        string
	store           *storage.Client
	renderer        chronon.Renderer
	assetPrefetcher chronon.AssetPrefetcher
	drive           drive.Publisher     // nil = external publication disabled
	recorder        artifactdb.Recorder // nil = artifact ledger disabled

	// phaseHook, when set, receives the wall-clock duration of each pipeline
	// phase so benchmarks can report asset_fetch/prepare/render/publish ms
	// without coupling the processor to a metrics backend.
	phaseHook func(phase string, d time.Duration)

	// report, when true, passes --report to chronon3d_cli so the execution
	// report + telemetry JSONL (render_ms/encode_ms/cache hits-misses) are
	// emitted. Disabled by default; enabled by the performance benchmark.
	report               bool
	hardwareEncoder      string
	encodePreset         string
	pipePixFmt           string
	nativeOutputProfiles bool
	strictNativeBackend  bool

	// workspaceLeaseTTL is the validity window written into the per-workspace
	// liveness marker at PrepareJob (and refreshed by the GPU lane). Zero means
	// the shipped default (2h), which is what the value was as a literal. It is
	// configuration because it has to be consistent with two other settings the
	// operator owns: the sweeper's stale age (pipeline.workspace_stale_after)
	// and the refresh period (pipeline.workspace_lease_refresh).
	workspaceLeaseTTL time.Duration

	// receiptVerify is the output-verification policy requested from Chronon
	// and enforced from its receipt (see receiptVerifyLevel). Empty means fast,
	// the production default.
	receiptVerify renderVerifyLevel

	// deepVisualValidation enables the sampled ffmpeg visual gate; see
	// deepVisualValidationEnabled.
	deepVisualValidation bool

	// keepWorkspace leaves a finished job's workspace on disk; see
	// SetKeepWorkspace.
	keepWorkspace bool

	// progressTracker, when set, receives per-frame progress observations
	// during RunGPU so health and the queue pusher can report live render
	// position instead of an opaque RUNNING/0% for minutes.
	progressTracker *chronon.ProgressTracker

	// gpuGapMu/gpuGapLastRenderEnd track the wall-clock gap between the end of
	// one GPU render and the start of the next on THIS processor (the duty-cycle
	// KPI behind the per-job gpu_gap_us metric). They are processor-scoped so
	// tests and unrelated processors never cross-contaminate the measurement.
	gpuGapMu            sync.Mutex
	gpuGapLastRenderEnd time.Time

	// cleanupFailures counts workspaces that could not be removed. Workspace
	// cleanup is fail-open by design (a render must never fail because its
	// scratch directory survived), but the jobs root is frequently tmpfs, so a
	// systematically failing Cleanup leaks RAM. The counter plus a rate-limited
	// ERROR log keep that degradation observable instead of silently degrading:
	// it is reported on /health through Degradations().
	cleanupFailures atomic.Int64
	cleanupLogLast  atomic.Int64 // unix nanos of the last degradation log (rate limit)
}

// New creates a job processor.
func New(jobsRoot, backend, chrononVersion, storeURL string, store *storage.Client, renderer chronon.Renderer) *Processor {
	return &Processor{
		jobsRoot:       jobsRoot,
		backend:        backend,
		chrononVersion: chrononVersion,
		storeURL:       strings.TrimRight(storeURL, "/"),
		store:          store,
		renderer:       renderer,
	}
}

// SetPhaseHook installs an optional callback that receives each pipeline
// phase's wall-clock duration: "materialize", "plan", "render", "publish".
// Used by the performance benchmark; nil (default) disables the overhead.
func (p *Processor) SetPhaseHook(fn func(phase string, d time.Duration)) {
	p.phaseHook = fn
}

// SetWorkspaceLeaseTTL sets the validity window the per-job workspace liveness
// marker is written with. Zero keeps defaultWorkspaceLeaseTTL.
func (p *Processor) SetWorkspaceLeaseTTL(ttl time.Duration) {
	p.workspaceLeaseTTL = ttl
}

// defaultWorkspaceLeaseTTL is the shipped marker validity window, matching the
// worker's historical literal so an unconfigured processor behaves identically.
const defaultWorkspaceLeaseTTL = 2 * time.Hour

// workspaceLeaseDuration resolves the configured marker validity.
func (p *Processor) workspaceLeaseDuration() time.Duration {
	if p.workspaceLeaseTTL > 0 {
		return p.workspaceLeaseTTL
	}
	return defaultWorkspaceLeaseTTL
}

// SetReport enables the chronon3d_cli --report flag so the engine writes its
// execution report and telemetry JSONL (render_ms, encode_ms, cache hits and
// misses). Used by the performance benchmark; off by default.
func (p *Processor) SetReport(enabled bool) {
	p.report = enabled
}

// SetReceiptVerify sets the output-verification policy the worker requests from
// Chronon (fast / normal / certify). The zero value means fast, which is the
// production default; config validates the accepted spellings at load, so an
// unrecognized value can only arrive from a caller that bypassed config.
func (p *Processor) SetReceiptVerify(level string) {
	p.receiptVerify = renderVerifyLevel(level)
}

// SetDeepVisualValidation enables the sampled ffmpeg visual gate for jobs whose
// plan carries an authored overlay.
func (p *Processor) SetDeepVisualValidation(enabled bool) {
	p.deepVisualValidation = enabled
}

// SetKeepWorkspace leaves a finished job's workspace on disk for inspection.
// The post pool is the workspace's last owner, so it and StagedRender must
// receive the SAME value: the old arrangement read the environment separately
// in each, which let the two owners disagree.
func (p *Processor) SetKeepWorkspace(keep bool) {
	p.keepWorkspace = keep
}

// SetHardwareEncoder selects an explicit FFmpeg hardware encoder (for
// example, nvenc). Empty/none preserves the software encoder path.
func (p *Processor) SetHardwareEncoder(encoder string) {
	p.hardwareEncoder = encoder
}

// SetEncodePreset selects an explicit FFmpeg NVENC preset (for example, "p2"
// for the throughput tier). Empty preserves the engine default; the worker
// never invents a preset when none is configured.
func (p *Processor) SetEncodePreset(preset string) {
	p.encodePreset = preset
}

// SetPipePixFmt selects the explicit host-frame pipe format forwarded to
// Chronon for GPU composition jobs. Empty preserves Chronon's default.
func (p *Processor) SetPipePixFmt(format string) {
	p.pipePixFmt = format
}

// SetNativeOutputProfiles enables passing output.profile_id to Chronon. Keep
// this disabled for legacy runtimes that reject unknown output properties; the
// worker still certifies the requested profile from the encoded MP4.
func (p *Processor) SetNativeOutputProfiles(enabled bool) { p.nativeOutputProfiles = enabled }

// SetStrictNativeBackend makes the gpu-vulkan-native profile fail closed when
// Chronon reports a hybrid or software-fallback execution. The artifact is
// rejected before object-store publication, so a receipt cannot certify the
// wrong execution path.
func (p *Processor) SetStrictNativeBackend(enabled bool) { p.strictNativeBackend = enabled }

// SetPublisher installs the Google Drive publisher used by Publish. When nil
// (the default) publication is disabled and Publish is a no-op.
func (p *Processor) SetPublisher(pub drive.Publisher) {
	p.drive = pub
}

// SetArtifactRecorder installs the worker-local artifact mirror. When set,
// every rendered job writes one ArtifactRecord (hash, probe facts, semantic
// counters, per-phase metrics) after the object store accepted the bytes. The
// mirror is diagnostic: a failed Record is logged and flagged on the artifact
// metrics (mirror_failure=1), it never fails the render — the central queue
// PostgreSQL row remains authoritative for the artifact. nil (default)
// disables the mirror.
func (p *Processor) SetArtifactRecorder(rec artifactdb.Recorder) {
	p.recorder = rec
}

// SetProgressTracker installs the shared render progress tracker. When set,
// RunGPU feeds every renderer frame-milestone into it and records the final
// frame position + average fps into the job's ledger metrics.
func (p *Processor) SetProgressTracker(tracker *chronon.ProgressTracker) {
	p.progressTracker = tracker
}

// SetAssetPrefetcher installs the optional Chronon daemon warm-up bridge.
// Warm-up is best-effort and never changes the semantic plan or render gate.
func (p *Processor) SetAssetPrefetcher(prefetcher chronon.AssetPrefetcher) {
	if p != nil {
		p.assetPrefetcher = prefetcher
	}
}

func (p *Processor) recordPhase(phase string, start time.Time) {
	if p.phaseHook == nil {
		return
	}
	p.phaseHook(phase, time.Since(start))
}

// cleanupWorkspace removes a job's workspace and reports the outcome. The
// caller treats a failure as non-fatal, but it is never invisible: the failure
// increments the process counter, logs at ERROR at a rate limited to one per
// second (a broken jobs root must not become a log storm), and is therefore
// visible on /health as a degradation.
func (p *Processor) cleanupWorkspace(ws *workspace.Workspace, jobID string) {
	if ws == nil {
		return
	}
	err := ws.Cleanup()
	if err == nil {
		return
	}
	total := p.cleanupFailures.Add(1)
	now := time.Now().UnixNano()
	last := p.cleanupLogLast.Load()
	if now-last >= int64(time.Second) && p.cleanupLogLast.CompareAndSwap(last, now) {
		// Job-scoped, so the degradation is attributable to the job whose
		// scratch directory survived — and lands in that job's own log file.
		workerlog.ByJobID(jobID).Errorf("workspace cleanup failed (total failures=%d): %v — the scratch directory is still on disk",
			total, err)
	}
}

// AttachDurableJobLog opens the job's durable per-job log file under the jobs
// root and returns its path. The pools call it at CLAIM, before any workspace
// work, so the file already contains the claim/lease lines by the time the job
// has a workspace (where PrepareJob attaches the live copy).
//
// This file is the worker's own record, and it is the reason the collector no
// longer depends on the host's journald retention: the workspace is removed at
// the end of every job, so the workspace copy alone would vanish.
func (p *Processor) AttachDurableJobLog(jobID string) (string, error) {
	path := workerlog.DurableJobLogPath(p.jobsRoot, jobID)
	if path == "" {
		return "", fmt.Errorf("processor: no durable job-log path for job %q (jobs root %q)", jobID, p.jobsRoot)
	}
	if _, err := workerlog.AttachJobLog(jobID, path); err != nil {
		return "", err
	}
	return path, nil
}

// Degradations reports the process-cumulative fail-open degradations. A
// non-zero value means the worker is still serving but is degrading silently
// unless an operator looks at this map; /health exposes it.
func (p *Processor) Degradations() map[string]int64 {
	if p == nil {
		return nil
	}
	total := p.cleanupFailures.Load()
	if total == 0 {
		return nil
	}
	return map[string]int64{metricnames.WorkspaceCleanupFailures: total}
}

// Process runs the full pipeline (render + external publication) and returns
// the published artifact. The worker normally calls Render and Publish
// separately so a failed publication can be retried without a re-render.
func (p *Processor) Process(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	artifact, err := p.Render(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	return p.Publish(ctx, job.ID, job.JobType, artifact)
}

// Render runs the render pipeline — validate, compile, materialize, plan.json,
// Chronon render, hash, store to the object store and record the artifact
// ledger — and returns the artifact metadata (without external publication
// fields). The artifact bytes are durably stored under artifact.StorageKey, so
// a publication retry can skip rendering entirely.
//
// Render is the serial counterpart of the worker pools' FinalizeJob stage: it
// stops before Publish on purpose. Use Process for the full serial pipeline
// (Render + Publish), which resolves the same publication policy as the pools.
//
// The implementation is the staged pipeline (PrepareJob -> RunGPU ->
// FinalizeJob) run serially for this job; the worker's concurrent pools use
// the same stages to overlap CPU work with the GPU lane.
func (p *Processor) Render(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	return p.StagedRender(ctx, job)
}
