package postgres

import (
	"fmt"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	_ "github.com/jackc/pgx/v5/stdlib"
	"sync"
	"testing"
	"time"
)

func TestRenderedThenClaimPublishOnlyThenComplete(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-rendered"}); err != nil {
		t.Fatal(err)
	}

	// Attempt 1: render done, Drive publication fails -> rendered.
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	stored := model.Artifact{
		StorageKey:   "sha256-abc",
		ArtifactHash: "sha256-abc",
		ContentType:  "video/mp4",
		SizeBytes:    123,
		Width:        1280,
		Height:       720,
		DurationUS:   3_000_000,
	}
	if err := r.Rendered("job-rendered", "w1", stored, "drive: upload failed"); err != nil {
		t.Fatalf("rendered: %v", err)
	}

	got, err := r.Get("job-rendered")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.StateRendered {
		t.Fatalf("want rendered state, got %s", got.State)
	}
	if got.Artifact == nil || got.Artifact.StorageKey != "sha256-abc" {
		t.Fatalf("rendered artifact not recorded: %+v", got.Artifact)
	}

	// Attempt 2: re-claim in the rendered state -> artifact returned for a
	// publication-only retry (no re-render).
	again, _, err := r.Claim("w2")
	if err != nil {
		t.Fatal(err)
	}
	if again == nil || again.ID != "job-rendered" || again.Attempts != 2 {
		t.Fatalf("re-claim: %+v", again)
	}
	if again.Artifact == nil || again.Artifact.StorageKey != "sha256-abc" {
		t.Fatalf("claimed rendered job must carry its artifact: %+v", again.Artifact)
	}

	// Publication retry succeeds -> completed with Drive fields.
	published := stored
	published.DriveFileID = "drive-1"
	published.DriveLink = "https://drive.example.com/file/d/drive-1"
	if err := r.Complete("job-rendered", "w2", published); err != nil {
		t.Fatalf("complete: %v", err)
	}

	done, err := r.Get("job-rendered")
	if err != nil {
		t.Fatal(err)
	}
	if done.State != model.StateCompleted {
		t.Fatalf("want completed state, got %s", done.State)
	}
	if done.Artifact == nil || done.Artifact.DriveFileID != "drive-1" || done.Artifact.DriveLink != "https://drive.example.com/file/d/drive-1" {
		t.Fatalf("drive fields not persisted on completion: %+v", done.Artifact)
	}

	// Attempt history preserved: #1 rendered, #2 completed.
	var status string
	if err := db.QueryRow(`SELECT status FROM render_attempts WHERE job_id='job-rendered' AND attempt_number=1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "rendered" {
		t.Fatalf("attempt 1 status = %q, want rendered", status)
	}
	if err := db.QueryRow(`SELECT status FROM render_attempts WHERE job_id='job-rendered' AND attempt_number=2`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("attempt 2 status = %q, want completed", status)
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
