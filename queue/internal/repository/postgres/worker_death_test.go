package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestWorkerDeathBetweenPublishAndCompleteLeavesOneLedger pins scenario 7 of
// the GPU/lease fault matrix. The first worker publishes (Rendered) and then
// dies before Complete; a second worker re-claims the RENDERED job. The stale
// generation must be fenced out of Complete, the current one must succeed, and
// the job must end with exactly one artifact row and one completion — never a
// double COMPLETED or a second ledger row.
func TestWorkerDeathBetweenPublishAndCompleteLeavesOneLedger(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	if err := r.Submit(ctx, model.Job{ID: "death-after-publish"}); err != nil {
		t.Fatal(err)
	}
	first, _, err := r.Claim(ctx, "worker-a")
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	artifact := model.Artifact{
		ID:           "artifact-death-after-publish",
		Kind:         "overlay",
		StorageKey:   "sha-death-after-publish",
		ArtifactHash: "sha-death-after-publish",
		SizeBytes:    1024,
	}
	if err := r.Rendered(ctx, first.ID, "worker-a", first.LeaseGeneration, artifact, "published, then the worker died"); err != nil {
		t.Fatalf("Rendered: %v", err)
	}

	// The publisher re-claims the RENDERED job and hydrates the artifact the
	// dead worker already published.
	second, _, err := r.ClaimState(ctx, "worker-b", model.StateRendered)
	if err != nil || second == nil {
		t.Fatalf("re-claim rendered = %+v, %v", second, err)
	}
	if second.Artifact == nil || second.Artifact.ID != artifact.ID {
		t.Fatalf("re-claimed job lost its published artifact: %+v", second.Artifact)
	}
	if second.LeaseGeneration <= first.LeaseGeneration {
		t.Fatalf("fencing generation did not advance: first=%d second=%d", first.LeaseGeneration, second.LeaseGeneration)
	}

	// The dead worker's late Complete is fenced.
	if err := r.Complete(ctx, first.ID, "worker-a", first.LeaseGeneration, artifact); err == nil {
		t.Fatal("the dead worker's stale Complete was accepted")
	}
	// The current owner completes the same published artifact.
	if err := r.Complete(ctx, second.ID, "worker-b", second.LeaseGeneration, artifact); err != nil {
		t.Fatalf("current Complete: %v", err)
	}

	var state string
	if err := db.QueryRowContext(ctx,
		`SELECT state FROM render_jobs WHERE id = 'death-after-publish'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(model.StateCompleted) {
		t.Fatalf("state = %q, want completed", state)
	}
	var artifacts int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM render_artifacts WHERE job_id = 'death-after-publish'`).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if artifacts != 1 {
		t.Fatalf("artifact rows = %d, want exactly one ledger row", artifacts)
	}
	var completions int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM render_events WHERE job_id = 'death-after-publish' AND event_type = 'JOB_COMPLETED'`).Scan(&completions); err != nil {
		t.Fatal(err)
	}
	if completions != 1 {
		t.Fatalf("completion events = %d, want exactly one", completions)
	}

	// A retried Complete from the current generation is idempotent at most; it
	// must not create a second row or event.
	_ = r.Complete(ctx, second.ID, "worker-b", second.LeaseGeneration, artifact)
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM render_events WHERE job_id = 'death-after-publish' AND event_type = 'JOB_COMPLETED'`).Scan(&completions); err != nil {
		t.Fatal(err)
	}
	if completions != 1 {
		t.Fatalf("duplicate Complete produced %d completion events, want one", completions)
	}
}
