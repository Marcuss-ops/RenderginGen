package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/testsupport"
)

// TestExportReferencedKeysIncludesArtifactAndTimingSidecar runs against the
// real PostgreSQL schema: the artifact address and its raw-timing sidecar are
// two separate ledger columns, and a GC that protected only the clip would
// delete the sidecar it still points at.
func TestExportReferencedKeysIncludesArtifactAndTimingSidecar(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("RENDERINGGEN_REQUIRE_POSTGRES_TESTS") == "1" {
			t.Fatal("TEST_DATABASE_URL is unset while RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping artifact key export test")
	}
	testsupport.RequireDedicatedTestDatabase(t, dsn)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := migrate.Apply(ctx, db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	testsupport.SerializeDBTest(t, db)
	if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
		t.Fatal(err)
	}

	clip := strings.Repeat("a", 64)
	sidecar := strings.Repeat("b", 64)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO render_jobs (id, job_type, state) VALUES ('job-export', 'render_segment', 'completed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO render_artifacts (id, job_id, kind, storage_key, chronon_timing_storage_key)
		VALUES ('art-export', 'job-export', 'overlay', $1, $2)`, clip, sidecar); err != nil {
		t.Fatal(err)
	}
	// A legacy artifact naming scheme cannot address an object-store file; the
	// export warns and skips it instead of emitting an invalid reference line.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO render_artifacts (id, job_id, kind, storage_key)
		VALUES ('art-legacy', 'job-export', 'overlay', 'overlay/job-export/out.mp4')`); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	stats, err := exportReferencedKeys(ctx, db, &out)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stats.Referenced != 2 || stats.SkippedNonCanonical != 1 {
		t.Fatalf("stats = %+v, want 2 referenced / 1 skipped", stats)
	}

	got := map[string]bool{}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		got[line] = true
	}
	if len(got) != 2 || !got[clip] || !got[sidecar] {
		t.Fatalf("exported keys = %v, want clip %s and sidecar %s", got, clip, sidecar)
	}
}
