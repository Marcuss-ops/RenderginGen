package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// openClaimBenchDB prepares an isolated schema for the benchmark. It mirrors
// setupRepoB: same skip/require guard, same fresh tables.
func openClaimBenchDB(b *testing.B) *sql.DB {
	b.Helper()
	db, err := sql.Open("pgx", testDatabaseURL(b))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		b.Fatal(err)
	}
	if err := migrate.Apply(ctx, db); err != nil {
		b.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
		b.Fatal(err)
	}
	return db
}

// countingQuerier counts the statements a hydration issues without changing
// their behavior. The claim path's N+1 regression was invisible to
// result-based tests, so the count itself is the contract.
type countingQuerier struct {
	inner   artifactQuerier
	queries int
}

func (c *countingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.queries++
	return c.inner.QueryContext(ctx, query, args...)
}

func (c *countingQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.queries++
	return c.inner.QueryRowContext(ctx, query, args...)
}

// TestAttachClaimArtifactsUsesConstantStatements pins the batch read: artifact
// rows, metrics and telemetry are three statements for a whole claim window,
// independent of how many jobs it contains. A reintroduced per-job read makes
// the 16- and 64-job cases cost 3n and fails here.
func TestAttachClaimArtifactsUsesConstantStatements(t *testing.T) {
	_, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	for _, n := range []int{1, 16, 64} {
		t.Run(fmt.Sprintf("jobs=%d", n), func(t *testing.T) {
			claimed := make([]*model.Job, 0, n)
			candidates := make([]claimCandidate, 0, n)
			for i := 0; i < n; i++ {
				jobID := fmt.Sprintf("stmt-%d-%d", n, i)
				artifactID := "artifact-" + jobID
				if _, err := db.ExecContext(ctx,
					`INSERT INTO render_jobs (id, job_type, state) VALUES ($1, 'render_segment', 'rendered')`,
					jobID); err != nil {
					t.Fatalf("insert job: %v", err)
				}
				if _, err := db.ExecContext(ctx,
					`INSERT INTO render_artifacts (id, job_id, kind, storage_key) VALUES ($1, $2, 'overlay', $3)`,
					artifactID, jobID, "sha-"+jobID); err != nil {
					t.Fatalf("insert artifact: %v", err)
				}
				claimed = append(claimed, &model.Job{ID: jobID})
				var c claimCandidate
				c.id = jobID
				c.artifactID = sql.NullString{String: artifactID, Valid: true}
				candidates = append(candidates, c)
			}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			counter := &countingQuerier{inner: tx}
			if err := attachClaimArtifacts(ctx, counter, claimed, candidates); err != nil {
				t.Fatalf("hydrate: %v", err)
			}
			if counter.queries != 3 {
				t.Fatalf("hydration used %d statements for %d jobs, want 3 (artifact, metrics, telemetry)", counter.queries, n)
			}
			for _, job := range claimed {
				if job.Artifact == nil {
					t.Fatalf("job %s was hydrated without an artifact", job.ID)
				}
			}
		})
	}
}

