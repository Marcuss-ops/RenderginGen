// Package postgres implements the job queue repository backed by PostgreSQL.
//
// Claim uses SELECT ... FOR UPDATE SKIP LOCKED so any number of workers can
// pull jobs concurrently without two of them ever receiving the same job.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// defaultJobType is used until the Job model carries an explicit type.
const defaultJobType = "overlay"

// Repository is the PostgreSQL backend for the central job queue.
type Repository struct {
	db          *sql.DB
	lease       time.Duration
	maxAttempts int
}

// Compile-time check that Repository satisfies the repository contract.
var _ repository.JobRepository = (*Repository)(nil)

// New creates a PostgreSQL-backed job repository.
func New(db *sql.DB, lease time.Duration, maxAttempts int) *Repository {
	return &Repository{db: db, lease: lease, maxAttempts: maxAttempts}
}

// inputManifest is the JSONB shape stored in render_jobs.input_manifest.
type inputManifest struct {
	Assets []model.AssetRef `json:"assets"`
}

// Submit enqueues a job. The ID is required and must be unique.
func (r *Repository) Submit(job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	overlay, err := normalizeJSON(job.OverlaySpec)
	if err != nil {
		return fmt.Errorf("overlay_spec: %w", err)
	}
	manifest, err := json.Marshal(inputManifest{Assets: job.Assets})
	if err != nil {
		return fmt.Errorf("input_manifest: %w", err)
	}

	res, err := r.db.ExecContext(context.Background(), `
		INSERT INTO render_jobs (id, job_type, overlay_spec, input_manifest, max_attempts)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING`,
		job.ID, defaultJobType, overlay, manifest, r.maxAttempts)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	return nil
}

// Claim atomically claims the highest-priority, longest-waiting pending job
// for a worker, holding it under a lease. It returns nil when no job is
// pending.
func (r *Repository) Claim(workerID string) (*model.Job, time.Duration, error) {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	now := time.Now()
	leaseUntil := now.Add(r.lease)

	var (
		id       string
		overlay  []byte
		manifest []byte
		attempts int
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, overlay_spec, input_manifest, attempt_count
		FROM render_jobs
		WHERE state = 'pending'
		ORDER BY priority DESC, queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`).Scan(&id, &overlay, &manifest, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'running',
		    current_worker_id = $2,
		    lease_until = $3,
		    started_at = COALESCE(started_at, $4),
		    attempt_count = attempt_count + 1
		WHERE id = $1`, id, workerID, leaseUntil, now); err != nil {
		return nil, 0, err
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}

	job := &model.Job{
		ID:          id,
		OverlaySpec: json.RawMessage(overlay),
		Assets:      decodeAssets(manifest),
		State:       model.StateRunning,
		Worker:      workerID,
		Attempts:    attempts + 1,
		LeaseUntil:  leaseUntil,
	}
	return job, r.lease, nil
}

// Complete marks a running job as completed.
func (r *Repository) Complete(id, workerID string) error {
	return r.transition(id, workerID, `
		UPDATE render_jobs
		SET state = 'completed', completed_at = now()
		WHERE id = $1 AND state = 'running' AND current_worker_id = $2`)
}

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (r *Repository) Fail(id, workerID, reason string) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	err = tx.QueryRowContext(ctx, `
		SELECT attempt_count, max_attempts
		FROM render_jobs
		WHERE id = $1 AND state = 'running' AND current_worker_id = $2
		FOR UPDATE`, id, workerID).Scan(&attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}
	if err != nil {
		return err
	}

	var update string
	if maxAttempts > 0 && attempts >= maxAttempts {
		update = `
			UPDATE render_jobs
			SET state = 'failed', failed_at = now(), error_message = $2,
			    current_worker_id = NULL, lease_until = NULL
			WHERE id = $1`
	} else {
		update = `
			UPDATE render_jobs
			SET state = 'pending', error_message = $2, queued_at = now(),
			    current_worker_id = NULL, lease_until = NULL
			WHERE id = $1`
	}
	if _, err := tx.ExecContext(ctx, update, id, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// Renew extends the lease for a running job owned by workerID.
func (r *Repository) Renew(id, workerID string) error {
	return r.transition(id, workerID, `
		UPDATE render_jobs
		SET lease_until = $3
		WHERE id = $1 AND state = 'running' AND current_worker_id = $2`,
		time.Now().Add(r.lease))
}

// RequeueExpired permanently fails expired jobs that exhausted their attempts
// and requeues the rest. It returns the number of jobs affected.
func (r *Repository) RequeueExpired(now time.Time) (int, error) {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	failed, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'failed', failed_at = now(), current_worker_id = NULL,
		    lease_until = NULL, error_message = 'lease expired, max attempts reached'
		WHERE state = 'running' AND lease_until IS NOT NULL AND lease_until < $1
		  AND max_attempts > 0 AND attempt_count >= max_attempts`, now)
	if err != nil {
		return 0, err
	}
	requeued, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'pending', current_worker_id = NULL, lease_until = NULL, queued_at = now()
		WHERE state = 'running' AND lease_until IS NOT NULL AND lease_until < $1`, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	nFailed, _ := failed.RowsAffected()
	nRequeued, _ := requeued.RowsAffected()
	return int(nFailed + nRequeued), nil
}

// Stats returns a snapshot of the queue state.
func (r *Repository) Stats() model.Stats {
	var stats model.Stats
	err := r.db.QueryRowContext(context.Background(), `
		SELECT
			COALESCE(count(*) FILTER (WHERE state = 'pending'), 0),
			COALESCE(count(*) FILTER (WHERE state = 'running'), 0),
			COALESCE(count(*) FILTER (WHERE state = 'completed'), 0),
			COALESCE(count(*) FILTER (WHERE state = 'failed'), 0)
		FROM render_jobs`).Scan(&stats.Pending, &stats.Running, &stats.Completed, &stats.Failed)
	if err != nil {
		return model.Stats{}
	}
	stats.Depth = stats.Pending
	return stats
}

// transition runs a single-row UPDATE that must affect exactly one running job
// owned by workerID, returning an error otherwise.
func (r *Repository) transition(id, workerID, query string, extra ...any) error {
	args := append([]any{id, workerID}, extra...)
	res, err := r.db.ExecContext(context.Background(), query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}
	return nil
}

// normalizeJSON returns the raw JSON, defaulting empty input to "{}".
func normalizeJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}"), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid json")
	}
	return raw, nil
}

// decodeAssets extracts the asset references from an input_manifest JSONB.
func decodeAssets(raw []byte) []model.AssetRef {
	if len(raw) == 0 {
		return nil
	}
	var m inputManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m.Assets
}
