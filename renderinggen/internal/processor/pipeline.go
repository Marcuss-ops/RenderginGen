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
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
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
	Plan            *overlay.Plan    // typed concrete Chronon plan (post-compile, marshaled once at WritePlan)
	ResolvedAssets  []queue.AssetRef // content-addressed assets materialized for this final plan
	PreparedOverlay overlay.PreparedPackage
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
	// NativeCertified is true when the render ran on Chronon's native
	// Vulkan/NVENC lane. Canonicalization preserves it: the emitted bytes are a
	// canonical re-encode of that render, not a different render path.
	// gate certified a source-video execution. Image/text-only compositions
	// may use the GPU compositor, but their host-frame pipe handoff is not the
	// native-surface contract.
	NativeCertified bool
	// FragmentedMP4 mirrors the OutputSpec flag RunGPU renders with: the
	// artifact is a silent video-only intermediate whose bytes are final while
	// appended. It is the gate for the tail-upload lane (a classic MP4's
	// backpatched header would invalidate a streaming hash, so it must never
	// be uploaded from a growing file).
	FragmentedMP4 bool
	// TailUpload is the streaming upload RunGPU started for this job's output,
	// nil when the artifact was not fragmented or the store cannot stage. It
	// is set at RunGPU's commit point and consumed by storeArtifact.
	TailUpload *stagedUpload
	// Segments is the P4 batch-superjob segmentation: one MP4 per semantic
	// item cut from ONE encoder session (the job's OutputPath doubles as the
	// session's whole-timeline mirror, so the fused clip keeps its ordinary
	// artifact contract). Empty (the default) preserves the single-output
	// path. SegmentItemIDs is parallel to Segments and names the owning
	// semantic item of each window.
	Segments       []chronon.OutputSegment
	SegmentItemIDs []string
	// baseRenderRequest is the request RunGPU actually submitted (minus
	// per-item windows): the P4 single-segment retry re-renders ONE failed
	// segment from this exact request narrowed to its frame window, so the
	// retry can never drift from the session it repairs.
	baseRenderRequest chronon.RenderRequest
	totalStart        time.Time
}

// PrepareJob runs the CPU-bound preparation half of the render pipeline:
// validate, compile, asset materialization and plan.json. It returns a
// PreparedJob whose workspace must be cleaned up by the GPU-stage caller.
func (p *Processor) PrepareJob(ctx context.Context, job *queue.Job) (*PreparedJob, error) {
	totalStart := time.Now()
	metrics := make(map[string]float64, 8)
	if job != nil && job.DaemonAdmissionWaitMS > 0 {
		metrics[metricnames.DaemonAdmissionWaitMS] = job.DaemonAdmissionWaitMS
	}
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
	leaseUntil := "-"
	if !job.LeaseUntil.IsZero() {
		leaseUntil = job.LeaseUntil.UTC().Format(time.RFC3339)
	}
	workerlog.ByJobID(job.ID).Statef("preparing", "type=%s attempts=%d lease_generation=%d lease_until=%s",
		job.JobType, job.Attempts, job.LeaseGeneration, leaseUntil)

	st := &prepareState{job: job, metrics: metrics, record: record, totalStart: totalStart}
	// The stages are ordered and each one fails closed before the next spends
	// a resource: compile/gate -> workspace/materialize/prove -> burn/rebuild
	// -> serialize. See prepare_stages.go for the per-stage contract.
	if err := p.prepareCompile(ctx, st); err != nil {
		return nil, err
	}
	if err := p.prepareWorkspace(ctx, st); err != nil {
		return nil, err
	}
	// Ownership transfers only on the single success return below. Registering
	// the cleanup guard once, after the workspace exists, makes "an error path
	// forgot to remove the workspace" impossible: the historical shape repeated
	// an explicit cleanupWorkspace call on ~20 return sites.
	owned := true
	defer func() {
		if owned {
			p.cleanupWorkspace(st.ws, job.ID)
		}
	}()
	if err := p.prepareSubtitles(st); err != nil {
		return nil, err
	}
	if err := p.rebuildPreparedPackage(st); err != nil {
		return nil, err
	}
	if err := p.preparePlanOutput(st); err != nil {
		return nil, err
	}
	record(metricnames.PrepareTotalStem, totalStart)
	workerlog.ByJobID(job.ID).Statef("prepared", "layers=%d assets=%d items=%d prepare_ms=%.1f",
		len(st.plan.Layers), len(st.assets), st.stats.SemanticItemCount, metrics[metricnames.PrepareTotalStem+"_ms"])
	owned = false
	return st.preparedJob(), nil
}

