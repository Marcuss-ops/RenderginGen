// lease.go owns the queue-lease renewal helpers shared by the worker pools:
// every stage runs under a lease that is renewed in the background and aborts
// the work as soon as the queue definitively reports the lease as lost.
package main

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// sleepCtx sleeps for d or until ctx is cancelled; reports whether the sleep
// completed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// jitter20 spreads a backoff by ±20% so a fleet of workers restarting together
// does not retry the queue in lockstep (thundering herd).
func jitter20(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := d / 5
	j := (rand.Int63n(int64(2*delta+1)) - int64(delta))
	return d + time.Duration(j)
}

// withLeaseVoid runs fn while renewing the job's lease in the background.
// If the lease cannot be renewed (e.g. it expired and the job was requeued to
// another worker), the job context is cancelled so the work aborts instead of
// double-processing.
//
// A transient renewal failure (network blip, queue restart) must not abort a
// render that took minutes: each renewal attempt is retried with backoff, and
// the job only aborts when the queue definitively reports the lease as lost
// (a 409-class conflict from the queue) or every retry has been exhausted.
func withLeaseVoid(ctx context.Context, job *queue.Job, q *queue.Client, timings config.PipelineConfig, fn func(context.Context) error) error {
	if job.Lease <= 0 {
		return fn(ctx)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := job.Lease / 2
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if renewWithRetry(jobCtx, job.ID, q, interval, timings) {
					continue
				}
				workerlog.ByJobID(job.ID).Errorf("lease renew failed permanently, aborting")
				cancel()
				return
			}
		}
	}()
	return fn(jobCtx)
}

// renewWithRetry attempts one lease renewal with bounded backoff retries.
// It reports whether the lease is still held: true after a successful renew,
// false when the queue reports the job is no longer owned by this worker
// (permanent — the job was requeued elsewhere) or the retry budget is spent.
// Cancellation of ctx always reports false immediately.
func renewWithRetry(ctx context.Context, jobID string, q *queue.Client, interval time.Duration, timings config.PipelineConfig) bool {
	maxAttempts := timings.LeaseRenewAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	backoff := timings.LeaseRenewBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 1; ; attempt++ {
		err := q.Renew(ctx, jobID)
		if err == nil {
			return true
		}
		// A conflict means the lease is definitively gone (expired and
		// requeued, completed, or owned by another worker): no retry helps.
		if errors.Is(err, queue.RenewConflictError) {
			return false
		}
		if attempt >= maxAttempts || ctx.Err() != nil {
			return false
		}
		wait := jitter20(backoff)
		workerlog.ByJobID(jobID).Warnf("lease renew attempt %d/%d failed, retrying in %s: %v", attempt, maxAttempts, wait, err)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
		backoff *= 2
		if backoff >= interval {
			backoff = interval
		}
	}
}

// withLease is withLeaseVoid for functions returning an artifact.
func withLease(ctx context.Context, job *queue.Job, q *queue.Client, timings config.PipelineConfig, fn func(context.Context) (queue.Artifact, error)) (queue.Artifact, error) {
	var artifact queue.Artifact
	err := withLeaseVoid(ctx, job, q, timings, func(jobCtx context.Context) error {
		var err error
		artifact, err = fn(jobCtx)
		return err
	})
	return artifact, err
}
