package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/testsupport"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// retentionTestDB opens the PostgreSQL test database and applies the queue
// schema. A missing DSN is a skip, like every other queue integration test.
func retentionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping render_events retention test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	// `go test ./...` runs packages in parallel and every queue integration
	// test shares TEST_DATABASE_URL. Without the lock, another package's
	// TRUNCATE can delete this test's events mid-assertion.
	testsupport.SerializeDBTest(t, db)
	if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestPruneRenderEventsDeletesOnlyRowsOlderThanTheCutoff pins the retention
// contract: only rows strictly older than the cutoff are deleted, the recent
// ledger stays, and the cutoff is inclusive-safe for "now".
func TestPruneRenderEventsDeletesOnlyRowsOlderThanTheCutoff(t *testing.T) {
	db := retentionTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO render_jobs (id, job_type, state) VALUES ('retention-job', 'overlay', 'completed')`); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []string{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "2026-11-25T00:00:00Z"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO render_events (job_id, event_type, created_at) VALUES ('retention-job', 'JOB_CREATED', $1)`,
			ts); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := pruneRenderEvents(ctx, db, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want the two rows older than the cutoff", deleted)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM render_events WHERE job_id = 'retention-job'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining rows = %d, want the one recent event", remaining)
	}

	// A cutoff in the future prunes everything and is not an error.
	deleted, err = pruneRenderEvents(ctx, db, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("future cutoff deleted = %d, want the last row", deleted)
	}
}

// TestPruneRenderEventsBatchesLongLedgers proves the sweep drains more rows
// than one DELETE batch, so the first pass is not a silent ceiling.
func TestPruneRenderEventsBatchesLongLedgers(t *testing.T) {
	db := retentionTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO render_jobs (id, job_type, state) VALUES ('retention-batch', 'overlay', 'completed')`); err != nil {
		t.Fatal(err)
	}
	rows := renderEventsRetentionBatch + 25
	if _, err := db.ExecContext(ctx, `
		INSERT INTO render_events (job_id, event_type, created_at)
		SELECT 'retention-batch', 'JOB_CREATED', timestamptz '2026-01-01T00:00:00Z'
		FROM generate_series(1, $1)`, rows); err != nil {
		t.Fatal(err)
	}
	deleted, err := pruneRenderEvents(ctx, db, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != int64(rows) {
		t.Fatalf("deleted = %d, want all %d rows across batches", deleted, rows)
	}
}
