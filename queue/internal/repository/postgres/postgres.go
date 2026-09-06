// Package postgres implements the job queue repository backed by PostgreSQL.
//
// Claim uses SELECT ... FOR UPDATE SKIP LOCKED so any number of workers can
// pull jobs concurrently without two of them ever receiving the same job.
// Every claim also creates a render_attempt and every state transition appends
// a render_event, so the full history of a job is preserved and never
// overwritten.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

// defaultJobType is the job type recorded for render jobs. A job is one
// render SEGMENT (a full chronon.render-plan), not a single overlay.
const defaultJobType = model.JobTypeRenderSegment

func nonEmptyJobType(jobType string) string {
	if jobType == "" {
		return defaultJobType
	}
	return jobType
}

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
	if job.FrameRange != nil && (job.FrameRange.Start < 0 || job.FrameRange.End <= job.FrameRange.Start) {
		return fmt.Errorf("invalid frame_range for job %s", job.ID)
	}
	if job.ChunkIndex < 0 {
		return fmt.Errorf("invalid chunk_index for job %s", job.ID)
	}
	schema := job.Schema
	if schema == "" {
		schema = model.JobSchemaV1
	}
	version := job.Version
	if version == 0 {
		version = model.JobSchemaVersionV1
	}
	plan, err := normalizeJSON(job.RenderPlan)
	if err != nil {
		return fmt.Errorf("render_plan: %w", err)
	}
	manifest, err := json.Marshal(inputManifest{Assets: job.Assets})
	if err != nil {
		return fmt.Errorf("input_manifest: %w", err)
	}

	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO render_jobs (id, job_type, job_schema, job_schema_version, render_plan, input_manifest, max_attempts, idempotency_key, parent_job_id, chunk_index, frame_range)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11)
		ON CONFLICT (id) DO NOTHING`,
		job.ID, nonEmptyJobType(job.JobType), schema, version, plan, manifest, r.maxAttempts, job.IdempotencyKey, job.ParentJobID, job.ChunkIndex, job.FrameRange)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
	}

	if err := recordEvent(ctx, tx, eventJobCreated, job.ID, "", "", nil); err != nil {
		return err
	}
	return tx.Commit()
}

// SubmitIdempotent uses the database uniqueness constraint as the race-safe
// winner selection for concurrent retries of the same logical request.
func (r *Repository) SubmitIdempotent(job model.Job) (*model.Job, bool, error) {
	if job.IdempotencyKey == "" {
		if err := r.Submit(job); err != nil {
			return nil, false, err
		}
		canonical, err := r.Get(job.ID)
		return canonical, true, err
	}
	var existingID string
	err := r.db.QueryRow(`SELECT id FROM render_jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID)
	if err == nil {
		canonical, getErr := r.Get(existingID)
		return canonical, false, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if err := r.Submit(job); err == nil {
		canonical, getErr := r.Get(job.ID)
		return canonical, true, getErr
	}
	// Another submitter may have won the unique-key race.
	if err := r.db.QueryRow(`SELECT id FROM render_jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID); err != nil {
		return nil, false, err
	}
	canonical, getErr := r.Get(existingID)
	return canonical, false, getErr
}

// Claim atomically claims the longest-waiting pending job for a worker,
// holding it under a lease. It returns nil when no job is pending.
func (r *Repository) Claim(workerID string) (*model.Job, time.Duration, error) {
	return r.ClaimState(workerID, "")
}

// claimStateFilters maps a claimable state to its SQL predicate. The map is
// compile-time fixed: no caller input ever reaches the query text — the
// accepted states are whitelisted and rendered through this table, never via
// string concatenation of the caller's value.
var claimStateFilters = map[model.State]string{
	model.StatePending:  "state = 'pending'",
	model.StateRendered: "state = 'rendered'",
}

func (r *Repository) ClaimState(workerID string, state model.State) (*model.Job, time.Duration, error) {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	now := time.Now()
	leaseUntil := now.Add(r.lease)

	var (
		id          string
		jobType     string
		schema      string
		version     sql.NullInt64
		plan        []byte
		manifest    []byte
		attempts    int
		queuedAt    time.Time
		artifactID  sql.NullString
		parentJobID sql.NullString
		chunkIndex  int
		frameRange  []byte
	)
	stateFilter := "state IN ('pending', 'rendered')"
	if state != "" {
		filter, ok := claimStateFilters[state]
		if !ok {
			return nil, 0, fmt.Errorf("unsupported claim state %q", state)
		}
		stateFilter = filter
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id, job_type, job_schema, job_schema_version, render_plan, input_manifest, attempt_count, queued_at, artifact_id, parent_job_id, chunk_index, frame_range
		FROM render_jobs
		WHERE `+stateFilter+`
		ORDER BY queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`).Scan(&id, &jobType, &schema, &version, &plan, &manifest, &attempts, &queuedAt, &artifactID, &parentJobID, &chunkIndex, &frameRange)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	var maxRecordedAttempt int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number), 0) FROM render_attempts WHERE job_id = $1`, id).Scan(&maxRecordedAttempt); err != nil {
		return nil, 0, err
	}
	attemptNumber := attempts + 1
	if maxRecordedAttempt >= attemptNumber {
		attemptNumber = maxRecordedAttempt + 1
	}
	if err := createAttempt(ctx, tx, id, attemptNumber, workerID, now); err != nil {
		return nil, 0, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'running',
		    current_worker_id = $2,
		    lease_until = $3,
		    started_at = $4,
		    attempt_count = $5
		WHERE id = $1`, id, workerID, leaseUntil, now, attemptNumber); err != nil {
		return nil, 0, err
	}

	if err := recordEvent(ctx, tx, eventJobClaimed, id, attemptID(id, attemptNumber), workerID, nil); err != nil {
		return nil, 0, err
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}

	job := &model.Job{ID: id,
		ParentJobID: parentJobID.String,
		ChunkIndex:  chunkIndex,
		FrameRange:  decodeFrameRange(frameRange),
		JobType:     jobType,
		Schema:      schema,
		Version:     schemaVersion(version),
		RenderPlan:  json.RawMessage(plan),
		Assets:      decodeAssets(manifest),
		State:       model.StateRunning,
		Worker:      workerID,
		Attempts:    attemptNumber,
		QueuedAt:    queuedAt,
		StartedAt:   now,
		LeaseUntil:  leaseUntil,
	}
	// A job re-claimed from the rendered state carries its already-stored
	// artifact so the worker can skip rendering and only retry publication.
	if artifactID.Valid {
		artifact, err := getArtifact(ctx, r.db, artifactID.String)
		if err != nil {
			return nil, 0, fmt.Errorf("job %s artifact %s: %w", id, artifactID.String, err)
		}
		job.Artifact = artifact
	}
	return job, r.lease, nil
}

