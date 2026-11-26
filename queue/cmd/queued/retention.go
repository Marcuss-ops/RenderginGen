// retention.go owns the age-based retention of the append-only
// `render_events` ledger.
//
// `render_events` records every lifecycle transition of a job and grew without
// bound: nothing in the queue deleted old rows, and the environment's only
// retention paths covered the PipelineGen SQLite databases. The ledger is
// history, not state (`render_jobs` is the current state), so rows older than
// the retention window are safe to drop.
//
// Deletion is batched so one sweep never holds a long transaction on a live
// queue, and every statement is bounded by the created_at index added in
// migration 033.
package main

import (
	"context"
	"database/sql"
	"time"
)

const (
	// renderEventsRetentionBatch bounds one DELETE, so the prune never builds
	// an unbounded transaction on the queue database.
	renderEventsRetentionBatch = 5000
	// renderEventsRetentionSweep is how often the prune runs while the queue
	// is up. The first sweep runs at startup.
	renderEventsRetentionSweep = 6 * time.Hour
)

// pruneRenderEvents deletes render_events rows created before cutoff in
// batches and returns the total number of deleted rows. It is safe to call
// with a cutoff in the future (deletes nothing).
func pruneRenderEvents(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		result, err := db.ExecContext(ctx, `
			DELETE FROM render_events
			WHERE id IN (
				SELECT id FROM render_events
				WHERE created_at < $1
				ORDER BY created_at
				LIMIT $2
			)`, cutoff, renderEventsRetentionBatch)
		if err != nil {
			return total, err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted < renderEventsRetentionBatch {
			return total, nil
		}
	}
}

// runRenderEventsRetention prunes once at startup and then every
// renderEventsRetentionSweep until the root context is cancelled. A retention
// window <= 0 disables the sweep (an operator can turn it off explicitly).
func runRenderEventsRetention(ctx context.Context, db *sql.DB, olderThan time.Duration, logf func(string, ...any)) {
	if olderThan <= 0 {
		logf("render_events retention disabled (window %s)", olderThan)
		return
	}
	sweep := func() {
		cutoff := time.Now().Add(-olderThan)
		deleted, err := pruneRenderEvents(ctx, db, cutoff)
		if err != nil {
			if ctx.Err() == nil {
				logf("render_events retention failed: %v", err)
			}
			return
		}
		if deleted > 0 {
			logf("render_events retention: deleted %d rows older than %s", deleted, cutoff.Format(time.RFC3339))
		}
	}
	sweep()
	ticker := time.NewTicker(renderEventsRetentionSweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("render_events retention stopped")
			return
		case <-ticker.C:
			sweep()
		}
	}
}
