package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// artifactRow is the raw nullable projection of one render_artifacts row plus
// the job id used by the metrics/telemetry side tables.
type artifactRow struct {
	artifact                                    model.Artifact
	jobID                                       string
	storageKey, url, sha256, mimeType           sql.NullString
	profileID, codec, codecProfile              sql.NullString
	backend, chrononVersion                     sql.NullString
	driveFileID, driveLink, container, pixelFmt sql.NullString
	chrononTimingKey, chrononTimingURL          sql.NullString
	chrononTimingSHA, chrononTimingContentType  sql.NullString
	audioStreams                                sql.NullInt64
	outputFacts, provenance, verification       []byte
	sizeBytes, chrononTimingSize                sql.NullInt64
	width, height, fpsNum, fpsDen               sql.NullInt64
	frameCount, durationUS                      sql.NullInt64
	copyEligible, closedGOP, firstFrameKey      sql.NullBool
}

// queryArtifactRow loads the artifact row itself. Scan errors are returned
// unchanged, matching getArtifact's historical contract.
// artifactQuerier is the read surface an artifact hydration needs. Both
// *sql.DB and *sql.Tx satisfy it, so the claim path can hydrate the artifacts
// of the jobs it is about to hand out INSIDE the claim transaction instead of
// after it has committed.
type artifactQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// artifactReadColumns is the one projection every full artifact read uses.
// Keeping it in a constant is what lets the single read and the batch read
// stay byte-for-byte identical instead of drifting into two row shapes.
const artifactReadColumns = `id, job_id, kind, storage_key, artifact_url, sha256, mime_type, size_bytes,
	       width, height, fps_num, fps_den, frame_count, duration_us,
	       profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe,
	       backend, chronon_version, drive_file_id, drive_link, container, pixel_format, audio_streams,
	       chronon_timing_storage_key, chronon_timing_url, chronon_timing_sha256,
	       chronon_timing_size_bytes, chronon_timing_content_type, output_facts, provenance, verification`

// rowScanner is the scan surface shared by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanArtifactRow(s rowScanner) (*artifactRow, error) {
	row := &artifactRow{}
	err := s.Scan(
		&row.artifact.ID, &row.jobID, &row.artifact.Kind, &row.storageKey, &row.url, &row.sha256, &row.mimeType, &row.sizeBytes,
		&row.width, &row.height, &row.fpsNum, &row.fpsDen, &row.frameCount, &row.durationUS,
		&row.profileID, &row.copyEligible, &row.codec, &row.codecProfile, &row.closedGOP, &row.firstFrameKey,
		&row.backend, &row.chrononVersion, &row.driveFileID, &row.driveLink, &row.container, &row.pixelFmt, &row.audioStreams,
		&row.chrononTimingKey, &row.chrononTimingURL, &row.chrononTimingSHA,
		&row.chrononTimingSize, &row.chrononTimingContentType, &row.outputFacts, &row.provenance, &row.verification)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func queryArtifactRow(ctx context.Context, db artifactQuerier, id string) (*artifactRow, error) {
	return scanArtifactRow(db.QueryRowContext(ctx, `
		SELECT `+artifactReadColumns+`
		FROM render_artifacts
		WHERE id = $1`, id))
}

