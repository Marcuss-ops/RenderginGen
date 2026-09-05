package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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

func insertRenderTelemetry(ctx context.Context, tx *sql.Tx, jobID, attemptID string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var doc struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode chronon telemetry: %w", err)
	}
	if doc.Schema == "" {
		return fmt.Errorf("chronon telemetry schema is missing")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO render_telemetry (job_id, attempt_id, schema, telemetry)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (job_id, attempt_id) DO UPDATE SET schema=EXCLUDED.schema, telemetry=EXCLUDED.telemetry`,
		jobID, nullIfEmpty(attemptID), doc.Schema, raw)
	return err
}

func insertProcessingMetrics(ctx context.Context, tx *sql.Tx, jobID, attemptID string, values map[string]float64) error {
	for name, value := range values {
		if name == "" {
			continue
		}
		unit := metricUnit(name)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO processing_metrics (job_id, attempt_id, metric_name, metric_value, unit)
			VALUES ($1, $2, $3, $4, $5)`, jobID, nullIfEmpty(attemptID), name, value, unit); err != nil {
			return fmt.Errorf("insert processing metric %q: %w", name, err)
		}
	}
	return nil
}

// metricUnit preserves the unit encoded by the canonical metric name. Names
// without a suffix are counts by default; this keeps legacy map payloads
// compatible while preventing bytes/frames/ratios from being labelled ms.
func metricUnit(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, "_us"):
		return "us"
	case strings.HasSuffix(n, "_ms"):
		return "ms"
	case strings.Contains(n, "bytes"):
		return "bytes"
	case strings.HasSuffix(n, "_mb"):
		return "mb"
	case strings.Contains(n, "fps"):
		return "fps"
	case strings.Contains(n, "ratio"), strings.HasSuffix(n, "_percent"):
		return "ratio"
	default:
		return "count"
	}
}

// getArtifact loads a single artifact by ID.
func getArtifact(ctx context.Context, db *sql.DB, id string) (*model.Artifact, error) {
	var a model.Artifact
	var (
		jobID                                          string
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
		SELECT id, job_id, kind, storage_key, artifact_url, sha256, mime_type, size_bytes,
		       width, height, fps_num, fps_den, frame_count, duration_us,
		       profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe,
		       backend, chronon_version, drive_file_id, drive_link, container, pixel_format, audio_streams
		FROM render_artifacts
		WHERE id = $1`, id).Scan(
		&a.ID, &jobID, &a.Kind, &storageKey, &url, &sha256, &mimeType, &sizeBytes,
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
	// processing_metrics is the durable numeric projection of Chronon's
	// timing sidecar. It must be read back with the artifact; otherwise the
	// queue API exposes only media facts (for example frame_count) and silently
	// drops FPS, render phases and GPU counters before PipelineGen can project
	// them into localized_renders.metrics.
	rows, err := db.QueryContext(ctx, `
		SELECT metric_name, metric_value
		FROM processing_metrics
		WHERE job_id = $1
		ORDER BY created_at ASC, id ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("load processing metrics for artifact %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var value float64
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan processing metric for artifact %s: %w", id, err)
		}
		if name != "" {
			if a.Metrics == nil {
				a.Metrics = make(map[string]float64)
			}
			// Later rows win, which is correct for a retried job where the
			// newest attempt is appended after the previous attempt.
			a.Metrics[name] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate processing metrics for artifact %s: %w", id, err)
	}
	return &a, nil
}