// Get returns the current state of a job, including its artifact when done.
func (r *Repository) Get(id string) (*model.Job, error) {
	ctx := context.Background()

	var (
		job            model.Job
		state          string
		schema         string
		version        sql.NullInt64
		plan           []byte
		manifest       []byte
		worker         sql.NullString
		queuedAt       sql.NullTime
		startedAt      sql.NullTime
		completedAt    sql.NullTime
		leaseUntil     sql.NullTime
		errorMsg       sql.NullString
		artifactID     sql.NullString
		idempotencyKey sql.NullString
		parentJobID    sql.NullString
		chunkIndex     int
		frameRange     []byte
		pFramesDone    sql.NullInt64
		pTotalFrames   sql.NullInt64
		pLastFrameAt   sql.NullTime
		pWorker        sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, state, job_type, job_schema, job_schema_version, render_plan,
		       input_manifest, attempt_count,
		       current_worker_id, queued_at, started_at, completed_at,
		       lease_until, error_message, artifact_id, idempotency_key,
		       parent_job_id, chunk_index, frame_range,
		       progress_frames_done, progress_total_frames, progress_last_frame_at, progress_worker
		FROM render_jobs
		WHERE id = $1`, id).Scan(
		&job.ID, &state, &job.JobType, &schema, &version, &plan,
		&manifest, &job.Attempts,
		&worker, &queuedAt, &startedAt, &completedAt,
		&leaseUntil, &errorMsg, &artifactID, &idempotencyKey,
		&parentJobID, &chunkIndex, &frameRange,
		&pFramesDone, &pTotalFrames, &pLastFrameAt, &pWorker)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	job.State = model.State(state)
	job.Schema = schema
	job.Version = schemaVersion(version)
	job.RenderPlan = json.RawMessage(plan)
	job.IdempotencyKey = idempotencyKey.String
	job.ParentJobID = parentJobID.String
	job.ChunkIndex = chunkIndex
	job.FrameRange = decodeFrameRange(frameRange)
	job.Assets = decodeAssets(manifest)
	if worker.Valid {
		job.Worker = worker.String
	}
	if queuedAt.Valid {
		job.QueuedAt = queuedAt.Time
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time
	}
	if leaseUntil.Valid {
		job.LeaseUntil = leaseUntil.Time
	}
	if errorMsg.Valid {
		job.FailReason = errorMsg.String
	}

	if pLastFrameAt.Valid {
		job.Progress = &model.Progress{
			FramesDone:  int(pFramesDone.Int64),
			TotalFrames: int(pTotalFrames.Int64),
			LastFrameAt: pLastFrameAt.Time,
			Worker:      pWorker.String,
		}
	}

	if artifactID.Valid {
		artifact, err := getArtifact(ctx, r.db, artifactID.String)
		if err != nil {
			return nil, err
		}
		job.Artifact = artifact
	}
	return &job, nil
}

// ClaimFinalization atomically claims a parent row for one finalizer.
func (r *Repository) ClaimFinalization(parentJobID, workerID string) (*model.Job, bool, error) {
	if parentJobID == "" || workerID == "" {
		return nil, false, fmt.Errorf("parent job id and worker id are required")
	}
	res, err := r.db.ExecContext(context.Background(), `
		UPDATE render_jobs
		SET state = 'finalizing', current_worker_id = $2, started_at = now(),
		    lease_until = now() + make_interval(secs => $3)
		WHERE id = $1 AND state IN ('pending', 'running')`, parentJobID, workerID, r.lease.Seconds())
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		job, getErr := r.Get(parentJobID)
		if getErr != nil {
			return nil, false, getErr
		}
		if job.State == model.StateFinalizing || job.State == model.StateCompleted {
			return job, false, nil
		}
		return job, false, fmt.Errorf("parent job %s is in state %q", parentJobID, job.State)
	}
	job, err := r.Get(parentJobID)
	return job, true, err
}

// Complete marks a running job as completed and, when the artifact has a
// storage key, persists it and links it to the job.
func (r *Repository) Complete(id, workerID string, artifact model.Artifact) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'completed', completed_at = now()
		WHERE id = $1 AND state IN ('running', 'finalizing') AND current_worker_id = $2`, id, workerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusCompleted, "", ""); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventJobCompleted, id, attempt, workerID, nil); err != nil {
		return err
	}

	if artifact.StorageKey != "" {
		if artifact.ID == "" {
			artifact.ID = id // one artifact per job
		}
		if err := insertArtifact(ctx, tx, id, artifact); err != nil {
			return err
		}
	}
	if err := insertProcessingMetrics(ctx, tx, id, attempt, artifact.Metrics); err != nil {
		return err
	}
	if err := insertRenderTelemetry(ctx, tx, id, attempt, artifact.ChrononTelemetry); err != nil {
		return err
	}
	return tx.Commit()
}

