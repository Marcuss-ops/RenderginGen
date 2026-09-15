package artifactdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// legacyBaseSchema is the artifact_records table as the FIRST shipped worker
// created it: no chronon_telemetry and no chronon_timing_* columns, and a
// user_version of 0 (the pre-versioning code never recorded one).
const legacyBaseSchema = `
CREATE TABLE artifact_records (
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
  created_at          TEXT NOT NULL
);`

// rawExec runs statements against a ledger file without going through the
// recorder, so the test can build the exact legacy shapes.
func rawExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw %s: %v", path, err)
	}
	defer db.Close()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", firstLine(stmt), err)
		}
	}
}

func firstLine(stmt string) string {
	if idx := strings.IndexByte(stmt, '\n'); idx >= 0 {
		return strings.TrimSpace(stmt[:idx])
	}
	return strings.TrimSpace(stmt)
}

// ledgerColumnNames returns the sorted column names of artifact_records.
func ledgerColumnNames(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA table_info(artifact_records)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	var names []string
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
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ledgerVersion reads SQLite's schema stamp.
func ledgerVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return version
}

// TestFreshLedgerIsAtCurrentVersion pins that a new ledger records the schema
// version, so the next worker can tell an up-to-date file from a legacy one.
func TestFreshLedgerIsAtCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close()
	if got, want := ledgerVersion(t, path), len(migrations); got != want {
		t.Fatalf("fresh ledger user_version = %d, want %d", got, want)
	}
}

// TestLegacyLedgersReachTheSameColumns is the migration contract: whatever
// subset of the column-adds an unversioned worker managed to apply (all of them,
// none of them, or a partial run after a crash), opening the ledger produces the
// SAME schema as a fresh one, preserves the rows, and stamps the version.
func TestLegacyLedgersReachTheSameColumns(t *testing.T) {
	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := NewSQLite(freshPath)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	fresh.Close()
	wantColumns := ledgerColumnNames(t, freshPath)

	// The column-adds the pre-versioning code ran, in its own order.
	var columnAdds []string
	for _, step := range migrations[1:] {
		columnAdds = append(columnAdds, step.ddl)
	}

	shapes := map[string][]string{
		"no columns yet": nil,
		"all columns":    columnAdds,
		"partial run":    columnAdds[:3],
	}
	for name, adds := range shapes {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			rawExec(t, path, append([]string{legacyBaseSchema}, adds...)...)
			// The legacy worker never stamped a version; that is what makes the
			// column check (rather than the version) load-bearing.
			if got := ledgerVersion(t, path); got != 0 {
				t.Fatalf("legacy ledger user_version = %d, want 0", got)
			}
			// A row written before the migration must survive it.
			rawExec(t, path,
				`INSERT INTO artifact_records (job_id, artifact_hash, storage_key, size_bytes, created_at) VALUES ('legacy-1','hash','key',7,'2026-01-01T00:00:00Z')`)

			rec, err := NewSQLite(path)
			if err != nil {
				t.Fatalf("open legacy ledger: %v", err)
			}
			defer rec.Close()

			if got, want := ledgerVersion(t, path), len(migrations); got != want {
				t.Fatalf("migrated user_version = %d, want %d", got, want)
			}
			if got := ledgerColumnNames(t, path); len(got) != len(wantColumns) {
				t.Fatalf("column count = %d, want %d (%v)", len(got), len(wantColumns), got)
			} else {
				for i := range got {
					if got[i] != wantColumns[i] {
						t.Fatalf("columns = %v, want %v", got, wantColumns)
					}
				}
			}
			// The pre-existing row is still there and the migrated ledger
			// accepts a full record (the upsert names every column, so a
			// missing column would fail the arity guard).
			if err := rec.Record(context.Background(), sampleRecord("legacy-1")); err != nil {
				t.Fatalf("record on migrated ledger: %v", err)
			}
		})
	}
}

// TestLedgerFromNewerWorkerIsRefused pins the fail-closed half of versioning: a
// ledger stamped by a newer worker must not be opened by an older one, which
// would write rows the newer worker misreads.
func TestLedgerFromNewerWorkerIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	rawExec(t, path, legacyBaseSchema, versionStamp(len(migrations)+1))

	if _, err := NewSQLite(path); err == nil {
		t.Fatal("a ledger from a newer worker must be refused, not silently opened")
	}
}
