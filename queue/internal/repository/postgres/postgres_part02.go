package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

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
	var decErr error
	if job.FrameRange, decErr = decodeFrameRange(frameRange); decErr != nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE render_jobs SET state='failed', failed_at=now(), error_message=$2, current_worker_id=NULL, lease_until=NULL WHERE id=$1`, id, decErr.Error())
		return nil, decErr
	}
	if job.Assets, decErr = decodeAssets(manifest); decErr != nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE render_jobs SET state='failed', failed_at=now(), error_message=$2, current_worker_id=NULL, lease_until=NULL WHERE id=$1`, id, decErr.Error())
		return nil, decErr
	}
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