// Rendered marks a running job as rendered: its artifact is durably stored in
// the object store but external publication (Google Drive) failed. The job is
// kept out of `completed` and becomes claimable again for a publication-only
// retry, so a flaky upload never wastes a GPU re-render.
func (r *Repository) Rendered(id, workerID string, artifact model.Artifact, reason string) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'rendered', error_message = $2, queued_at = now(),
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1 AND state = 'running' AND current_worker_id = $3`, id, reason, workerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusRendered, "drive_upload_failed", reason); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventJobRendered, id, attempt, workerID, map[string]any{"reason": reason}); err != nil {
		return err
	}

	if artifact.StorageKey != "" {
		if artifact.ID == "" {
			artifact.ID = id // one artifact per job
		}
		if err := insertArtifact(ctx, tx, id, artifact); err != nil {
			return err
		}
	}
	if err := insertProcessingMetrics(ctx, tx, id, attempt, artifact.Metrics); err != nil {
		return err
	}
	if err := insertRenderTelemetry(ctx, tx, id, attempt, artifact.ChrononTelemetry); err != nil {
		return err
	}
	return tx.Commit()
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

	permanent := maxAttempts > 0 && attempts >= maxAttempts

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusFailed, "", reason); err != nil {
		return err
	}

	var update string
	if permanent {
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

	eventType := eventJobRequeued
	if permanent {
		eventType = eventJobFailed
	}
	if err := recordEvent(ctx, tx, eventType, id, attempt, workerID, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return tx.Commit()
}

// Renew extends the lease for a running job owned by workerID.
func (r *Repository) Renew(id, workerID string) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET lease_until = $3
		WHERE id = $1 AND state IN ('running', 'finalizing') AND current_worker_id = $2`,
		id, workerID, time.Now().Add(r.lease))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE render_attempts
		SET last_renewed_at = now()
		WHERE id = $1`, attempt); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventLeaseRenewed, id, attempt, workerID, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// Retry resets a failed job back to pending state for re-execution.
func (r *Repository) Retry(id string) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM render_jobs WHERE id = $1 FOR UPDATE`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if state != string(model.StateFailed) {
		return fmt.Errorf("job %s is in state %q, cannot retry non-failed job", id, state)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = 'pending', error_message = NULL, queued_at = now(),
		    current_worker_id = NULL, lease_until = NULL,
		    progress_frames_done = 0, progress_total_frames = 0,
		    progress_last_frame_at = NULL, progress_worker = ''
		WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if err := recordEvent(ctx, tx, eventJobRequeued, id, "", "", map[string]any{"action": "retry"}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProgress stores the latest render progress from the lease-owning worker.
// The conditional UPDATE (state='running' AND current_worker_id=workerID)
// makes a stale worker's report a no-op instead of a corruption vector: the
// report either lands on the current owner's row or affects zero rows.
func (r *Repository) SetProgress(id, workerID string, p model.Progress) error {
	if id == "" || workerID == "" {
		return fmt.Errorf("job id and worker id are required")
	}
	if p.FramesDone < 0 || p.TotalFrames < 0 {
		return fmt.Errorf("job %s: negative progress values are invalid", id)
	}
	if p.TotalFrames > 0 && p.FramesDone > p.TotalFrames {
		return fmt.Errorf("job %s: frames_done %d exceeds frames_total %d", id, p.FramesDone, p.TotalFrames)
	}
	res, err := r.db.ExecContext(context.Background(), `
		UPDATE render_jobs
		SET progress_frames_done = $2,
		    progress_total_frames = $3,
		    progress_last_frame_at = now(),
		    progress_worker = $4
		WHERE id = $1 AND state = 'running' AND current_worker_id = $4`,
		id, p.FramesDone, p.TotalFrames, workerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "unknown job" from "not the lease owner" for callers.
		var state string
		err := r.db.QueryRowContext(context.Background(), `SELECT state FROM render_jobs WHERE id = $1`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if state != string(model.StateRunning) {
			return fmt.Errorf("job %s is in state %q, cannot report progress for non-running job", id, state)
		}
		return fmt.Errorf("job %s is owned by another worker", id)
	}
	return nil
}

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

// Stats returns a snapshot of the queue state. A database error is logged and
// reported through the Ok flag: silently returning zeros would make dashboards
// and autoscalers read an empty queue during an outage.
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
		log.Printf("queue stats query failed: %v", err)
		stats.Ok = false
		return stats
	}
	stats.Ok = true
	stats.Depth = stats.Pending
	return stats
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

// schemaVersion maps a nullable job_schema_version column to an int,
// defaulting to the v1 envelope version when the column is NULL (legacy rows).
func schemaVersion(v sql.NullInt64) int {
	if !v.Valid {
		return model.JobSchemaVersionV1
	}
	return int(v.Int64)
}

// decodeAssets extracts the asset references from an input_manifest JSONB.
func decodeFrameRange(raw []byte) *model.FrameRange {
	if len(raw) == 0 {
		return nil
	}
	var result model.FrameRange
	if err := json.Unmarshal(raw, &result); err != nil {
		// Corrupted JSONB must never degrade silently into "no frame range":
		// a nil range changes render semantics (full clip instead of the
		// declared chunk). Surface the corruption on every read.
		log.Printf("postgres: corrupt frame_range jsonb (%d bytes), treating as no range: %v", len(raw), err)
		return nil
	}
	return &result
}

func decodeAssets(raw []byte) []model.AssetRef {
	if len(raw) == 0 {
		return nil
	}
	var m inputManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		// Corrupted JSONB would silently drop every declared asset (and thus
		// their SHA-256 verification at the worker boundary). Surface it.
		log.Printf("postgres: corrupt input_manifest jsonb (%d bytes), treating as no assets: %v", len(raw), err)
		return nil
	}
	return m.Assets
}

// Children returns child chunks in deterministic chunk order.
//
// The projection deliberately excludes render_plan and input_manifest: the
// finalizer's fan-in calls this after every child completion, and loading the
// full plan JSONB per child would turn the fan-in into an O(children²)
// transfer of large blobs nobody reads. A child's frame range, state and
// artifact are all the validation and assembly need.
func (r *Repository) Children(parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	rows, err := r.db.QueryContext(context.Background(), `
		SELECT j.id, j.state, j.chunk_index, j.frame_range,
		       a.id, a.storage_key, a.artifact_url, a.sha256, a.mime_type, a.size_bytes,
		       a.width, a.height, a.fps_num, a.fps_den, a.frame_count, a.duration_us,
		       a.profile_id, a.copy_eligible, a.codec, a.codec_profile, a.closed_gop,
		       a.first_frame_keyframe, a.backend, a.chronon_version,
		       a.drive_file_id, a.drive_link, a.container, a.pixel_format, a.audio_streams
		FROM render_jobs j
		LEFT JOIN render_artifacts a ON a.id = j.artifact_id
		WHERE j.parent_job_id = $1
		ORDER BY j.chunk_index ASC`, parentJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var children []*model.Job
	for rows.Next() {
		var (
			id, state  string
			chunkIndex int
			frameRange []byte
			// Nullable mirrors so a NULL join row (child without an artifact
			// yet) stays a nil job.Artifact.
			nArtifactID, nStorageKey, nArtifactURL, nSHA256, nMimeType   sql.NullString
			nSizeBytes, nWidth, nHeight, nFPSNum, nFPSDen                sql.NullInt64
			nFrameCount, nDurationUS, nAudioStreams                      sql.NullInt64
			nProfileID, nCodec, nCodecProfile, nBackend, nChrononVersion sql.NullString
			nDriveFileID, nDriveLink, nContainer, nPixelFormat           sql.NullString
			nCopyEligible, nClosedGOP, nFirstFrameKeyframe               sql.NullBool
		)
		if err := rows.Scan(
			&id, &state, &chunkIndex, &frameRange,
			&nArtifactID, &nStorageKey, &nArtifactURL, &nSHA256, &nMimeType, &nSizeBytes,
			&nWidth, &nHeight, &nFPSNum, &nFPSDen, &nFrameCount, &nDurationUS,
			&nProfileID, &nCopyEligible, &nCodec, &nCodecProfile, &nClosedGOP,
			&nFirstFrameKeyframe, &nBackend, &nChrononVersion,
			&nDriveFileID, &nDriveLink, &nContainer, &nPixelFormat, &nAudioStreams,
		); err != nil {
			return nil, err
		}
		job := &model.Job{
			ID:          id,
			State:       model.State(state),
			ParentJobID: parentJobID,
			ChunkIndex:  chunkIndex,
			FrameRange:  decodeFrameRange(frameRange),
		}
		if nArtifactID.Valid {
			job.Artifact = &model.Artifact{
				ID:                 nArtifactID.String,
				Kind:               "segment",
				StorageKey:         nStorageKey.String,
				ArtifactURL:        nArtifactURL.String,
				ArtifactHash:       nSHA256.String,
				ContentType:        nMimeType.String,
				SizeBytes:          nSizeBytes.Int64,
				Width:              int(nWidth.Int64),
				Height:             int(nHeight.Int64),
				FPSNum:             int(nFPSNum.Int64),
				FPSDen:             int(nFPSDen.Int64),
				FrameCount:         int(nFrameCount.Int64),
				DurationUS:         nDurationUS.Int64,
				ProfileID:          nProfileID.String,
				CopyEligible:       nCopyEligible.Bool,
				Codec:              nCodec.String,
				CodecProfile:       nCodecProfile.String,
				ClosedGOP:          nClosedGOP.Bool,
				FirstFrameKeyframe: nFirstFrameKeyframe.Bool,
				Backend:            nBackend.String,
				ChrononVersion:     nChrononVersion.String,
				DriveFileID:        nDriveFileID.String,
				DriveLink:          nDriveLink.String,
				Container:          nContainer.String,
				PixelFormat:        nPixelFormat.String,
				AudioStreams:       int(nAudioStreams.Int64),
			}
		}
		children = append(children, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return children, nil
}

// nullIfEmpty converts an empty string to SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
