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
	"sort"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// Repository is a thread-safe in-memory job queue with lease expiry.
type Repository struct {
	mu          sync.Mutex
	jobs        map[string]*model.Job
	artifacts   map[string]model.Artifact
	order       []string // FIFO order of pending job IDs
	workers     map[string]model.Worker
	lease       time.Duration
	maxAttempts int
}

// Compile-time check that Repository satisfies the repository contracts.
var (
	_ repository.JobRepository         = (*Repository)(nil)
	_ repository.WorkerRepository      = (*Repository)(nil)
	_ repository.IdempotencyRepository = (*Repository)(nil)
)

// New creates a queue with the given lease duration and max attempts per job.
func New(lease time.Duration, maxAttempts int) *Repository {
	return &Repository{
		jobs:        make(map[string]*model.Job),
		artifacts:   make(map[string]model.Artifact),
		workers:     make(map[string]model.Worker),
		lease:       lease,
		maxAttempts: maxAttempts,
	}
}

func (s *Repository) SubmitIdempotent(job model.Job) (*model.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ID == "" {
		return nil, false, fmt.Errorf("job id is required")
	}
	for _, existing := range s.jobs {
		if job.IdempotencyKey != "" && existing.IdempotencyKey == job.IdempotencyKey {
			copy := *existing
			return &copy, false, nil
		}
	}
	if _, exists := s.jobs[job.ID]; exists {
		return nil, false, fmt.Errorf("job %s already exists", job.ID)
	}
	now := time.Now()
	if err := validateChunkMetadata(job); err != nil {
		return nil, false, err
	}
	job.State, job.CreatedAt, job.QueuedAt = model.StatePending, now, now
	s.jobs[job.ID] = &job
	s.order = append(s.order, job.ID)
	copy := job
	return &copy, true, nil
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
	now := time.Now()
	if err := validateChunkMetadata(job); err != nil {
		return err
	}
	job.State = model.StatePending
	job.CreatedAt = now
	job.QueuedAt = now
	s.jobs[job.ID] = &job
	s.order = append(s.order, job.ID)
	return nil
}

func validateChunkMetadata(job model.Job) error {
	if job.FrameRange != nil && (job.FrameRange.Start < 0 || job.FrameRange.End <= job.FrameRange.Start) {
		return fmt.Errorf("invalid frame_range for job %s", job.ID)
	}
	if job.ChunkIndex < 0 {
		return fmt.Errorf("invalid chunk_index for job %s", job.ID)
	}
	return nil
}

// Claim atomically claims the oldest pending job for a worker and returns it
// with its lease duration. It returns nil when the queue is empty.
func (s *Repository) Claim(workerID string) (*model.Job, time.Duration, error) {
	return s.ClaimState(workerID, "")
}

func (s *Repository) ClaimState(workerID string, state model.State) (*model.Job, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, id := range s.order {
		job := s.jobs[id]
		if job == nil || (state != "" && job.State != state) ||
			(state == "" && job.State != model.StatePending && job.State != model.StateRendered) {
			continue
		}
		s.order = append(s.order[:i], s.order[i+1:]...)
		job.State = model.StateRunning
		job.Worker = workerID
		job.Attempts++
		job.StartedAt = time.Now()
		job.LeaseUntil = job.StartedAt.Add(s.lease)
		if artifact, ok := s.artifacts[id]; ok {
			copy := artifact
			job.Artifact = &copy
		}
		return job, s.lease, nil
	}
	return nil, 0, nil
}

// Get returns the current state of a job, including its artifact when done.
func (s *Repository) Get(id string) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job := s.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	copy := *job
	if artifact, ok := s.artifacts[id]; ok {
		copy.Artifact = &artifact
	}
	return &copy, nil
}

// Children returns child chunks in deterministic chunk order.
func (s *Repository) Children(parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	children := make([]*model.Job, 0)
	for _, job := range s.jobs {
		if job.ParentJobID != parentJobID {
			continue
		}
		copy := *job
		if artifact, ok := s.artifacts[job.ID]; ok {
			artifactCopy := artifact
			copy.Artifact = &artifactCopy
		}
		children = append(children, &copy)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].ChunkIndex < children[j].ChunkIndex
	})
	return children, nil
}

// ClaimFinalization atomically claims a parent row for one finalizer.
func (s *Repository) ClaimFinalization(parentJobID, workerID string) (*model.Job, bool, error) {
	if parentJobID == "" || workerID == "" {
		return nil, false, fmt.Errorf("parent job id and worker id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[parentJobID]
	if job == nil {
		return nil, false, fmt.Errorf("parent job %s: %w", parentJobID, repository.ErrNotFound)
	}
	if job.State == model.StateFinalizing {
		return nil, false, nil
	}
	if job.State != model.StatePending && job.State != model.StateRunning {
		return nil, false, fmt.Errorf("parent job %s is in state %q", parentJobID, job.State)
	}
	job.State = model.StateFinalizing
	job.Worker = workerID
	job.LeaseUntil = time.Now().Add(s.lease)
	copy := *job
	return &copy, true, nil
}

// Complete marks a running job as completed and records its artifact.
func (s *Repository) Complete(id, workerID string, artifact model.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateCompleted
	job.CompletedAt = time.Now()
	s.artifacts[id] = artifact
	return nil
}

// Rendered marks a running job as rendered: its artifact is durably stored, but
// external publication failed, so the job stays out of `completed` and is
// re-claimable for a publication-only retry.
func (s *Repository) Rendered(id, workerID string, artifact model.Artifact, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateRendered
	job.FailReason = reason
	job.Worker = ""
	job.QueuedAt = time.Now()
	s.artifacts[id] = artifact
	s.order = append(s.order, id)
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
	job.QueuedAt = time.Now()
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
		if (job.State != model.StateRunning && job.State != model.StateFinalizing) || job.LeaseUntil.IsZero() || !now.After(job.LeaseUntil) {
			continue
		}
		if s.maxAttempts > 0 && job.Attempts >= s.maxAttempts {
			job.State = model.StateFailed
			job.FailReason = "lease expired, max attempts reached"
		} else {
			job.State = model.StatePending
			job.Worker = ""
			job.QueuedAt = time.Now()
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
	if job.State != model.StateRunning && job.State != model.StateFinalizing {
		return nil, fmt.Errorf("job %s is not running or finalizing", id)
	}
	if job.Worker != workerID {
		return nil, fmt.Errorf("job %s is owned by %s, not %s", id, job.Worker, workerID)
	}
	return job, nil
}
