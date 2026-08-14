// Package service implements the job queue business logic, sitting between
// the HTTP server and the storage backend. It owns rules such as input
// validation and is the place where event/metric emission will live; storage
// concerns stay in the repository packages.
//
//	HTTP Server → Service → JobRepository → (memory | postgres)
package service

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// Service is the job queue application service.
type Service struct {
	repo repository.JobRepository
}

// New creates a service backed by the given repository.
func New(repo repository.JobRepository) *Service {
	return &Service{repo: repo}
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Service) Submit(job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	return s.repo.Submit(job)
}

// Get returns the current state of a job, including its artifact when done.
func (s *Service) Get(id string) (*model.Job, error) {
	return s.repo.Get(id)
}

// Claim atomically claims the next pending job for a worker, returning the job
// and its lease duration. It returns a nil job when the queue is empty.
func (s *Service) Claim(workerID string) (*model.Job, time.Duration, error) {
	if workerID == "" {
		return nil, 0, fmt.Errorf("worker id is required")
	}
	return s.repo.Claim(workerID)
}

// Complete marks a running job as completed and records its artifact.
func (s *Service) Complete(id, workerID string, artifact model.Artifact) error {
	return s.repo.Complete(id, workerID, artifact)
}

// Fail marks a running job failed (requeue or permanent fail).
func (s *Service) Fail(id, workerID, reason string) error {
	return s.repo.Fail(id, workerID, reason)
}

// Renew extends the lease for a running job owned by workerID.
func (s *Service) Renew(id, workerID string) error {
	return s.repo.Renew(id, workerID)
}

// RequeueExpired requeues (or permanently fails) jobs whose lease elapsed,
// returning the number of jobs affected.
func (s *Service) RequeueExpired(now time.Time) (int, error) {
	return s.repo.RequeueExpired(now)
}

// Stats returns a snapshot of the queue state.
func (s *Service) Stats() model.Stats {
	return s.repo.Stats()
}
