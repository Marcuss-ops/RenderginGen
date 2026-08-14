package postgres

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// defaultArtifactKind is used when the artifact does not declare a kind.
const defaultArtifactKind = "overlay"

// insertArtifact persists the artifact metadata and links it to the job via
// render_jobs.artifact_id. It is called inside the Complete transaction so the
// job state, attempt, event and artifact are committed atomically.
func insertArtifact(ctx context.Context, tx *sql.Tx, jobID string, a model.Artifact) error {
	kind := a.Kind
	if kind == "" {
		kind = defaultArtifactKind
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO render_artifacts
		    (id, job_id, kind, storage_key, artifact_url, sha256, mime_type, size_bytes,
		     width, height, fps_num, fps_den, frame_count, duration_us,
		     profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe)
		VALUES
		    ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, jobID, kind, a.StorageKey, nullIfEmpty(a.URL), nullIfEmpty(a.SHA256),
		nullIfEmpty(a.MimeType), a.SizeBytes, a.Width, a.Height, a.FPSNum, a.FPSDen,
		a.FrameCount, a.DurationUS, nullIfEmpty(a.ProfileID), a.CopyEligible,
		nullIfEmpty(a.Codec), nullIfEmpty(a.CodecProfile), a.ClosedGOP, a.FirstFrameKeyframe)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET artifact_id = $2
		WHERE id = $1`, jobID, a.ID)
	return err
}
