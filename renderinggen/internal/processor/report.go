package processor

import (
	"context"
	"log"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
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
	log.Printf("job %s failed: %v", job.ID, err)
	if hasDurableArtifact(job.Artifact) {
		if reportErr := q.Rendered(ctx, job.ID, err.Error(), *job.Artifact); reportErr != nil {
			log.Printf("job %s report rendered: %v", job.ID, reportErr)
		}
		return
	}
	if reportErr := q.Fail(ctx, job.ID, err.Error()); reportErr != nil {
		log.Printf("job %s report fail: %v", job.ID, reportErr)
	}
}

// ReportFailureWithArtifact is ReportFailure for the caller that holds the
// durable artifact separately (the post pool: FinalizeJob produced it, but it
// was never attached to the claimed job). Rendered keeps the artifact claimable
// for a publication-only retry so a lost terminal report can never trigger a
// GPU re-render.
func ReportFailureWithArtifact(ctx context.Context, q ReportQueue, id string, artifact queue.Artifact, err error) {
	log.Printf("job %s failed: %v", id, err)
	if !hasDurableArtifact(&artifact) {
		if reportErr := q.Fail(ctx, id, err.Error()); reportErr != nil {
			log.Printf("job %s report fail: %v", id, reportErr)
		}
		return
	}
	if reportErr := q.Rendered(ctx, id, err.Error(), artifact); reportErr != nil {
		log.Printf("job %s report rendered: %v", id, reportErr)
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
		log.Printf("job %s report complete: %v", id, err)
		if durable && hasDurableArtifact(&artifact) {
			// The bytes are durable in L3; never let a lost report trigger a
			// GPU re-render. Record rendered so a publication-only retry
			// re-claims the artifact (see ReportFailure's artifact branch).
			if reportErr := q.Rendered(ctx, id, err.Error(), artifact); reportErr != nil {
				log.Printf("job %s report rendered: %v", id, reportErr)
			}
		}
	}
}
