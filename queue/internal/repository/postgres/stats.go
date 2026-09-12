// stats.go owns the queue snapshot query used by dashboards and autoscalers.
package postgres

import (
	"context"
	"log"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// Stats returns a snapshot of the queue state. A database error is logged and
// reported through the Ok flag: silently returning zeros would make dashboards
// and autoscalers read an empty queue during an outage.
func (r *Repository) Stats() model.Stats {
	var stats model.Stats
	err := r.db.QueryRowContext(context.Background(), `
		SELECT
			COALESCE(count(*) FILTER (WHERE state = `+stateLiteral(model.StatePending)+`), 0),
			COALESCE(count(*) FILTER (WHERE state = `+stateLiteral(model.StateRunning)+`), 0),
			COALESCE(count(*) FILTER (WHERE state = `+stateLiteral(model.StateCompleted)+`), 0),
			COALESCE(count(*) FILTER (WHERE state = `+stateLiteral(model.StateFailed)+`), 0)
		FROM render_jobs`).Scan(&stats.Pending, &stats.Running, &stats.Completed, &stats.Failed)
	if err != nil {
		log.Printf("queue stats query failed: %v", err)
		stats.Ok = false
		return stats
	}
	stats.Ok = true
	stats.Depth = stats.Pending
	return stats
}
