// requeue.go owns the lease-expiry sweep: permanently failing expired jobs
// that exhausted their attempts and requeueing the rest, each with a closed
// attempt and an appended event.
package postgres

import (
	"context"
	"time"
)

// RequeueExpired permanently fails expired jobs that exhausted their attempts
// and requeues the rest, recording the attempt outcome and an event for each.
func (r *Repository) RequeueExpired(now time.Time) (int, error) {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, attempt_count, max_attempts
		FROM render_jobs
		WHERE state IN ('running', 'finalizing') AND lease_until IS NOT NULL AND lease_until < $1
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return 0, err
	}

	type expiredJob struct {
		id          string
		attempts    int
		maxAttempts int
	}
	var expired []expiredJob
	for rows.Next() {
		var e expiredJob
		if err := rows.Scan(&e.id, &e.attempts, &e.maxAttempts); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, e := range expired {
		attempt, err := runningAttemptID(ctx, tx, e.id)
		if err != nil {
			return 0, err
		}
		if err := finishAttempt(ctx, tx, attempt, attemptStatusLeaseExpired, "", "lease expired"); err != nil {
			return 0, err
		}

		if e.maxAttempts > 0 && e.attempts >= e.maxAttempts {
			if _, err := tx.ExecContext(ctx, `
				UPDATE render_jobs
				SET state = 'failed', failed_at = now(), current_worker_id = NULL,
				    lease_until = NULL, error_message = 'lease expired, max attempts reached'
				WHERE id = $1`, e.id); err != nil {
				return 0, err
			}
			if err := recordEvent(ctx, tx, eventJobFailed, e.id, attempt, "", map[string]any{"reason": "lease expired, max attempts reached"}); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE render_jobs
				SET state = 'pending', current_worker_id = NULL, lease_until = NULL, queued_at = now()
				WHERE id = $1`, e.id); err != nil {
				return 0, err
			}
			if err := recordEvent(ctx, tx, eventJobRequeued, e.id, attempt, "", map[string]any{"reason": "lease expired"}); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(expired), nil
}
