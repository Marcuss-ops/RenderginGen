package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// setupRepo opens a PostgreSQL connection, applies the schema and truncates
// the queue tables for a deterministic test. It skips unless TEST_DATABASE_URL
// is set (e.g. in unit CI without a service container). It returns both the
// repository and the *sql.DB so tests can inspect attempts/events.
func setupRepo(t *testing.T, lease time.Duration, maxAttempts int) (*Repository, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping postgres integration test")
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
	if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
		t.Fatal(err)
	}
	return New(db, lease, maxAttempts), db
}

// newRepo is a convenience wrapper for tests that only need the repository.
func newRepo(t *testing.T, lease time.Duration, maxAttempts int) *Repository {
	r, _ := setupRepo(t, lease, maxAttempts)
	return r
}

func TestSubmitClaimComplete(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)

	job := model.Job{
		ID:          "job-1",
		OverlaySpec: []byte(`{"n":1}`),
		Assets:      []model.AssetRef{{Hash: "abc", URL: "s3://a"}},
	}
	if err := r.Submit(job); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := r.Submit(job); err == nil {
		t.Fatal("duplicate submit should fail")
	}

	claimed, lease, err := r.Claim("w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.ID != "job-1" {
		t.Fatalf("want job-1, got %+v", claimed)
	}
	if lease != 30*time.Second {
		t.Fatalf("want 30s lease, got %s", lease)
	}
	if len(claimed.Assets) != 1 || claimed.Assets[0].Hash != "abc" {
		t.Fatalf("assets not round-tripped: %+v", claimed.Assets)
	}

	if err := r.Complete("job-1", "w1", model.Artifact{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if s := r.Stats(); s.Completed != 1 {
		t.Fatalf("want 1 completed, got %+v", s)
	}
}

func TestClaimFIFOAndExclusive(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Submit(model.Job{ID: "job-2"}); err != nil {
		t.Fatal(err)
	}

	first, _, err := r.Claim("w1")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.ID != "job-1" {
		t.Fatalf("want job-1, got %+v", first)
	}
	second, _, err := r.Claim("w2")
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.ID != "job-2" {
		t.Fatalf("want job-2, got %+v", second)
	}
	if got, _, _ := r.Claim("w3"); got != nil {
		t.Fatalf("queue should be empty, got %+v", got)
	}
}

func TestFailRequeuesUntilMaxAttempts(t *testing.T) {
	r := newRepo(t, 30*time.Second, 2)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	// Attempt 1 -> requeue.
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Fail("job-1", "w1", "boom"); err != nil {
		t.Fatal(err)
	}

	// Attempt 2 -> permanent fail.
	if _, _, err := r.Claim("w2"); err != nil {
		t.Fatal(err)
	}
	if err := r.Fail("job-1", "w2", "boom"); err != nil {
		t.Fatal(err)
	}

	s := r.Stats()
	if s.Failed != 1 || s.Pending != 0 {
		t.Fatalf("want 1 failed, 0 pending; got %+v", s)
	}
}

func TestFailWrongWorkerFails(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("job-1", "w2", model.Artifact{}); err == nil {
		t.Fatal("expected error completing with wrong worker")
	}
}

func TestLeaseExpiryRequeues(t *testing.T) {
	r := newRepo(t, 10*time.Millisecond, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)
	n, err := r.RequeueExpired(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 requeued, got %d", n)
	}

	again, _, err := r.Claim("w2")
	if err != nil {
		t.Fatal(err)
	}
	if again == nil || again.ID != "job-1" {
		t.Fatalf("job should be claimable again, got %+v", again)
	}
	if again.Attempts != 2 {
		t.Fatalf("want attempts=2, got %d", again.Attempts)
	}
}

func TestRenewExtendsLease(t *testing.T) {
	r := newRepo(t, 100*time.Millisecond, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Millisecond) // near the end of the original lease
	if err := r.Renew("job-1", "w1"); err != nil {
		t.Fatalf("renew: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // past original lease, within renewed lease

	n, err := r.RequeueExpired(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("job should still be running after renew, got %d requeued", n)
	}
}

func TestClaimConcurrentExclusive(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)

	const jobs = 50
	for i := 0; i < jobs; i++ {
		if err := r.Submit(model.Job{ID: fmt.Sprintf("job-%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				job, _, err := r.Claim(worker)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				if seen[job.ID] {
					t.Errorf("job %s claimed more than once", job.ID)
				}
				seen[job.ID] = true
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("want %d distinct claims, got %d", jobs, len(seen))
	}
	if s := r.Stats(); s.Running != jobs {
		t.Fatalf("want %d running, got %d", jobs, s.Running)
	}
}
