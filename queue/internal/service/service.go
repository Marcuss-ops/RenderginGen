// Package service implements the job queue business logic, sitting between
// the HTTP server and the storage backend. It owns rules such as input
// validation and emits operational metrics; storage concerns stay in the
// repository packages.
//
//	HTTP Server → Service → JobRepository → (memory | postgres)
package service

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// Service is the job queue application service.
type Service struct {
	repo       repository.JobRepository
	workerRepo repository.WorkerRepository
	staleAfter time.Duration
	retry      RetryConfig
	metrics    *metrics.Metrics
}

// New creates a service backed by the given repository.
func New(repo repository.JobRepository) *Service {
	return &Service{repo: repo}
}

// SetMetrics attaches optional Prometheus metrics. When nil, metric emission
// is a no-op so tests and lightweight deployments can run without them.
func (s *Service) SetMetrics(m *metrics.Metrics) {
	s.metrics = m
}

// SetRequeueRetry configures retry-with-backoff for RequeueExpired. The zero
// RetryConfig (the default) performs a single attempt with no retry.
func (s *Service) SetRequeueRetry(cfg RetryConfig) {
	s.retry = cfg
}

// SetWorkerRepository wires the worker registry and the heartbeat-staleness
// window used to classify workers as offline. A nil repository disables the
// worker surface and its metrics.
func (s *Service) SetWorkerRepository(repo repository.WorkerRepository, staleAfter time.Duration) {
	s.workerRepo = repo
	s.staleAfter = staleAfter
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Service) Submit(job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if err := s.repo.Submit(job); err != nil {
		return err
	}
	s.observePending()
	return nil
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
	job, lease, err := s.repo.Claim(workerID)
	if err != nil {
		return nil, 0, err
	}
	if job != nil && s.metrics != nil && !job.QueuedAt.IsZero() {
		s.metrics.QueueWait.Observe(time.Since(job.QueuedAt).Seconds())
	}
	s.observePending()
	return job, lease, nil
}

// Complete marks a running job as completed and records its artifact.
func (s *Service) Complete(id, workerID string, artifact model.Artifact) error {
	if err := s.repo.Complete(id, workerID, artifact); err != nil {
		return err
	}
	if s.metrics != nil {
		if job, err := s.repo.Get(id); err == nil && job != nil && !job.StartedAt.IsZero() {
			d := job.CompletedAt.Sub(job.StartedAt)
			if d <= 0 {
				d = time.Since(job.StartedAt)
			}
			s.metrics.RenderDuration.Observe(d.Seconds())
		}
	}
	s.observePending()
	return nil
}

// Fail marks a running job failed (requeue or permanent fail).
func (s *Service) Fail(id, workerID, reason string) error {
	if err := s.repo.Fail(id, workerID, reason); err != nil {
		return err
	}
	s.observePending()
	return nil
}

// Renew extends the lease for a running job owned by workerID.
func (s *Service) Renew(id, workerID string) error {
	return s.repo.Renew(id, workerID)
}

// RequeueExpired requeues (or permanently fails) jobs whose lease elapsed,
// returning the number of jobs affected. Transient repository failures are
// retried with exponential backoff and jitter (see SetRequeueRetry).
func (s *Service) RequeueExpired(now time.Time) (int, error) {
	maxAttempts := s.retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var n int
	var err error
	for attempt := 1; ; attempt++ {
		n, err = s.repo.RequeueExpired(now)
		if err == nil || attempt >= maxAttempts {
			break
		}
		time.Sleep(backoffDelay(s.retry, attempt))
	}
	if err != nil {
		return 0, err
	}
	if n > 0 && s.metrics != nil {
		s.metrics.LeaseExpired.Add(float64(n))
	}
	s.observePending()
	return n, nil
}

// Stats returns a snapshot of the queue state.
func (s *Service) Stats() model.Stats {
	return s.repo.Stats()
}

// observePending refreshes the pending gauge from the repository snapshot.
func (s *Service) observePending() {
	if s.metrics == nil {
		return
	}
	s.metrics.JobsPending.Set(float64(s.repo.Stats().Pending))
}
