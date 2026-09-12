// decode.go owns the JSONB decode helpers shared by the read paths: input
// normalization, schema-version mapping and corrupted-JSONB surfacing.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

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

// decodeFrameRange extracts the frame range from a frame_range JSONB.
// Corrupt JSONB is fail-closed: the caller must surface the error so the job
// is poisoned rather than silently rendered as a full clip.
func decodeFrameRange(raw []byte) (*model.FrameRange, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var result model.FrameRange
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("postgres: corrupt frame_range jsonb (%d bytes): %w", len(raw), err)
	}
	return &result, nil
}

func decodeAssets(raw []byte) ([]model.AssetRef, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m inputManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("postgres: corrupt input_manifest jsonb (%d bytes): %w", len(raw), err)
	}
	return m.Assets, nil
}

// poisonCorruptJob marks a job failed due to corrupt JSONB so it never
// renders as full-plan and never drops SHA256 verification. It is called
// inside the Claim/Get transaction while the row is still locked.
func poisonCorruptJob(ctx context.Context, tx *sql.Tx, jobID string, reason string) {
	_, _ = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateFailed)+`, failed_at = now(), error_message = $2,
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1`, jobID, reason)
	// Best-effort attempt/event — poison must not fail because attempt bookkeeping failed.
	if attempt, err := runningAttemptID(ctx, tx, jobID); err == nil && attempt != "" {
		_ = finishAttempt(ctx, tx, attempt, attemptStatusFailed, "", reason)
		_ = recordEvent(ctx, tx, eventJobFailed, jobID, attempt, "", map[string]any{"reason": reason})
	} else {
		_ = recordEvent(ctx, tx, eventJobFailed, jobID, "", "", map[string]any{"reason": reason})
	}
}

// nullIfEmpty converts an empty string to SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
