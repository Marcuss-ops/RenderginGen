package memory

import (
	"fmt"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
	"time"
)

// Retry resets a failed job back to pending state for re-execution.
func (s *Repository) Retry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if job.State != model.StateFailed {
		return fmt.Errorf("job %s is in state %q, cannot retry non-failed job", id, job.State)
	}
	job.State = model.StatePending
	job.Worker = ""
	job.FailReason = ""
	job.QueuedAt = time.Now()
	job.LeaseUntil = time.Time{}
	job.Progress = nil
	s.order = append(s.order, id)
	return nil
}

// Cancel moves a job that has not reached a terminal state to cancelled.
// Cancelled is terminal: ClaimState skips it (it is no longer pending or
// rendered) and RequeueExpired only touches running/finalizing jobs, so a
// cancelled job is never claimed again and never requeued after its lease
// elapses. Cancelling an already-cancelled job is a no-op; cancelling a
// completed/failed job is an error. Attempts are deliberately NOT incremented:
// every claim that would have invoked Chronon already happened.
func (s *Repository) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	switch job.State {
	case model.StatePending, model.StateRunning, model.StateRendered, model.StateFinalizing:
		job.State = model.StateCancelled
		job.Worker = ""
		job.LeaseUntil = time.Time{}
		job.Progress = nil
		return nil
	case model.StateCancelled:
		// Idempotent: the producer already cancelled this job.
		return nil
	default:
		return fmt.Errorf("job %s is in state %q, cannot cancel terminal job", id, job.State)
	}
}

// SetProgress stores the latest render progress from the lease-owning worker.
// It rejects reports for jobs that are not running or whose lease moved to
// another worker, mirroring the PostgreSQL backend.
func (s *Repository) SetProgress(id, workerID string, p model.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	copy := p
	copy.Worker = workerID
	copy.LastFrameAt = time.Now()
	job.Progress = &copy
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

	stats := model.Stats{}
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
	// Depth counts claimable render work only, matching PostgreSQL (where the
	// gauge reads state='pending'). Rendered-state jobs are claimable too but
	// are not render work; the gauge exists for autoscaling render capacity.
	stats.Ok = true
	stats.Depth = stats.Pending
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