// TestClaimBatchRollsBackWhenOneArtifactIsCorrupt is the batch counterpart of
// TestClaimRollsBackWhenArtifactHydrationFails: a failure on ONE artifact must
// roll back the WHOLE window, leaving no job leased and no attempt booked.
func TestClaimBatchRollsBackWhenOneArtifactIsCorrupt(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	const jobs = 3
	for i := 0; i < jobs; i++ {
		jobID := fmt.Sprintf("batch-rollback-%d", i)
		if err := r.Submit(ctx, model.Job{ID: jobID}); err != nil {
			t.Fatalf("Submit(%s): %v", jobID, err)
		}
		claimed, _, err := r.ClaimState(ctx, "renderer", model.StatePending)
		if err != nil || claimed == nil {
			t.Fatalf("ClaimState(%s) = %+v, %v", jobID, claimed, err)
		}
		artifact := model.Artifact{
			ID:           "artifact-" + jobID,
			Kind:         "overlay",
			StorageKey:   "sha-" + jobID,
			ArtifactHash: "sha-" + jobID,
		}
		if err := r.Rendered(ctx, claimed.ID, "renderer", claimed.LeaseGeneration, artifact, "deferred"); err != nil {
			t.Fatalf("Rendered(%s): %v", jobID, err)
		}
	}

	// Corrupt the SECOND job's artifact: the first row hydrates fine, which is
	// exactly the case a per-row commit would have left half-applied.
	if _, err := db.ExecContext(ctx,
		`UPDATE render_artifacts SET provenance = '"corrupt"'::jsonb WHERE id = 'artifact-batch-rollback-1'`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.ClaimBatch(ctx, "publisher", jobs); err == nil {
		t.Fatal("a corrupt artifact must fail the whole batch claim")
	}

	for i := 0; i < jobs; i++ {
		jobID := fmt.Sprintf("batch-rollback-%d", i)
		var state string
		var worker sql.NullString
		var attempts int
		if err := db.QueryRowContext(ctx,
			`SELECT state, current_worker_id FROM render_jobs WHERE id = $1`, jobID).Scan(&state, &worker); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM render_attempts WHERE job_id = $1`, jobID).Scan(&attempts); err != nil {
			t.Fatal(err)
		}
		if state != string(model.StateRendered) {
			t.Errorf("%s state = %q, want %q (batch was not rolled back)", jobID, state, model.StateRendered)
		}
		if worker.Valid {
			t.Errorf("%s current_worker_id = %q, want empty", jobID, worker.String)
		}
		if attempts != 1 {
			t.Errorf("%s has %d attempts, want only the original 1", jobID, attempts)
		}
	}
}

// TestClaimStateHonoursCancelledContext proves a cancelled caller cannot leave
// a leased job behind: the transaction is cancelled/rolled back and the error
// is propagated.
func TestClaimStateHonoursCancelledContext(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	if err := r.Submit(ctx, model.Job{ID: "cancel-claim"}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := r.Claim(ctx, "renderer")
	if err != nil || claimed == nil {
		t.Fatalf("initial claim = %+v, %v", claimed, err)
	}
	if err := r.Rendered(ctx, claimed.ID, "renderer", claimed.LeaseGeneration, model.Artifact{
		ID: "artifact-cancel-claim", Kind: "overlay", StorageKey: "sha-cancel", ArtifactHash: "sha-cancel",
	}, "deferred"); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.ClaimState(cancelled, "publisher", model.StateRendered); err == nil {
		t.Fatal("a cancelled context must not claim a job")
	}

	var state string
	var worker sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT state, current_worker_id FROM render_jobs WHERE id = 'cancel-claim'`).Scan(&state, &worker); err != nil {
		t.Fatal(err)
	}
	if state != string(model.StateRendered) || worker.Valid {
		t.Fatalf("cancelled claim changed the row: state=%q worker=%q", state, worker.String)
	}
}

// BenchmarkClaimBatchHydration measures the statement-bound hydration for the
// three window sizes the ticket names. It is a current baseline, not a
// before/after: the per-job path no longer exists in this tree.
func BenchmarkClaimBatchHydration(b *testing.B) {
	for _, n := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("jobs=%d", n), func(b *testing.B) {
			db := openClaimBenchDB(b)
			ctx := context.Background()
			claimed := make([]*model.Job, 0, n)
			candidates := make([]claimCandidate, 0, n)
			for i := 0; i < n; i++ {
				jobID := fmt.Sprintf("bench-%d-%d", n, i)
				artifactID := "artifact-" + jobID
				if _, err := db.ExecContext(ctx,
					`INSERT INTO render_jobs (id, job_type, state) VALUES ($1, 'render_segment', 'rendered')`, jobID); err != nil {
					b.Fatal(err)
				}
				if _, err := db.ExecContext(ctx,
					`INSERT INTO render_artifacts (id, job_id, kind, storage_key) VALUES ($1, $2, 'overlay', $3)`,
					artifactID, jobID, "sha-"+jobID); err != nil {
					b.Fatal(err)
				}
				claimed = append(claimed, &model.Job{ID: jobID})
				var c claimCandidate
				c.id = jobID
				c.artifactID = sql.NullString{String: artifactID, Valid: true}
				candidates = append(candidates, c)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, job := range claimed {
					job.Artifact = nil
				}
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				if err := attachClaimArtifacts(ctx, tx, claimed, candidates); err != nil {
					tx.Rollback()
					b.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
