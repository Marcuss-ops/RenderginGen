package processor

import (
	"context"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// ReportQueue is the queue surface the worker's terminal-report helpers need.
// *queue.Client satisfies it; tests substitute a fake. Keeping the dependency
// minimal (three methods) is what lets ReportFailure/ReportComplete live here
// next to the rest of the job state machine instead of in cmd/renderinggen.
type ReportQueue interface {
	Complete(ctx context.Context, id string, artifact queue.Artifact) error
	Rendered(ctx context.Context, id, reason string, artifact queue.Artifact) error
	Fail(ctx context.Context, id, reason string) error
}

// hasDurableArtifact is the single "the artifact bytes are already in the
// object store" predicate used by both terminal-report paths. It is the ONLY
// place the durability concept is decided: a non-empty StorageKey means a
// re-claim must be publication-only (Rendered), never a GPU re-render.
func hasDurableArtifact(artifact *queue.Artifact) bool {
	return artifact != nil && artifact.StorageKey != ""
}

// ReportFailure applies the queue transition rules for a failed job: a failure
// while a durable artifact is attached is a publication failure (the job is
// re-claimed for a publication-only retry via Rendered); anything else is a
// render failure (Fail). Logging happens here so every terminal report — fail
// or rendered — is visible with the same shape.
func ReportFailure(ctx context.Context, q ReportQueue, job *queue.Job, err error) {
	// Bind here as well as at claim: this funnel is reachable from the serial
	// Render path too, and a terminal line without the run id would be the one
	// line an operator most needs to correlate.
	jobLog := workerlog.BindJob(job.ParentJobID, job.ID)
	jobLog.Errorf("failed: %v", err)
	// Observed at the report funnel, so a future worker pool cannot complete or
	// fail jobs without the worker's own metric surface seeing it.
	noteJobOutcome(JobOutcomeFailed)
	if hasDurableArtifact(job.Artifact) {
		if reportErr := q.Rendered(ctx, job.ID, err.Error(), *job.Artifact); reportErr != nil {
			jobLog.Warnf("report rendered: %v", reportErr)
		}
		return
	}
	if reportErr := q.Fail(ctx, job.ID, err.Error()); reportErr != nil {
		jobLog.Warnf("report fail: %v", reportErr)
	}
}

// ReportFailureWithArtifact is ReportFailure for the caller that holds the
// durable artifact separately (the post pool: FinalizeJob produced it, but it
// was never attached to the claimed job). Rendered keeps the artifact claimable
// for a publication-only retry so a lost terminal report can never trigger a
// GPU re-render.
func ReportFailureWithArtifact(ctx context.Context, q ReportQueue, id string, artifact queue.Artifact, err error) {
	// ByJobID resolves the parent_run id recorded when the job was bound at
	// claim/Prepare: this caller holds only the id, and an unattributed
	// terminal line is exactly what made worker triage impossible.
	jobLog := workerlog.ByJobID(id)
	jobLog.Errorf("failed: %v", err)
	noteJobOutcome(JobOutcomeFailed)
	if !hasDurableArtifact(&artifact) {
		if reportErr := q.Fail(ctx, id, err.Error()); reportErr != nil {
			jobLog.Warnf("report fail: %v", reportErr)
		}
		return
	}
	if reportErr := q.Rendered(ctx, id, err.Error(), artifact); reportErr != nil {
		jobLog.Warnf("report rendered: %v", reportErr)
	}
}

// ReportComplete marks a job completed on the queue.
//
// When durable is true and the artifact is already persisted in the object
// store (render finished, only the terminal report failed), a Complete failure
// falls back to Rendered: the job then stays claimable for a publication-only
// retry with its artifact attached, instead of expiring back to pending and
// being re-rendered on the next claim. A failed lease-expiry race is thereby
// downgraded from a full GPU re-render to an upload retry.
func ReportComplete(ctx context.Context, q ReportQueue, id string, artifact queue.Artifact, durable bool) {
	if err := q.Complete(ctx, id, artifact); err != nil {
		workerlog.ByJobID(id).Warnf("report complete: %v", err)
		// A completion that could not be reported is NOT a completed job: it
		// either falls back to Rendered (publication-only retry, counted as a
		// failure below) or stays claimable. Counting it as completed would make
		// the worker report success for work the queue never accepted.
		noteJobOutcome(JobOutcomeFailed)
		if durable && hasDurableArtifact(&artifact) {
			// The bytes are durable in L3; never let a lost report trigger a
			// GPU re-render. Record rendered so a publication-only retry
			// re-claims the artifact (see ReportFailure's artifact branch).
			if reportErr := q.Rendered(ctx, id, err.Error(), artifact); reportErr != nil {
				workerlog.ByJobID(id).Warnf("report rendered: %v", reportErr)
			}
		}
		return
	}
	// The queue accepted the completion: this attempt finished. It is per
	// ATTEMPT, not per distinct job — the queue owns the job-level lifecycle and
	// already counts it (renderinggen_jobs_*), so this series must not be read as
	// a job census.
	noteJobOutcome(JobOutcomeCompleted)
}
