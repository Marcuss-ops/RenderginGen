// worker_pools.go owns the three-stage claim pipeline of the worker: CPU
// preparation feeds the GPU lanes, and CPU post-processing (probe, hash,
// store, publish) drains behind them. main() only wires the pools together.
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// preppedJob is a claimed job whose CPU preparation succeeded; it is ready
// for the GPU lane. The workspace transfers ownership to the GPU lane.
type preppedJob struct {
	job      *queue.Job
	prepared *processor.PreparedJob
}

// renderOutcome carries a GPU-completed job to the post pool.
type renderOutcome struct {
	job      *queue.Job
	prepared *processor.PreparedJob
	err      error
}

// runPrepPool claims jobs and runs their CPU-bound preparation. Prepare-only
// jobs complete here without touching the GPU lane. A rendered job observed
// by the prep pool (worker restart / stage hand-off) is forwarded straight to
// the post pool: its artifact is already durable and must never re-render.
func runPrepPool(ctx context.Context, q *queue.Client, proc *processor.Processor, prepCh chan<- *preppedJob, timings config.PipelineConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Long-poll claim: the server wakes this request as soon as a job may
		// be claimable (submit/rendered/fail/expiry). The claim accepts any
		// claimable state: pending render jobs AND rendered jobs awaiting a
		// publication-only retry (their durable artifact rides on the claim,
		// see job.Artifact below). The atomic claim remains unchanged (SKIP
		// LOCKED on the DB side), so the wait never assigns work, it only
		// removes the empty-queue sleep between renders.
		job, err := q.ClaimWait(ctx, timings.ClaimLongPoll)
		if err != nil {
			log.Printf("prep claim: %v", err)
			if !sleepCtx(ctx, timings.ClaimRetryDelay) {
				return
			}
			continue
		}
		if job == nil {
			continue
		}

		// Publication retry of a rendered job: never render again. The lease
		// is renewed for the whole upload so a multi-minute Drive publish can
		// never expire mid-transfer and be re-claimed by another worker.
		if job.Artifact != nil {
			log.Printf("job %s prep received durable artifact; switching to publication", job.ID)
			artifact := *job.Artifact
			artifact.Metrics = nil
			pubErr := withLeaseVoid(ctx, job, q, timings, func(jobCtx context.Context) error {
				published, publishErr := proc.Publish(jobCtx, job.ID, job.JobType, artifact)
				if publishErr != nil {
					return publishErr
				}
				// Complete while the lease is still held: a completion after
				// lease expiry would 409 and strand a finished artifact.
				return q.Complete(jobCtx, job.ID, published)
			})
			if pubErr != nil {
				processor.ReportFailure(ctx, q, job, pubErr)
				continue
			}
			log.Printf("job %s publication retry completed (artifact %q)", job.ID, job.Artifact.StorageKey)
			continue
		}

		// Prepare-only jobs (overlay.prepare warm-up) finish here.
		if job.JobType == queue.JobTypeOverlayPrepare {
			artifact, prepErr := withLease(ctx, job, q, timings, func(jobCtx context.Context) (queue.Artifact, error) {
				return proc.Prepare(jobCtx, job)
			})
			if prepErr != nil {
				processor.ReportFailure(ctx, q, job, prepErr)
				continue
			}
			processor.ReportComplete(ctx, q, job.ID, artifact, false)
			continue
		}

		// CPU preparation for a render job. The lease renewal must be
		// continuous from PrepareJob through hand-off to the GPU lane:
		// prepCh dwell with a buffered channel and a lease scoped only to
		// PrepareJob would let renewal stop while the job waits for a GPU
		// lane — under backlog >10 min the lease expires, the job is
		// requeued and double-rendered. Wrap PrepareJob + rendezvous send
		// in one withLeaseVoid so renewal never stops in channel dwell.
		var prepared *processor.PreparedJob
		handoffErr := withLeaseVoid(ctx, job, q, timings, func(jobCtx context.Context) error {
			var err error
			prepared, err = proc.PrepareJob(jobCtx, job)
			if err != nil {
				return err
			}
			// prepCh is a rendezvous, so the send below completes only when a
			// GPU lane is free: the interval around it IS this job's admission
			// wait on the GPU. Recorded because it is the dominant cost of a
			// backlogged batch and would otherwise be buried in total_ms with
			// no phase to explain it (see processor.RecordGPULaneWait).
			laneWaitStart := time.Now()
			select {
			case prepCh <- &preppedJob{job: job, prepared: prepared}:
				processor.RecordGPULaneWait(prepared, time.Since(laneWaitStart))
				return nil
			case <-jobCtx.Done():
				_ = prepared.Workspace.Cleanup()
				return jobCtx.Err()
			case <-ctx.Done():
				_ = prepared.Workspace.Cleanup()
				return ctx.Err()
			}
		})
		if handoffErr != nil {
			if ctx.Err() != nil {
				if prepared != nil {
					_ = prepared.Workspace.Cleanup()
				}
				return
			}
			if errors.Is(handoffErr, context.Canceled) {
				// Lease permanently lost while waiting for GPU lane: the queue
				// has requeued the job elsewhere — do not ReportFailure (would
				// 409) and do not double-render. Workspace already cleaned on
				// jobCtx cancellation.
				continue
			}
			processor.ReportFailure(ctx, q, job, handoffErr)
			continue
		}
	}
}

