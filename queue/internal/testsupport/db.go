// Package testsupport holds the small amount of shared plumbing the queue's
// PostgreSQL test packages need. It is imported only from _test.go files.
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// queueTestLockKey is an arbitrary application-scoped advisory-lock key.
// Changing it only needs to be done here: every package that shares the test
// database takes this same lock.
const queueTestLockKey int64 = 0x72676671 // "rgfq"

// DedicatedTestDatabaseName parses a PostgreSQL DSN and accepts only an
// explicitly test-named database. Queue integration tests TRUNCATE shared
// tables, so checking only TEST_DATABASE_URL is not sufficient: a copy/paste
// mistake once pointed the test container at a live worker database.
func DedicatedTestDatabaseName(dsn string) (string, error) {
	config, err := pgx.ParseConfig(strings.TrimSpace(dsn))
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(config.Database))
	if name == "" || !(strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test")) {
		return name, fmt.Errorf("refusing PostgreSQL test database %q: its name must start with test_ or end with _test", name)
	}
	return name, nil
}

// RequireDedicatedTestDatabase is the fail-closed guard called before a test
// opens a connection or runs migrations/truncates tables.
func RequireDedicatedTestDatabase(tb testing.TB, dsn string) {
	tb.Helper()
	if _, err := DedicatedTestDatabaseName(dsn); err != nil {
		tb.Fatal(err)
	}
}

// SerializeDBTest takes an exclusive session-scoped PostgreSQL advisory lock
// for the duration of the test.
//
// `go test ./...` runs packages in parallel, and every queue integration test
// package shares TEST_DATABASE_URL and TRUNCATEs the same tables. Without this
// lock two packages erase each other's fixtures mid-test, which showed up as
// unrelated failures such as a retention batch deleting another package's
// rows. The lock lives on a dedicated connection so the test's query pool is
// not constrained by it, and it is released by the test cleanup.
func SerializeDBTest(tb testing.TB, db *sql.DB) {
	tb.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		tb.Fatalf("acquire test-database connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, queueTestLockKey); err != nil {
		_ = conn.Close()
		tb.Fatalf("acquire test-database lock: %v", err)
	}
	tb.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, queueTestLockKey)
		_ = conn.Close()
	})
}
