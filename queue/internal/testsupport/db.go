// Package testsupport holds the small amount of shared plumbing the queue's
// PostgreSQL test packages need. It is imported only from _test.go files.
package testsupport

import (
	"context"
	"database/sql"
	"testing"
)

// queueTestLockKey is an arbitrary application-scoped advisory-lock key.
// Changing it only needs to be done here: every package that shares the test
// database takes this same lock.
const queueTestLockKey int64 = 0x72676671 // "rgfq"

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
