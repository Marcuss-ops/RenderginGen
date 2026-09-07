// lease.go owns the queue-lease renewal helpers shared by the worker pools:
// every stage runs under a lease that is renewed in the background and aborts
// the work as soon as the queue definitively reports the lease as lost.
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// sleepCtx sleeps for d or until ctx is cancelled; reports whether the sleep
// completed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// withLease runs fn while renewing the job's lease in the background.
// If the lease cannot be renewed (e.g. it expired and the job was requeued to
// another worker), the job context is cancelled so the work aborts instead of
// double-processing.
//
// A transient renewal failure (network blip, queue restart) must not abort a
// render that took minutes: each renewal attempt is retried with backoff, and
// the job only aborts when the queue definitively reports the lease as lost
// (a 409-class conflict from the queue) or every retry has been exhausted.
func withLeaseVoid(ctx context.Context, job *queue.Job, q *queue.Client, fn func(context.Context) error) error {
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
				if renewWithRetry(jobCtx, job.ID, q, interval) {
					continue
				}
				log.Printf("job %s: lease renew failed permanently, aborting", job.ID)
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
func renewWithRetry(ctx context.Context, jobID string, q *queue.Client, interval time.Duration) bool {
	const maxAttempts = 3
	backoff := 2 * time.Second
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
		log.Printf("job %s: lease renew attempt %d/%d failed, retrying: %v", jobID, attempt, maxAttempts, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff >= interval {
			backoff = interval
		}
	}
}

// withLease is withLeaseVoid for functions returning an artifact.
func withLease(ctx context.Context, job *queue.Job, q *queue.Client, fn func(context.Context) (queue.Artifact, error)) (queue.Artifact, error) {
	var artifact queue.Artifact
	err := withLeaseVoid(ctx, job, q, func(jobCtx context.Context) error {
		var err error
		artifact, err = fn(jobCtx)
		return err
	})
	return artifact, err
}