// prefetchWarmAssets primes Chronon's persistent image/video cache from the
// already verified workspace. At most prefetchMaxImages image assets and at
// most prefetchMaxVideos video assets are selected deterministically,
// bounding warm-up work on high-cardinality scenes while still warming the
// assets the render will most likely need first.
//
// The requests are issued CONCURRENTLY and NOT awaited. A warm-up failure is
// diagnostic only (the materialized files remain authoritative for Render), yet
// the previous shape blocked the prepare stage on up to four IPC round-trips:
// an optional cache warm-up was charged to the prepare→GPU handoff — the very
// interval RecordGPULaneWait exists to measure — and it delayed the lane from
// starting on a job whose render did not depend on it.
//
// Lifetime: every detached warm-up runs under a child of the prepare ctx
// with prefetchTimeout. A cancelled prepare (lost lease, shutdown) therefore
// cancels the in-flight warm-ups; no WithoutCancel orphan survives the job.
// Each request is additionally bounded by the IPC client's own service
// timeout, so the detached goroutines cannot accumulate.
//
// prefetchInflight bounds warm-up concurrency process-wide. Four in flight
// keeps the warm-up ahead of the render without a thundering herd. The
// semaphore is acquired inside the detached goroutine (or the goroutine
// exits on ctx cancellation), so the bound never blocks the prepare stage
// it serves.
const (
	prefetchMaxImages = 3
	prefetchMaxVideos = 6
	prefetchTimeout   = 30 * time.Second
)

var prefetchInflight = make(chan struct{}, 4)

func (p *Processor) prefetchWarmAssets(ctx context.Context, jobID, root string, assets []queue.AssetRef) {
	if p == nil || p.assetPrefetcher == nil {
		return
	}
	selected := make([]string, 0, prefetchMaxImages+prefetchMaxVideos)
	seen := make(map[string]struct{}, prefetchMaxImages+prefetchMaxVideos)
	images, videos := 0, 0
	for _, asset := range assets {
		ext := strings.ToLower(filepath.Ext(asset.LogicalPath))
		isVideo := ext == ".mp4" || ext == ".mov" || ext == ".webm"
		isImage := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !isVideo && !isImage {
			continue
		}
		if isImage {
			if images >= prefetchMaxImages {
				continue
			}
			images++
		} else {
			if videos >= prefetchMaxVideos {
				continue
			}
			videos++
		}
		path := filepath.Join(root, filepath.FromSlash(asset.LogicalPath))
		if _, ok := seen[path]; ok {
			// Duplicate path counted above but selected once: the caps stay
			// on distinct assets examined, the warm-up stays deduplicated.
			if isImage {
				images--
			} else {
				videos--
			}
			continue
		}
		seen[path] = struct{}{}
		selected = append(selected, path)
	}
	// Lifetime is bound to the prepare context: each detached warm-up gets a
	// child ctx with prefetchTimeout, so a cancelled prepare (lost lease,
	// shutdown) cancels the in-flight warm-ups instead of leaving orphaned
	// WithoutCancel goroutines writing into Chronon after the job is gone.
	// Fire-and-forget is preserved: the bound lives inside the goroutines, so
	// this function never blocks the prepare→GPU handoff.
	for _, path := range selected {
		path := path
		go func() {
			select {
			case prefetchInflight <- struct{}{}:
				defer func() { <-prefetchInflight }()
			case <-ctx.Done():
				return
			}
			warmCtx, cancel := context.WithTimeout(ctx, prefetchTimeout)
			defer cancel()
			if err := p.assetPrefetcher.PrefetchAsset(warmCtx, path); err != nil {
				workerlog.ByJobID(jobID).Infof("chronon asset warm-up skipped: path=%s err=%v", path, err)
				return
			}
			workerlog.ByJobID(jobID).Infof("chronon asset warm-up complete: path=%s", path)
		}()
	}
}