// runGPULane is the only stage that invokes Chronon. One goroutine runs per
// configured GPU lane (cfg.Worker.GPULanes); the renderer itself is wrapped in
// chronon.LimitConcurrency(renderer, gpuLanes), so up to gpuLanes Chronon
// sessions — and therefore encoder sessions — run concurrently, matching the
// NVENC multi-session baseline. While Chronon works here, the prep pool
// prepares the next job and the post pool finalizes the previous one.
func runGPULane(ctx context.Context, q *queue.Client, proc *processor.Processor, prepCh <-chan *preppedJob, doneCh chan<- renderOutcome, timings config.PipelineConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-prepCh:
			// Chronon is the long-running stage. Keep the queue lease alive
			// while it renders; renewing only during prepare/post would let a
			// normal software render expire and be claimed a second time.
			//
			// A running render may write nothing to its workspace for longer
			// than the stale-sweeper horizon (1h), so the workspace liveness
			// marker is refreshed here for the whole GPU stage: without it the
			// sweeper could RemoveAll a live render's directory.
			renderDone := make(chan struct{})
			go func() {
				ticker := time.NewTicker(timings.WorkspaceLeaseRefresh)
				defer ticker.Stop()
				for {
					select {
					case <-renderDone:
						return
					case <-ticker.C:
						if err := p.prepared.Workspace.WriteLease(time.Now().Add(timings.WorkspaceLeaseTTL)); err != nil {
							log.Printf("job %s: refresh workspace lease marker: %v", p.job.ID, err)
						}
					}
				}
			}()
			gpuErr := withLeaseVoid(ctx, p.job, q, timings, func(jobCtx context.Context) error {
				return proc.RunGPU(jobCtx, p.prepared)
			})
			close(renderDone)
			// Duty-cycle telemetry: record this render's end so the next job's
			// gpu_gap_us metric measures the true dead time between renders.
			proc.PutGPURenderEnd(time.Now())
			select {
			case doneCh <- renderOutcome{job: p.job, prepared: p.prepared, err: gpuErr}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// runPostPool finalizes GPU-completed jobs (probe, hash, store, ledger) and
// publishes them. Workspace cleanup always runs here: it is the last owner.
func runPostPool(ctx context.Context, q *queue.Client, proc *processor.Processor, parentFinalizer *processor.ParentFinalizer, doneCh <-chan renderOutcome, timings config.PipelineConfig) {
	// Read once, from configuration: this pool and StagedRender (the other
	// workspace owner) must agree, and a second environment read is exactly how
	// they could diverge.
	keepWorkspaces := timings.KeepWorkspace
	for {
		select {
		case <-ctx.Done():
			return
		case out := <-doneCh:
			job, prepared := out.job, out.prepared
			// The post pool is the last owner of the workspace, so cleanup runs
			// at the end of THIS iteration — never as a deferred call inside
			// the loop, which would postpone it until pool shutdown and let
			// every finished job's workspace accumulate (jobs root is often
			// /dev/shm, i.e. RAM).
			cleanup := func() {
				if keepWorkspaces {
					return
				}
				if err := prepared.Workspace.Cleanup(); err != nil {
					log.Printf("job %s: workspace cleanup: %v", job.ID, err)
				}
			}
			if out.err != nil {
				processor.ReportFailure(ctx, q, job, out.err)
				cleanup()
				continue
			}
			var artifact queue.Artifact
			err := withLeaseVoid(ctx, job, q, timings, func(jobCtx context.Context) error {
				var finalizeErr error
				artifact, finalizeErr = proc.FinalizeJob(jobCtx, prepared)
				if finalizeErr != nil {
					return finalizeErr
				}
				// Rendering stops as soon as the artifact is durable in object
				// storage; Drive publication is part of this pool, not the GPU
				// lane's critical path.
				artifact, finalizeErr = proc.Publish(jobCtx, job.ID, job.JobType, artifact)
				return finalizeErr
			})
			if err != nil {
				// The render may have finished with the bytes durable in the
				// object store before a later stage (external publication)
				// failed. hasDurableArtifact decides the transition: Rendered
				// keeps such a job claimable for a publication-only retry;
				// anything else is a render failure.
				processor.ReportFailureWithArtifact(ctx, q, job.ID, artifact, err)
				cleanup()
				continue
			}
			log.Printf("job %s artifact: storage_key=%q sha256=%q size=%d copy_eligible=%t backend=%q frames=%d %dx%d",
				job.ID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes,
				artifact.CopyEligible, artifact.Backend, artifact.FrameCount, artifact.Width, artifact.Height)
			processor.ReportComplete(ctx, q, job.ID, artifact, true)
			if parentFinalizer != nil && job.ParentJobID != "" {
				tryFinalizeParent(ctx, parentFinalizer, job.ParentJobID)
			}
			// Cleanup is intentionally after Complete/parent-finalize: the
			// queue is the durable record and parent assembly reads the
			// children's artifacts from the object store/L2, never from the
			// child workspace.
			cleanup()
		}
	}
}

// tryFinalizeParent is intentionally best-effort: a child completion can be
// observed before its siblings, so an incomplete parent is normal. The
// finalizer itself performs the second children read and atomic claim, which
// makes concurrent attempts and worker restarts safe.
//
// It stays SYNCHRONOUS on the post pool, deliberately. A child completion is the
// only trigger for parent finalization in this worker — there is no periodic
// sweep that would adopt a detached attempt — so detaching it would let one
// failed attempt strand the parent until an unrelated event re-triggered it.
// What it does NOT do is pay for the child family twice: the finalizer derives
// the expected frame range from the same read it validates
// (ParentFinalizer.FinalizeFromChildren), so a completed chunk no longer issues
// a Children() round-trip only to learn the range it spans.
func tryFinalizeParent(ctx context.Context, finalizer *processor.ParentFinalizer, parentID string) {
	finalized, artifact, err := finalizer.FinalizeFromChildren(ctx, parentID)
	if err != nil {
		// Incomplete children and a competing finalizer are expected during the
		// normal fan-in; the queue remains the source of truth for retry.
		log.Printf("parent %s not finalized yet: %v", parentID, err)
		return
	}
	if finalized {
		log.Printf("parent %s finalized: storage_key=%q sha256=%q size=%d", parentID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes)
	}
}