// queryArtifactRows loads every requested artifact row in ONE statement. The
// claim path used to run this query (plus metrics and telemetry) once per
// claimed job, so a batch of n re-claimed jobs cost 3n round trips.
func queryArtifactRows(ctx context.Context, db artifactQuerier, ids []string) ([]*artifactRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+artifactReadColumns+`
		FROM render_artifacts
		WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*artifactRow
	for rows.Next() {
		row, err := scanArtifactRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// toModel maps the nullable columns into the public artifact shape. A corrupt
// provenance document is a real error; output_facts stays fail-open.
func (row *artifactRow) toModel(id string) (*model.Artifact, error) {
	a := row.artifact
	a.StorageKey = row.storageKey.String
	a.ArtifactURL = row.url.String
	a.ArtifactHash = row.sha256.String
	a.ContentType = row.mimeType.String
	a.ProfileID = row.profileID.String
	a.Codec = row.codec.String
	a.CodecProfile = row.codecProfile.String
	a.Backend = row.backend.String
	a.ChrononVersion = row.chrononVersion.String
	a.DriveFileID = row.driveFileID.String
	a.DriveLink = row.driveLink.String
	a.Container = row.container.String
	a.PixelFormat = row.pixelFmt.String
	if len(row.outputFacts) > 0 {
		var facts model.OutputFacts
		if err := json.Unmarshal(row.outputFacts, &facts); err == nil {
			a.OutputFacts = &facts
		}
	}
	if len(row.provenance) > 0 {
		var value model.ArtifactProvenance
		if err := json.Unmarshal(row.provenance, &value); err != nil {
			return nil, fmt.Errorf("decode artifact provenance %s: %w", id, err)
		}
		a.Provenance = &value
	}
	if len(row.verification) > 0 {
		var value model.ArtifactVerification
		if err := json.Unmarshal(row.verification, &value); err != nil {
			return nil, fmt.Errorf("decode artifact verification %s: %w", id, err)
		}
		a.Verification = &value
	}
	a.ChrononTimingStorageKey = row.chrononTimingKey.String
	a.ChrononTimingURL = row.chrononTimingURL.String
	a.ChrononTimingSHA256 = row.chrononTimingSHA.String
	a.ChrononTimingContentType = row.chrononTimingContentType.String
	if row.chrononTimingSize.Valid {
		a.ChrononTimingSizeBytes = row.chrononTimingSize.Int64
	}
	if row.audioStreams.Valid {
		a.AudioStreams = int(row.audioStreams.Int64)
	}
	if row.sizeBytes.Valid {
		a.SizeBytes = row.sizeBytes.Int64
	}
	if row.width.Valid {
		a.Width = int(row.width.Int64)
	}
	if row.height.Valid {
		a.Height = int(row.height.Int64)
	}
	if row.fpsNum.Valid {
		a.FPSNum = int(row.fpsNum.Int64)
	}
	if row.fpsDen.Valid {
		a.FPSDen = int(row.fpsDen.Int64)
	}
	if row.frameCount.Valid {
		a.FrameCount = int(row.frameCount.Int64)
	}
	if row.durationUS.Valid {
		a.DurationUS = row.durationUS.Int64
	}
	if row.copyEligible.Valid {
		a.CopyEligible = row.copyEligible.Bool
	}
	if row.closedGOP.Valid {
		a.ClosedGOP = row.closedGOP.Bool
	}
	if row.firstFrameKey.Valid {
		a.FirstFrameKeyframe = row.firstFrameKey.Bool
	}
	return &a, nil
}

// loadArtifactMetrics rehydrates the durable numeric projection of Chronon's
// timing sidecar. Ordering and later-row-wins semantics are unchanged.
func loadArtifactMetrics(ctx context.Context, db artifactQuerier, jobID string, a *model.Artifact, id string) error {
	rows, err := db.QueryContext(ctx, `
		SELECT metric_name, metric_value
		FROM processing_metrics
		WHERE job_id = $1
		ORDER BY created_at ASC, id ASC`, jobID)
	if err != nil {
		return fmt.Errorf("load processing metrics for artifact %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var value float64
		if err := rows.Scan(&name, &value); err != nil {
			return fmt.Errorf("scan processing metric for artifact %s: %w", id, err)
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
		return fmt.Errorf("iterate processing metrics for artifact %s: %w", id, err)
	}
	return nil
}

// loadArtifactTelemetry rehydrates the bounded Chronon ledger document. A
// missing row is fail-open for legacy workers and must not fail the read.
func loadArtifactTelemetry(ctx context.Context, db artifactQuerier, jobID string, a *model.Artifact) {
	var telemetry []byte
	if err := db.QueryRowContext(ctx, `
		SELECT telemetry FROM render_telemetry
		WHERE job_id = $1 ORDER BY created_at DESC LIMIT 1`, jobID).Scan(&telemetry); err == nil && len(telemetry) > 0 {
		a.ChrononTelemetry = json.RawMessage(telemetry)
	}
}

// loadArtifactMetricsByJob is the batch counterpart of loadArtifactMetrics:
// one ordered read for every job in the claim window, with the same
// later-row-wins semantics per job.
func loadArtifactMetricsByJob(ctx context.Context, db artifactQuerier, jobIDs []string) (map[string]map[string]float64, error) {
	out := make(map[string]map[string]float64, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT job_id, metric_name, metric_value
		FROM processing_metrics
		WHERE job_id = ANY($1)
		ORDER BY job_id ASC, created_at ASC, id ASC`, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("load processing metrics for %d artifacts: %w", len(jobIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var jobID, name string
		var value float64
		if err := rows.Scan(&jobID, &name, &value); err != nil {
			return nil, fmt.Errorf("scan processing metric for job %s: %w", jobID, err)
		}
		if name == "" {
			continue
		}
		if out[jobID] == nil {
			out[jobID] = make(map[string]float64)
		}
		out[jobID][name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate processing metrics for %d artifacts: %w", len(jobIDs), err)
	}
	return out, nil
}

// loadArtifactTelemetryByJob is the batch counterpart of
// loadArtifactTelemetry. DISTINCT ON keeps the newest row per job in one read;
// an absent row stays absent (fail-open).
func loadArtifactTelemetryByJob(ctx context.Context, db artifactQuerier, jobIDs []string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (job_id) job_id, telemetry
		FROM render_telemetry
		WHERE job_id = ANY($1)
		ORDER BY job_id ASC, created_at DESC`, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("load render telemetry for %d artifacts: %w", len(jobIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var jobID string
		var telemetry []byte
		if err := rows.Scan(&jobID, &telemetry); err != nil {
			return nil, fmt.Errorf("scan render telemetry for job %s: %w", jobID, err)
		}
		if len(telemetry) > 0 {
			out[jobID] = json.RawMessage(telemetry)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate render telemetry for %d artifacts: %w", len(jobIDs), err)
	}
	return out, nil
}
