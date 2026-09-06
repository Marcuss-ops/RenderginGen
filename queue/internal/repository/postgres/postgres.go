// Package postgres implements the job queue repository backed by PostgreSQL.
//
// Claim uses SELECT ... FOR UPDATE SKIP LOCKED so any number of workers can
// pull jobs concurrently without two of them ever receiving the same job.
// Every claim also creates a render_attempt and every state transition appends
// a render_event, so the full history of a job is preserved and never
// overwritten.
//
// File layout: postgres.go owns the repository core (submit/claim/get),
// finalize.go and lifecycle.go the terminal/transition writes, requeue.go the
// lease-expiry sweep, children.go the fan-in projection, stats.go the queue
// snapshot and decode.go the JSONB decode helpers.
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
