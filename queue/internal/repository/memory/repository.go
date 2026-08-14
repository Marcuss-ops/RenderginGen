// Package memory implements the in-memory job queue, used as the default
// (non-persistent) backend for local development and tests. It implements
// repository.JobRepository, so it can be swapped for the PostgreSQL backend.
//
// Jobs are pulled (claimed) by workers instead of being pushed. A claim
// carries a lease: if the worker dies before completing, the lease expires
// and the job is requeued for another worker.
package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// Repository is a thread-safe in-memory job queue with lease expiry.
type Repository struct {
	mu          sync.Mutex
	jobs        map[string]*model.Job
	order       []string // FIFO order of pending job IDs
	lease       time.Duration
	maxAttempts int
}

// Compile-time check that Repository satisfies the repository contract.
var _ repository.JobRepository = (*Repository)(nil)

// New creates a queue with the given lease duration and max attempts per job.
func New(lease time.Duration, maxAttempts int) *Repository {
	return &Repository{
		jobs:        make(map[string]*model.Job),
		lease:       lease,
		maxAttempts: maxAttempts,
	}
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Repository) Submit(job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	job.State = model.StatePending
	job.CreatedAt = time.Now()
	s.jobs[job.ID] = &job
	s.order = append(s.order, job.ID)
	return nil
}

// Claim atomically claims the oldest pending job for a worker and returns it
// with its lease duration. It returns nil when the queue is empty.
func (s *Repository) Claim(workerID string) (*model.Job, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, id := range s.order {
		job := s.jobs[id]
		if job == nil || job.State != model.StatePending {
			continue
		}
		s.order = append(s.order[:i], s.order[i+1:]...)
		job.State = model.StateRunning
		job.Worker = workerID
		job.Attempts++
		job.LeaseUntil = time.Now().Add(s.lease)
		return job, s.lease, nil
	}
	return nil, 0, nil
}

// Complete marks a running job as completed.
func (s *Repository) Complete(id, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateCompleted
	return nil
}

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (s *Repository) Fail(id, workerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.FailReason = reason
	if s.maxAttempts > 0 && job.Attempts >= s.maxAttempts {
		job.State = model.StateFailed
		return nil
	}
	job.State = model.StatePending
	job.Worker = ""
	s.order = append(s.order, id)
	return nil
}

// Renew extends the lease for a running job owned by workerID.
func (s *Repository) Renew(id, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.LeaseUntil = time.Now().Add(s.lease)
	return nil
}

// RequeueExpired moves running jobs whose lease has elapsed back to pending,
// or permanently fails them if they exhausted their attempts. It returns the
// number of jobs affected.
func (s *Repository) RequeueExpired(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, job := range s.jobs {
		if job.State != model.StateRunning || job.LeaseUntil.IsZero() || !now.After(job.LeaseUntil) {
			continue
		}
		if s.maxAttempts > 0 && job.Attempts >= s.maxAttempts {
			job.State = model.StateFailed
			job.FailReason = "lease expired, max attempts reached"
		} else {
			job.State = model.StatePending
			job.Worker = ""
			s.order = append(s.order, job.ID)
		}
		n++
	}
	return n, nil
}

// Stats returns a snapshot of the queue state.
func (s *Repository) Stats() model.Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := model.Stats{Depth: len(s.order)}
	for _, job := range s.jobs {
		switch job.State {
		case model.StatePending:
			stats.Pending++
		case model.StateRunning:
			stats.Running++
		case model.StateCompleted:
			stats.Completed++
		case model.StateFailed:
			stats.Failed++
		}
	}
	return stats
}

// runningJob returns the job if it is running and owned by workerID.
func (s *Repository) runningJob(id, workerID string) (*model.Job, error) {
	job := s.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("job %s not found", id)
	}
	if job.State != model.StateRunning {
		return nil, fmt.Errorf("job %s is not running", id)
	}
	if job.Worker != workerID {
		return nil, fmt.Errorf("job %s is owned by %s, not %s", id, job.Worker, workerID)
	}
	return job, nil
}
