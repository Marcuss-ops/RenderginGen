package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

func TestMetricUnitUsesMetricSemantics(t *testing.T) {
	cases := map[string]string{
		"sha256_us": "us", "render_ms": "ms", "output_bytes": "bytes",
		"chronon_gpu_vram_peak_mb": "mb", "fps": "fps", "cache_hit_ratio": "ratio",
		"frame_count": "count",
	}
	for name, want := range cases {
		if got := metricUnit(name); got != want {
			t.Errorf("metricUnit(%q)=%q, want %q", name, got, want)
		}
	}
}

func TestArtifactPersistedOnComplete(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	art := model.Artifact{
		ID:                 "art-1",
		Kind:               "overlay",
		StorageKey:         "overlay/job-1/out.mp4",
		ArtifactURL:        "s3://bucket/out.mp4",
		ArtifactHash:       "abc123",
		ContentType:        "video/mp4",
		SizeBytes:          12345,
		Width:              1920,
		Height:             1080,
		FPSNum:             30,
		FPSDen:             1,
		FrameCount:         546,
		DurationUS:         18200000,
		ProfileID:          queueclient.CertifiedProfileVeloxH2641080p30V1,
		CopyEligible:       true,
		Codec:              "h264",
		CodecProfile:       "high",
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
		Provenance:         &queueclient.ArtifactProvenance{SchemaVersion: "renderinggen.artifact-provenance.v1", Protocol: "renderinggen.overlay-import.v1", Producer: "pipelinegen-map-v2", ImportJobID: "job-1", ItemIDs: []string{"map-item-1"}, SourceSHA256: "abc123", SourceSizeBytes: 12345, IdentityVerified: true, StructureVerified: true, FullDecodeVerified: true},
	}
	if err := r.Complete(context.Background(), "job-1", "w1", 1, art); err != nil {
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
	var provenanceRaw []byte
	if err := db.QueryRowContext(ctx, `SELECT provenance FROM render_artifacts WHERE id='art-1'`).Scan(&provenanceRaw); err != nil {
		t.Fatalf("query artifact provenance: %v", err)
	}
	var persisted queueclient.ArtifactProvenance
	if err := json.Unmarshal(provenanceRaw, &persisted); err != nil {
		t.Fatalf("decode artifact provenance: %v", err)
	}
	if persisted.Protocol != "renderinggen.overlay-import.v1" || persisted.Producer != "pipelinegen-map-v2" || !persisted.FullDecodeVerified || len(persisted.ItemIDs) != 1 || persisted.ItemIDs[0] != "map-item-1" {
		t.Fatalf("artifact provenance = %+v", persisted)
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

// TestClaimRollsBackWhenArtifactHydrationFails pins the atomicity contract of
// a re-claim: the artifact read happens INSIDE the claim transaction, so a
// corrupt artifact rolls the whole claim back. Before the fix the transaction
// committed first, then hydration failed and the caller saw an error while the
// row was already RUNNING and leased to it — the job was invisible until the
// lease expired.
func TestClaimRollsBackWhenArtifactHydrationFails(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()
	if err := r.Submit(ctx, model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := r.Claim(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Rendered(ctx, "job-1", "w1", claimed.LeaseGeneration, model.Artifact{
		ID: "art-1", Kind: "overlay", StorageKey: "overlay/job-1/out.mp4", ArtifactHash: "abc",
	}, "recertified"); err != nil {
		t.Fatal(err)
	}
	// A JSON scalar is valid JSONB but cannot decode into ArtifactProvenance.
	if _, err := db.ExecContext(ctx, `UPDATE render_artifacts SET provenance = '"corrupt"'::jsonb WHERE id = 'art-1'`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.ClaimState(ctx, "w2", model.StateRendered); err == nil {
		t.Fatal("a corrupt artifact must fail the claim")
	}

	var state string
	var worker sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT state, current_worker_id FROM render_jobs WHERE id = 'job-1'`).Scan(&state, &worker); err != nil {
		t.Fatal(err)
	}
	if state != string(model.StateRendered) {
		t.Fatalf("claim was not rolled back: state = %q, want %q", state, model.StateRendered)
	}
	if worker.Valid {
		t.Fatalf("claim was not rolled back: current_worker_id = %q", worker.String)
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM render_attempts WHERE job_id = 'job-1'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("rolled-back claim created %d attempts, want only the original 1", attempts)
	}
}

// TestClaimBatchHydratesEveryRenderedArtifact pins the batch read that replaced
// the per-job N+1: every re-claimed job carries its artifact, metrics and
// bounded telemetry, and the whole window is hydrated by a fixed number of
// statements rather than three per job.
func TestClaimBatchHydratesEveryRenderedArtifact(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		jobID := fmt.Sprintf("batch-rendered-%d", i)
		if err := r.Submit(ctx, model.Job{ID: jobID}); err != nil {
			t.Fatalf("Submit(%s): %v", jobID, err)
		}
		claimed, _, err := r.ClaimState(ctx, "renderer", model.StatePending)
		if err != nil || claimed == nil {
			t.Fatalf("ClaimState(pending, %s) = %+v, %v", jobID, claimed, err)
		}
		if claimed.ID != jobID {
			t.Fatalf("claimed %s, want the pending %s", claimed.ID, jobID)
		}
		artifact := model.Artifact{
			ID:           "artifact-" + jobID,
			Kind:         "overlay",
			StorageKey:   "sha-" + jobID,
			ArtifactHash: "sha-" + jobID,
			SizeBytes:    int64(100 + i),
			Metrics:      map[string]float64{"render_ms": float64(10 + i)},
			ChrononTelemetry: json.RawMessage(fmt.Sprintf(
				`{"schema":"chronon3d.render-telemetry-summary.v1","job":%q}`, jobID)),
		}
		if err := r.Rendered(ctx, claimed.ID, "renderer", claimed.LeaseGeneration, artifact, "drive deferred"); err != nil {
			t.Fatalf("Rendered(%s): %v", jobID, err)
		}
	}

	jobs, _, err := r.ClaimBatch(ctx, "publisher", 3)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("ClaimBatch returned %d jobs, want 3", len(jobs))
	}
	for _, job := range jobs {
		if job.Artifact == nil {
			t.Fatalf("job %s was re-claimed without its artifact", job.ID)
		}
		if want := "artifact-" + job.ID; job.Artifact.ID != want {
			t.Errorf("job %s artifact = %q, want %q", job.ID, job.Artifact.ID, want)
		}
		if job.Artifact.Metrics["render_ms"] == 0 {
			t.Errorf("job %s lost its processing metrics: %v", job.ID, job.Artifact.Metrics)
		}
		if len(job.Artifact.ChrononTelemetry) == 0 {
			t.Errorf("job %s lost its bounded telemetry", job.ID)
		}
	}
}

func TestGetReturnsJobWithArtifact(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), "job-1", "w1", 1, model.Artifact{
		ID: "art-1", StorageKey: "overlay/job-1/out.mp4", ArtifactHash: "abc", ProfileID: queueclient.CertifiedProfileVeloxH2641080p30V1, CopyEligible: true,
	}); err != nil {
		t.Fatal(err)
	}

	job, err := r.Get(context.Background(), "job-1")
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
	if _, err := r.Get(context.Background(), "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCompleteWithoutArtifactSkipsPersistence(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	// Empty artifact: the job completes but no artifact row is written.
	if err := r.Complete(context.Background(), "job-1", "w1", 1, model.Artifact{}); err != nil {
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
