package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Event types recorded in render_events. render_jobs.state describes how a job
// is *now*; these events reconstruct what actually happened.
const (
	eventJobCreated   = "JOB_CREATED"
	eventJobClaimed   = "JOB_CLAIMED"
	eventLeaseRenewed = "LEASE_RENEWED"
	eventJobCompleted = "JOB_COMPLETED"
	eventJobFailed    = "JOB_FAILED"
	eventJobRequeued  = "JOB_REQUEUED"
)

// recordEvent appends an event to render_events. Empty attempt/worker IDs are
// stored as NULL; a nil payload is stored as an empty JSON object.
func recordEvent(ctx context.Context, tx *sql.Tx, eventType, jobID, attemptID, workerID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO render_events (job_id, attempt_id, worker_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		jobID, nullIfEmpty(attemptID), nullIfEmpty(workerID), eventType, raw)
	return err
}
