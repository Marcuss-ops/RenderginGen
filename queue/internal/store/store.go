// Package store implements the in-memory central job queue.
//
// Jobs are pulled (claimed) by workers instead of being pushed. A claim
// carries a lease: if the worker dies before completing, the lease expires
// and the job is requeued for another worker.
package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// State is the lifecycle state of a job.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
)

// AssetRef points at an asset in the central artifact store.
type AssetRef struct {
	Hash string `json:"hash"`
	URL  string `json:"url"`
}

// Job is a unit of work in the queue.
type Job struct {
	ID          string          `json:"id"`
	OverlaySpec json.RawMessage `json:"overlay_spec"`
	Assets      []AssetRef      `json:"assets"`

	State      State     `json:"state"`
	Worker     string    `json:"worker,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
	FailReason string    `json:"fail_reason,omitempty"`
}

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
type Stats struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Depth     int `json:"depth"`
}

// Store is a thread-safe in-memory job queue with lease expiry.
type Store struct {
	mu          sync.Mutex
	jobs        map[string]*Job
	order       []string // FIFO order of pending job IDs
	lease       time.Duration
	maxAttempts int
}

// New creates a queue with the given lease duration and max attempts per job.
func New(lease time.Duration, maxAttempts int) *Store {
	return &Store{
		jobs:        make(map[string]*Job),
		lease:       lease,
		maxAttempts: maxAttempts,
	}
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Store) Submit(job Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	job.State = StatePending
	job.CreatedAt = time.Now()
	s.jobs[job.ID] = &job
	s.order = append(s.order, job.ID)
	return nil
}

// Claim atomically claims the oldest pending job for a worker and returns it
// with its lease duration. It returns nil when the queue is empty.
func (s *Store) Claim(workerID string) (*Job, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, id := range s.order {
		job := s.jobs[id]
		if job == nil || job.State != StatePending {
			continue
		}
		s.order = append(s.order[:i], s.order[i+1:]...)
		job.State = StateRunning
		job.Worker = workerID
		job.Attempts++
		job.LeaseUntil = time.Now().Add(s.lease)
		return job, s.lease, nil
	}
	return nil, 0, nil
}

// Complete marks a running job as completed.
func (s *Store) Complete(id, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = StateCompleted
	return nil
}

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (s *Store) Fail(id, workerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.FailReason = reason
	if s.maxAttempts > 0 && job.Attempts >= s.maxAttempts {
		job.State = StateFailed
		return nil
	}
	job.State = StatePending
	job.Worker = ""
	s.order = append(s.order, id)
	return nil
}

// RequeueExpired moves running jobs whose lease has elapsed back to pending,
// or permanently fails them if they exhausted their attempts. It returns the
// number of jobs affected.
func (s *Store) RequeueExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, job := range s.jobs {
		if job.State != StateRunning || job.LeaseUntil.IsZero() || !now.After(job.LeaseUntil) {
			continue
		}
		if s.maxAttempts > 0 && job.Attempts >= s.maxAttempts {
			job.State = StateFailed
			job.FailReason = "lease expired, max attempts reached"
		} else {
			job.State = StatePending
			job.Worker = ""
			s.order = append(s.order, job.ID)
		}
		n++
	}
	return n
}

// Depth returns the number of pending jobs.
func (s *Store) Depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.order)
}

// Stats returns a snapshot of the queue state.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := Stats{Depth: len(s.order)}
	for _, job := range s.jobs {
		switch job.State {
		case StatePending:
			stats.Pending++
		case StateRunning:
			stats.Running++
		case StateCompleted:
			stats.Completed++
		case StateFailed:
			stats.Failed++
		}
	}
	return stats
}

// runningJob returns the job if it is running and owned by workerID.
func (s *Store) runningJob(id, workerID string) (*Job, error) {
	job := s.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("job %s not found", id)
	}
	if job.State != StateRunning {
		return nil, fmt.Errorf("job %s is not running", id)
	}
	if job.Worker != workerID {
		return nil, fmt.Errorf("job %s is owned by %s, not %s", id, job.Worker, workerID)
	}
	return job, nil
}
