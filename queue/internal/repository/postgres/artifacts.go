package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// defaultArtifactKind is used when the artifact does not declare a kind.
const defaultArtifactKind = "segment"

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
		     profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe,
		     backend, chronon_version, drive_file_id, drive_link, container, pixel_format, audio_streams)
		VALUES
		    ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		ON CONFLICT (id) DO UPDATE SET
		    drive_file_id = EXCLUDED.drive_file_id,
		    drive_link    = EXCLUDED.drive_link`,
		a.ID, jobID, kind, a.StorageKey, nullIfEmpty(a.ArtifactURL), nullIfEmpty(a.ArtifactHash),
		nullIfEmpty(a.ContentType), a.SizeBytes, a.Width, a.Height, a.FPSNum, a.FPSDen,
		a.FrameCount, a.DurationUS, nullIfEmpty(a.ProfileID), a.CopyEligible,
		nullIfEmpty(a.Codec), nullIfEmpty(a.CodecProfile), a.ClosedGOP, a.FirstFrameKeyframe,
		nullIfEmpty(a.Backend), nullIfEmpty(a.ChrononVersion), nullIfEmpty(a.DriveFileID), nullIfEmpty(a.DriveLink),
		nullIfEmpty(a.Container), nullIfEmpty(a.PixelFormat), a.AudioStreams)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET artifact_id = $2
		WHERE id = $1`, jobID, a.ID)
	return err
}

func insertProcessingMetrics(ctx context.Context, tx *sql.Tx, jobID, attemptID string, values map[string]float64) error {
	for name, value := range values {
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO processing_metrics (job_id, attempt_id, metric_name, metric_value, unit)
			VALUES ($1, $2, $3, $4, 'ms')`, jobID, nullIfEmpty(attemptID), name, value); err != nil {
			return fmt.Errorf("insert processing metric %q: %w", name, err)
		}
	}
	return nil
}

// getArtifact loads a single artifact by ID.
func getArtifact(ctx context.Context, db *sql.DB, id string) (*model.Artifact, error) {
	var a model.Artifact
	var (
		storageKey, url, sha256, mimeType              sql.NullString
		profileID, codec, codecProfile                 sql.NullString
		backend, chrononVersion                        sql.NullString
		driveFileID, driveLink, container, pixelFormat sql.NullString
		audioStreams                                   sql.NullInt64
		sizeBytes                                      sql.NullInt64
		width, height, fpsNum, fpsDen                  sql.NullInt64
		frameCount, durationUS                         sql.NullInt64
		copyEligible, closedGOP, firstFrameKey         sql.NullBool
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, kind, storage_key, artifact_url, sha256, mime_type, size_bytes,
		       width, height, fps_num, fps_den, frame_count, duration_us,
		       profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe,
		       backend, chronon_version, drive_file_id, drive_link, container, pixel_format, audio_streams
		FROM render_artifacts
		WHERE id = $1`, id).Scan(
		&a.ID, &a.Kind, &storageKey, &url, &sha256, &mimeType, &sizeBytes,
		&width, &height, &fpsNum, &fpsDen, &frameCount, &durationUS,
		&profileID, &copyEligible, &codec, &codecProfile, &closedGOP, &firstFrameKey,
		&backend, &chrononVersion, &driveFileID, &driveLink, &container, &pixelFormat, &audioStreams)
	if err != nil {
		return nil, err
	}

	a.StorageKey = storageKey.String
	a.ArtifactURL = url.String
	a.ArtifactHash = sha256.String
	a.ContentType = mimeType.String
	a.ProfileID = profileID.String
	a.Codec = codec.String
	a.CodecProfile = codecProfile.String
	a.Backend = backend.String
	a.ChrononVersion = chrononVersion.String
	a.DriveFileID = driveFileID.String
	a.DriveLink = driveLink.String
	a.Container = container.String
	a.PixelFormat = pixelFormat.String
	if audioStreams.Valid {
		a.AudioStreams = int(audioStreams.Int64)
	}
	if sizeBytes.Valid {
		a.SizeBytes = sizeBytes.Int64
	}
	if width.Valid {
		a.Width = int(width.Int64)
	}
	if height.Valid {
		a.Height = int(height.Int64)
	}
	if fpsNum.Valid {
		a.FPSNum = int(fpsNum.Int64)
	}
	if fpsDen.Valid {
		a.FPSDen = int(fpsDen.Int64)
	}
	if frameCount.Valid {
		a.FrameCount = int(frameCount.Int64)
	}
	if durationUS.Valid {
		a.DurationUS = durationUS.Int64
	}
	if copyEligible.Valid {
		a.CopyEligible = copyEligible.Bool
	}
	if closedGOP.Valid {
		a.ClosedGOP = closedGOP.Bool
	}
	if firstFrameKey.Valid {
		a.FirstFrameKeyframe = firstFrameKey.Bool
	}
	return &a, nil
}
