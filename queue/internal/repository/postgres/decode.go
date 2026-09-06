// decode.go owns the JSONB decode helpers shared by the read paths: input
// normalization, schema-version mapping and corrupted-JSONB surfacing.
package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
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

// nullIfEmpty converts an empty string to SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
