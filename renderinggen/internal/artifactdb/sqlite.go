package artifactdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteRecorder persists ArtifactRecords in a local diagnostic mirror. It uses
// the pure-Go modernc.org/sqlite driver so the worker keeps building with
// CGO_ENABLED=0 (see infra/docker/renderinggen-worker.Dockerfile). The schema
// mirrors central artifact metadata; records are upserted by job ID so a
// publication retry never duplicates a render's ledger entry.
type SQLiteRecorder struct {
	db *sql.DB
}

// NewSQLite opens (or creates) the artifact ledger at path.
//
// Production concurrency notes: the worker's post pool records artifacts from
// several goroutines at once. The ledger is therefore opened with WAL
// journaling and a busy_timeout so concurrent writers wait instead of failing
// with "database is locked" (modernc.org/sqlite defaults to busy_timeout=0,
// which fails immediately), and the pool is capped at one connection so all
// writes serialize on a single session.
func NewSQLite(path string) (*SQLiteRecorder, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("artifactdb: open %s: %w", path, err)
	}
	// One connection serializes every statement; busy_timeout then covers the
	// (rare) contention against an external reader of the same file.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteRecorder{db: db}, nil
}

// migration is one step of the ledger's schema history. The step at index i
// produces schema version i+1.
type migration struct {
	// column is the column this step adds, or "" when the step only establishes
	// the base table.
	//
	// It exists because the pre-versioning worker applied the column-adds in a
	// loop that SWALLOWED the "duplicate column" error, and recorded no version
	// at all. Such a ledger can therefore be at any subset of these steps while
	// reporting user_version = 0 — including a partial set, if the old loop was
	// interrupted. Asking the catalog whether the column is already there makes
	// the history idempotent for exactly that population, and repair-capable
	// for a partially applied one, instead of depending on the driver's error
	// text (which is what the old loop matched on, and which is not a contract).
	column string
	ddl    string
}

