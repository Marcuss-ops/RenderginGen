package service

import (
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"time"
)

// Retry resets a failed job back to pending state.
func (s *Service) Retry(id string) error {
	if err := s.repo.Retry(id); err != nil {
		return err
	}
	s.notify.Notify()
	s.observePending()
	return nil
}

// Cancel moves a job that has not reached a terminal state to cancelled.
// Cancelled is terminal: claim never selects it and lease expiry never
// requeues it, so the job can never be re-rendered across lease/claim cycles.
// Cancelling an already-cancelled job is a no-op.
func (s *Service) Cancel(id string) error {
	if err := s.repo.Cancel(id); err != nil {
		return err
	}
	// Wake claim waiters: a cancelled pending job is no longer claimable, so a
	// blocked claim must re-check the store instead of sleeping on a job that
	// will never arrive.
	s.notify.Notify()
	s.observePending()
	return nil
}

// SetProgress records the worker's render progress for a running job. The
// queue is the source of truth: progress survives worker restarts and is
// visible through GET /jobs/{id} without asking the worker directly.
func (s *Service) SetProgress(id, workerID string, p model.Progress) error {
	return s.repo.SetProgress(id, workerID, p)
}

// Stats returns a snapshot of the queue state.
func (s *Service) Stats() model.Stats {
	return s.repo.Stats()
}

// observePending refreshes the pending gauge from the repository snapshot,
// throttled to pendingGaugeInterval so high-frequency transitions (claim,
// complete, requeue across many workers) cannot turn the gauge into a
// full-table scan per request. State-changing paths still notify claim
// waiters through s.notify, which is what latency depends on — the gauge is
// observability, not correctness.
func (s *Service) observePending() {
	s.observePendingThrottled(false)
}

// observePendingThrottled is the shared implementation; force bypasses the
// throttle clock.
func (s *Service) observePendingThrottled(force bool) {
	if s.metrics == nil {
		return
	}
	// Read/advance the throttle clock under the lock, then fetch the stats
	// OUTSIDE it: repo.Stats() is a full-table COUNT(*) FILTER scan on
	// PostgreSQL, and every state-changing path (Claim, Complete, Fail,
	// Rendered, RequeueExpired) funnels through here. Holding the service-wide
	// mutex across that scan would serialize the whole queue hot path behind
	// observability on a large render_jobs table. A concurrent caller may now
	// pass the throttle and double-issue one scan; that is bounded, benign
	// contention, not a correctness hazard — the gauge is observability.
	s.pendingGaugeMu.Lock()
	if !force && !s.pendingGaugeLast.IsZero() && time.Since(s.pendingGaugeLast) < pendingGaugeInterval {
		s.pendingGaugeMu.Unlock()
		return
	}
	s.pendingGaugeLast = time.Now()
	s.pendingGaugeMu.Unlock()

	s.metrics.JobsPending.Set(float64(s.repo.Stats().Pending))
}

// RefreshPendingGauge forces an immediate, unthrottled pending-gauge refresh.
// Tests and administrative endpoints use this to read a synchronous snapshot
// without waiting out pendingGaugeInterval.
func (s *Service) RefreshPendingGauge() {
	s.observePendingThrottled(true)
}
