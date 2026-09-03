// Package service implements the job queue business logic, sitting between
// the HTTP server and the storage backend. It owns rules such as input
// validation and emits operational metrics; storage concerns stay in the
// repository packages.
//
//	HTTP Server → Service → JobRepository → (memory | postgres)
package service

import (
	"context"
	"fmt"
	"strings"
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
	notify     *Notifier
}

// New creates a service backed by the given repository.
func New(repo repository.JobRepository) *Service {
	return &Service{repo: repo, notify: NewNotifier()}
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
	_, _, err := s.SubmitIdempotent(job)
	return err
}

// SubmitIdempotent returns the canonical job for an idempotency key. created
// is false when the request is a retry of an existing logical job.
func (s *Service) SubmitIdempotent(job model.Job) (*model.Job, bool, error) {
	if job.ID == "" {
		return nil, false, fmt.Errorf("job id is required")
	}
	if idem, ok := s.repo.(repository.IdempotencyRepository); ok {
		canonical, created, err := idem.SubmitIdempotent(job)
		if err != nil {
			return nil, false, err
		}
		if created {
			s.observePending()
			s.notify.Notify()
		}
		return canonical, created, nil
	}
	if err := s.repo.Submit(job); err != nil {
		return nil, false, err
	}
	s.observePending()
	s.notify.Notify()
	canonical, err := s.repo.Get(job.ID)
	return canonical, true, err
}

// Notify returns the service's wake-up channel stream. The server uses it to
// long-poll claims: the signal carries no data and never assigns work, it only
// wakes a waiting claim to re-run the atomic claim against the store.
func (s *Service) Notify() *Notifier { return s.notify }

func (s *Service) ClaimFinalization(parentID, workerID string) (*model.Job, bool, error) {
	job, claimed, err := s.repo.ClaimFinalization(parentID, workerID)
	if claimed {
		s.notify.Notify()
	}
	return job, claimed, err
}

func (s *Service) Children(parentID string) ([]*model.Job, error) { return s.repo.Children(parentID) }

// Get returns the current state of a job, including its artifact when done.
func (s *Service) Get(id string) (*model.Job, error) {
	return s.repo.Get(id)
}

// Claim atomically claims the next pending job for a worker, returning the job
// and its lease duration. It returns a nil job when the queue is empty.
func (s *Service) Claim(workerID string) (*model.Job, time.Duration, error) {
	return s.ClaimState(workerID, "")
}

func (s *Service) ClaimState(workerID string, state model.State) (*model.Job, time.Duration, error) {
	if workerID == "" {
		return nil, 0, fmt.Errorf("worker id is required")
	}
	job, lease, err := s.repo.ClaimState(workerID, state)
	if err != nil {
		return nil, 0, err
	}
	if job != nil && s.metrics != nil && !job.QueuedAt.IsZero() {
		s.metrics.QueueWait.Observe(time.Since(job.QueuedAt).Seconds())
	}
	s.observePending()
	return job, lease, nil
}

// WaitAndClaim long-polls for a claimable job. It re-runs the atomic claim
// immediately on every wake-up and falls back to a bounded tick so spurious
// signal loss (or a repository without notifications) cannot stall a worker.
// The wait never assigns work: every successful claim goes through ClaimState
// and the store remains the single source of truth.
func (s *Service) WaitAndClaim(ctx context.Context, workerID string, state model.State, maxWait time.Duration) (*model.Job, time.Duration, error) {
	if workerID == "" {
		return nil, 0, fmt.Errorf("worker id is required")
	}
	if maxWait <= 0 {
		maxWait = 25 * time.Second
	}
	job, lease, err := s.ClaimState(workerID, state)
	if err != nil || job != nil {
		return job, lease, err
	}
	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	wake := s.notify.Done()
	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-deadline.C:
			return nil, 0, nil
		case <-wake:
			job, lease, err := s.ClaimState(workerID, state)
			if err != nil || job != nil {
				return job, lease, err
			}
			// Signal consumed by a competing worker; keep waiting.
			wake = s.notify.Done()
		case <-time.After(time.Second):
			// Bounded fallback re-poll so a missed wake-up cannot stall a
			// worker until the deadline.
			job, lease, err := s.ClaimState(workerID, state)
			if err != nil || job != nil {
				return job, lease, err
			}
		}
	}
}

func (s *Service) Complete(id, workerID string, artifact model.Artifact) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
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

// validateArtifact is the queue-side completion gate. A worker may only move
// a job to completed after it has published a verifiable artifact reference;
// an empty payload would create a completed job with no durable output.
func validateArtifact(artifact model.Artifact) error {
	if strings.TrimSpace(artifact.StorageKey) == "" {
		return fmt.Errorf("artifact storage_key is required")
	}
	if strings.TrimSpace(artifact.ArtifactHash) == "" {
		return fmt.Errorf("artifact sha256 is required")
	}
	if artifact.SizeBytes <= 0 {
		return fmt.Errorf("artifact size_bytes must be positive")
	}
	if strings.TrimSpace(artifact.ContentType) == "" {
		return fmt.Errorf("artifact content_type is required")
	}
	return nil
}

// Rendered marks a running job as rendered: the artifact is durably stored but
// external publication failed, so the job stays out of `completed` and is
// re-claimable for a publication-only retry.
func (s *Service) Rendered(id, workerID string, artifact model.Artifact, reason string) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	if err := s.repo.Rendered(id, workerID, artifact, reason); err != nil {
		return err
	}
	s.observePending()
	s.notify.Notify()
	return nil
}

// Fail marks a running job failed (requeue or permanent fail).
func (s *Service) Fail(id, workerID, reason string) error {
	if err := s.repo.Fail(id, workerID, reason); err != nil {
		return err
	}
	s.observePending()
	s.notify.Notify()
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
	if n > 0 {
		s.notify.Notify()
	}
	s.observePending()
	return n, nil
}

// Retry resets a failed job back to pending state.
func (s *Service) Retry(id string) error {
	if err := s.repo.Retry(id); err != nil {
		return err
	}
	s.notify.Notify()
	s.observePending()
	return nil
}

// ValidateChildren enforces the parent chunk contract: exact expected count,
// indices 0..N-1, contiguous half-open frame ranges, and completed artifacts.
func ValidateChildren(children []*model.Job, expected int64, start, end int64) error {
	if expected <= 0 || end <= start {
		return fmt.Errorf("invalid parent chunk contract")
	}
	if int64(len(children)) != expected {
		return fmt.Errorf("chunk count %d, want %d", len(children), expected)
	}
	cursor := start
	for i, child := range children {
		if child == nil || child.ChunkIndex != i {
			return fmt.Errorf("chunk index at position %d is invalid", i)
		}
		if child.State != model.StateCompleted || child.Artifact == nil || child.Artifact.StorageKey == "" {
			return fmt.Errorf("chunk %d is not completed with an artifact", i)
		}
		if child.FrameRange == nil || child.FrameRange.Start != cursor || child.FrameRange.End <= child.FrameRange.Start {
			return fmt.Errorf("chunk %d has gap, overlap, or invalid frame range", i)
		}
		cursor = child.FrameRange.End
	}
	if cursor != end {
		return fmt.Errorf("chunk ranges end at %d, want %d", cursor, end)
	}
	return nil
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
