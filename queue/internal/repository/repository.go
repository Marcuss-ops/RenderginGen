// Package repository defines the persistence contract for the central job
// queue. The in-memory store and the PostgreSQL backend both implement it, so
// the HTTP server and the lease-expiry loop never know which one is in use.
package repository

import (
	"errors"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// ErrNotFound is returned by Get when the job does not exist.
var ErrNotFound = errors.New("job not found")

// JobRepository is the storage contract for the central job queue.
type JobRepository interface {
	// Submit enqueues a job. The ID is required and must be unique.
	Submit(job model.Job) error

	// Claim atomically claims the next pending job for a worker, returning the
	// job and its lease duration. It returns a nil job when the queue is empty.
	Claim(workerID string) (*model.Job, time.Duration, error)
	ClaimState(workerID string, state model.State) (*model.Job, time.Duration, error)

	// Get returns the current state of a job, including its artifact when
	// completed. It returns ErrNotFound when the job does not exist.
	Get(id string) (*model.Job, error)

	// Children returns child chunks ordered by chunk index.
	Children(parentJobID string) ([]*model.Job, error)

	// ClaimFinalization atomically claims a parent whose children are ready.
	ClaimFinalization(parentJobID, workerID string) (*model.Job, bool, error)

	// Complete marks a running job as completed and records the rendered
	// artifact. The service layer rejects incomplete artifact metadata before
	// this method is called.
	Complete(id, workerID string, artifact model.Artifact) error

	// Rendered marks a running job as rendered: its artifact is durably stored
	// but external publication failed, so the job stays out of `completed` and
	// is re-claimable for a publication-only retry.
	Rendered(id, workerID string, artifact model.Artifact, reason string) error

	// Fail marks a running job failed. Jobs that have not exhausted their
	// attempts are requeued; otherwise they are permanently failed.
	Fail(id, workerID, reason string) error

	// Renew extends the lease for a running job owned by workerID.
	Renew(id, workerID string) error

	// RequeueExpired requeues (or permanently fails) jobs whose lease elapsed,
	// returning the number of jobs affected.
	RequeueExpired(now time.Time) (int, error)

	// Stats returns a snapshot of the queue state.
	Stats() model.Stats
}

// IdempotencyRepository optionally provides atomic submit-or-return-existing
// semantics for callers retrying the same logical render request.
type IdempotencyRepository interface {
	SubmitIdempotent(job model.Job) (*model.Job, bool, error)
}