// migrations is the ordered schema history. Each entry's ddl must be valid to
// run exactly once; idempotence for legacy ledgers comes from the column
// check, not from the SQL.
var migrations = []migration{
	{ddl: schema},
	{column: "chronon_telemetry", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_telemetry TEXT NOT NULL DEFAULT ''`},
	{column: "chronon_timing_storage_key", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_timing_storage_key TEXT NOT NULL DEFAULT ''`},
	{column: "chronon_timing_url", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_timing_url TEXT NOT NULL DEFAULT ''`},
	{column: "chronon_timing_sha256", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_timing_sha256 TEXT NOT NULL DEFAULT ''`},
	{column: "chronon_timing_size_bytes", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_timing_size_bytes INTEGER NOT NULL DEFAULT 0`},
	{column: "chronon_timing_content_type", ddl: `ALTER TABLE artifact_records ADD COLUMN chronon_timing_content_type TEXT NOT NULL DEFAULT ''`},
}

// migrate brings the ledger to the current schema version, recording each step
// in SQLite's own user_version. A step and its version stamp commit together,
// so a crash mid-migration leaves the ledger at a step boundary and the next
// open resumes from there.
func migrate(db *sql.DB) error {
	version, err := schemaVersion(db)
	if err != nil {
		return err
	}
	if version > len(migrations) {
		// Refuse rather than run an unknown history against a newer ledger: the
		// alternative is a downgraded worker silently writing a row shape the
		// newer worker will misread.
		return fmt.Errorf("artifactdb: ledger schema version %d is newer than this worker's %d; refusing to open it", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		step := migrations[i]
		if step.column != "" {
			present, err := hasColumn(db, "artifact_records", step.column)
			if err != nil {
				return err
			}
			if present {
				// Already applied by an unversioned worker: record the version and
				// move on, instead of failing on a duplicate column.
				if err := setSchemaVersion(db, i+1); err != nil {
					return err
				}
				continue
			}
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("artifactdb: begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(step.ddl); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("artifactdb: apply migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(versionStamp(i + 1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("artifactdb: stamp migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("artifactdb: commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// schemaVersion reads SQLite's own schema stamp for this database.
func schemaVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("artifactdb: read schema version: %w", err)
	}
	return version, nil
}

// setSchemaVersion records a version without running a step, used for the
// legacy ledgers whose columns an unversioned worker already added.
func setSchemaVersion(db *sql.DB, version int) error {
	if _, err := db.Exec(versionStamp(version)); err != nil {
		return fmt.Errorf("artifactdb: stamp schema version %d: %w", version, err)
	}
	return nil
}

// versionStamp renders the PRAGMA that sets user_version. PRAGMA statements
// cannot take a bound parameter, so the value is formatted from an int this
// package controls (never from input).
func versionStamp(version int) string {
	return fmt.Sprintf("PRAGMA user_version = %d", version)
}

// hasColumn reports whether table already has the named column.
//
// table and column are compile-time constants from this file: PRAGMA
// table_info cannot take a bound parameter, so the table name is interpolated.
func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("artifactdb: read columns of %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultV   sql.NullString
			primaryK   int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultV, &primaryK); err != nil {
			return false, fmt.Errorf("artifactdb: scan columns of %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close releases the ledger.
func (s *SQLiteRecorder) Close() error { return s.db.Close() }

// Record upserts one artifact row, keyed by job ID. Concurrent calls are
// serialized by the single connection opened in NewSQLite.
func (s *SQLiteRecorder) Record(ctx context.Context, rec ArtifactRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	// The column list, the placeholder list and the argument list are three
	// hand-maintained parallel sequences. The driver would report a count
	// mismatch as a generic bind error, but it cannot tell which of the three
	// drifted (a swapped pair binds the wrong value silently). This guard turns
	// that class of edit into a named, immediate error.
	args := []any{
		rec.JobID,
		rec.ArtifactHash, rec.StorageKey, rec.SizeBytes, rec.ContentType,
		rec.Backend, rec.ChrononVersion, rec.ProfileID,
		rec.Container, rec.Codec, rec.CodecProfile, rec.PixelFormat,
		rec.Width, rec.Height, rec.FPSNum, rec.FPSDen,
		rec.FrameCount, rec.DurationUS, rec.AudioStreams, rec.FirstFrameKeyframe,
		rec.EntityCount, rec.ImportantPhraseCnt, rec.ImportantWordCnt,
		rec.ImageCount, rec.LightLeakCount, rec.PresetID,
		rec.OverlayCompileUS, rec.AssetMaterializeUS, rec.ChrononRenderUS,
		rec.SHA256US, rec.ObjectStoreUploadUS, rec.DriveUploadUS,
		rec.TotalUS, rec.InputBytes, rec.OutputBytes,
		string(rec.ChrononTelemetry),
		rec.ChrononTimingStorageKey, rec.ChrononTimingURL, rec.ChrononTimingSHA256,
		rec.ChrononTimingSizeBytes, rec.ChrononTimingContentType,
		rec.CreatedAt.Format(time.RFC3339Nano),
	}
	if want := strings.Count(upsert, "?"); len(args) != want {
		return fmt.Errorf("artifactdb: record %s: upsert arity drift: %d args, %d placeholders", rec.JobID, len(args), want)
	}
	_, err := s.db.ExecContext(ctx, upsert, args...)
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
  sha256_us           INTEGER NOT NULL DEFAULT 0,
  objectstore_upload_us INTEGER NOT NULL DEFAULT 0,
  drive_upload_us     INTEGER NOT NULL DEFAULT 0,
  total_us            INTEGER NOT NULL DEFAULT 0,
  input_bytes         INTEGER NOT NULL DEFAULT 0,
  output_bytes        INTEGER NOT NULL DEFAULT 0,
  chronon_telemetry   TEXT NOT NULL DEFAULT '',
  chronon_timing_storage_key TEXT NOT NULL DEFAULT '',
  chronon_timing_url         TEXT NOT NULL DEFAULT '',
  chronon_timing_sha256      TEXT NOT NULL DEFAULT '',
  chronon_timing_size_bytes  INTEGER NOT NULL DEFAULT 0,
  chronon_timing_content_type TEXT NOT NULL DEFAULT '',
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
  sha256_us, objectstore_upload_us, drive_upload_us,
  total_us, input_bytes, output_bytes, chronon_telemetry,
  chronon_timing_storage_key, chronon_timing_url, chronon_timing_sha256,
  chronon_timing_size_bytes, chronon_timing_content_type, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
  sha256_us=excluded.sha256_us,
  objectstore_upload_us=excluded.objectstore_upload_us,
  drive_upload_us=excluded.drive_upload_us,
  total_us=excluded.total_us,
  input_bytes=excluded.input_bytes,
  output_bytes=excluded.output_bytes,
  chronon_telemetry=excluded.chronon_telemetry,
  chronon_timing_storage_key=excluded.chronon_timing_storage_key,
  chronon_timing_url=excluded.chronon_timing_url,
  chronon_timing_sha256=excluded.chronon_timing_sha256,
  chronon_timing_size_bytes=excluded.chronon_timing_size_bytes,
  chronon_timing_content_type=excluded.chronon_timing_content_type,
  created_at=excluded.created_at;`
