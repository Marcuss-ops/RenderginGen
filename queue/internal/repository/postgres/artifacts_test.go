package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
)

func TestArtifactPersistedOnComplete(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}

	art := model.Artifact{
		ID:                 "art-1",
		Kind:               "overlay",
		StorageKey:         "overlay/job-1/out.mp4",
		URL:                "s3://bucket/out.mp4",
		SHA256:             "abc123",
		MimeType:           "video/mp4",
		SizeBytes:          12345,
		Width:              1920,
		Height:             1080,
		FPSNum:             30,
		FPSDen:             1,
		FrameCount:         546,
		DurationUS:         18200000,
		ProfileID:          "velox-h264-copy-v1",
		CopyEligible:       true,
		Codec:              "h264",
		CodecProfile:       "high",
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
	}
	if err := r.Complete("job-1", "w1", art); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	var (
		sha          string
		copyEligible bool
		codec        string
		closedGOP    bool
	)
	err := db.QueryRowContext(ctx, `
		SELECT sha256, copy_eligible, codec, closed_gop
		FROM render_artifacts
		WHERE id = 'art-1'`).Scan(&sha, &copyEligible, &codec, &closedGOP)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if sha != "abc123" || !copyEligible || codec != "h264" || !closedGOP {
		t.Fatalf("artifact not persisted correctly: sha=%q copy=%v codec=%q gop=%v",
			sha, copyEligible, codec, closedGOP)
	}

	var jobArtifactID string
	if err := db.QueryRowContext(ctx, `
		SELECT artifact_id FROM render_jobs WHERE id = 'job-1'`).Scan(&jobArtifactID); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if jobArtifactID != "art-1" {
		t.Fatalf("job not linked to artifact: got %q", jobArtifactID)
	}
}

func TestGetReturnsJobWithArtifact(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("job-1", "w1", model.Artifact{
		ID: "art-1", StorageKey: "overlay/job-1/out.mp4", SHA256: "abc", ProfileID: "velox-copy-v1", CopyEligible: true,
	}); err != nil {
		t.Fatal(err)
	}

	job, err := r.Get("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateCompleted {
		t.Fatalf("want completed, got %s", job.State)
	}
	if job.Artifact == nil || job.Artifact.ID != "art-1" || !job.Artifact.CopyEligible {
		t.Fatalf("artifact not returned: %+v", job.Artifact)
	}
}

func TestGetMissingJobReturnsNotFound(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if _, err := r.Get("missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCompleteWithoutArtifactSkipsPersistence(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	// Empty artifact: the job completes but no artifact row is written.
	if err := r.Complete("job-1", "w1", model.Artifact{}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM render_artifacts WHERE job_id = 'job-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("want 0 artifacts, got %d", count)
	}
}
