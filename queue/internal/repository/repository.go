// Package repository defines the persistence contract for the central job
// queue. The in-memory store and the PostgreSQL backend both implement it, so
// the HTTP server and the lease-expiry loop never know which one is in use.
package repository

import (
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// JobRepository is the storage contract for the central job queue.
type JobRepository interface {
	// Submit enqueues a job. The ID is required and must be unique.
	Submit(job model.Job) error

	// Claim atomically claims the next pending job for a worker, returning the
	// job and its lease duration. It returns a nil job when the queue is empty.
	Claim(workerID string) (*model.Job, time.Duration, error)

	// Complete marks a running job as completed.
	Complete(id, workerID string) error

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
