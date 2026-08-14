package repository

import (
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// WorkerRepository is the storage contract for the rendering-worker registry
// and its heartbeat ledger. It is deliberately separate from JobRepository so
// the queue logic and the worker liveness tracking stay independent.
type WorkerRepository interface {
	// Register upserts a worker's identity and records an initial heartbeat.
	Register(worker model.Worker) error

	// Heartbeat records a heartbeat for a registered worker, updating its
	// last_heartbeat_at and appending to the heartbeat ledger.
	Heartbeat(workerID string) error

	// List returns all registered workers.
	List() ([]model.Worker, error)

	// Health returns the aggregate worker-health snapshot: Ready/Busy count
	// only workers with a heartbeat at or after now-staleAfter; Offline counts
	// the rest (including workers that never heartbeated).
	Health(now time.Time, staleAfter time.Duration) (model.WorkerHealth, error)
}
