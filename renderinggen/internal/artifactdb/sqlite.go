package artifactdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteRecorder persists ArtifactRecords in a local SQLite ledger. It uses
// the pure-Go modernc.org/sqlite driver so the worker keeps building with
// CGO_ENABLED=0 (see infra/docker/renderinggen-worker.Dockerfile). The schema
// mirrors the ArtifactRecord SSOT; records are upserted by job ID so a
// publication retry never duplicates a render's ledger entry.
type SQLiteRecorder struct {
	db *sql.DB
}

// NewSQLite opens (or creates) the artifact ledger at path.
func NewSQLite(path string) (*SQLiteRecorder, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("artifactdb: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("artifactdb: create schema: %w", err)
	}
	return &SQLiteRecorder{db: db}, nil
}

// Close releases the ledger.
func (s *SQLiteRecorder) Close() error { return s.db.Close() }

// Record upserts one artifact row, keyed by job ID.
func (s *SQLiteRecorder) Record(ctx context.Context, rec ArtifactRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, upsert,
		rec.JobID,
		rec.ArtifactHash, rec.StorageKey, rec.SizeBytes, rec.ContentType,
		rec.Backend, rec.ChrononVersion, rec.ProfileID,
		rec.Container, rec.Codec, rec.CodecProfile, rec.PixelFormat,
		rec.Width, rec.Height, rec.FPSNum, rec.FPSDen,
		rec.FrameCount, rec.DurationUS, rec.AudioStreams, rec.FirstFrameKeyframe,
		rec.EntityCount, rec.ImportantPhraseCnt, rec.ImportantWordCnt,
		rec.ImageCount, rec.LightLeakCount, rec.PresetID,
		rec.OverlayCompileUS, rec.AssetMaterializeUS, rec.ChrononRenderUS,
		rec.EncodeUS, rec.SHA256US, rec.ObjectStoreUploadUS, rec.DriveUploadUS,
		rec.TotalUS, rec.InputBytes, rec.OutputBytes,
		rec.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("artifactdb: record %s: %w", rec.JobID, err)
	}
	return nil
}

// UpdateDrive rewrites only the drive_upload_us column of an existing
// record. Publication retries never touch the artifact identity columns.
func (s *SQLiteRecorder) UpdateDrive(ctx context.Context, jobID string, driveUploadUS int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE artifact_records SET drive_upload_us=? WHERE job_id=?`, driveUploadUS, jobID); err != nil {
		return fmt.Errorf("artifactdb: update drive %s: %w", jobID, err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS artifact_records (
  job_id              TEXT PRIMARY KEY,
  artifact_hash       TEXT NOT NULL,
  storage_key         TEXT NOT NULL,
  size_bytes          INTEGER NOT NULL,
  content_type        TEXT NOT NULL DEFAULT '',
  backend             TEXT NOT NULL DEFAULT '',
  chronon_version     TEXT NOT NULL DEFAULT '',
  profile_id          TEXT NOT NULL DEFAULT '',
  container           TEXT NOT NULL DEFAULT '',
  codec               TEXT NOT NULL DEFAULT '',
  codec_profile       TEXT NOT NULL DEFAULT '',
  pixel_format        TEXT NOT NULL DEFAULT '',
  width               INTEGER NOT NULL DEFAULT 0,
  height              INTEGER NOT NULL DEFAULT 0,
  fps_num             INTEGER NOT NULL DEFAULT 0,
  fps_den             INTEGER NOT NULL DEFAULT 0,
  frame_count         INTEGER NOT NULL DEFAULT 0,
  duration_us         INTEGER NOT NULL DEFAULT 0,
  audio_streams       INTEGER NOT NULL DEFAULT 0,
  first_frame_keyframe INTEGER NOT NULL DEFAULT 0,
  entity_count        INTEGER NOT NULL DEFAULT 0,
  important_phrase_count INTEGER NOT NULL DEFAULT 0,
  important_word_count INTEGER NOT NULL DEFAULT 0,
  image_count         INTEGER NOT NULL DEFAULT 0,
  light_leak_count    INTEGER NOT NULL DEFAULT 0,
  preset_id           TEXT NOT NULL DEFAULT '',
  overlay_compile_us  INTEGER NOT NULL DEFAULT 0,
  asset_materialize_us INTEGER NOT NULL DEFAULT 0,
  chronon_render_us   INTEGER NOT NULL DEFAULT 0,
  encode_us           INTEGER NOT NULL DEFAULT 0,
  sha256_us           INTEGER NOT NULL DEFAULT 0,
  objectstore_upload_us INTEGER NOT NULL DEFAULT 0,
  drive_upload_us     INTEGER NOT NULL DEFAULT 0,
  total_us            INTEGER NOT NULL DEFAULT 0,
  input_bytes         INTEGER NOT NULL DEFAULT 0,
  output_bytes        INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL
);`

const upsert = `
INSERT INTO artifact_records (
  job_id, artifact_hash, storage_key, size_bytes, content_type,
  backend, chronon_version, profile_id,
  container, codec, codec_profile, pixel_format,
  width, height, fps_num, fps_den,
  frame_count, duration_us, audio_streams, first_frame_keyframe,
  entity_count, important_phrase_count, important_word_count,
  image_count, light_leak_count, preset_id,
  overlay_compile_us, asset_materialize_us, chronon_render_us,
  encode_us, sha256_us, objectstore_upload_us, drive_upload_us,
  total_us, input_bytes, output_bytes, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  artifact_hash=excluded.artifact_hash,
  storage_key=excluded.storage_key,
  size_bytes=excluded.size_bytes,
  content_type=excluded.content_type,
  backend=excluded.backend,
  chronon_version=excluded.chronon_version,
  profile_id=excluded.profile_id,
  container=excluded.container,
  codec=excluded.codec,
  codec_profile=excluded.codec_profile,
  pixel_format=excluded.pixel_format,
  width=excluded.width,
  height=excluded.height,
  fps_num=excluded.fps_num,
  fps_den=excluded.fps_den,
  frame_count=excluded.frame_count,
  duration_us=excluded.duration_us,
  audio_streams=excluded.audio_streams,
  first_frame_keyframe=excluded.first_frame_keyframe,
  entity_count=excluded.entity_count,
  important_phrase_count=excluded.important_phrase_count,
  important_word_count=excluded.important_word_count,
  image_count=excluded.image_count,
  light_leak_count=excluded.light_leak_count,
  preset_id=excluded.preset_id,
  overlay_compile_us=excluded.overlay_compile_us,
  asset_materialize_us=excluded.asset_materialize_us,
  chronon_render_us=excluded.chronon_render_us,
  encode_us=excluded.encode_us,
  sha256_us=excluded.sha256_us,
  objectstore_upload_us=excluded.objectstore_upload_us,
  drive_upload_us=excluded.drive_upload_us,
  total_us=excluded.total_us,
  input_bytes=excluded.input_bytes,
  output_bytes=excluded.output_bytes,
  created_at=excluded.created_at;`

