// Artifact mirror and publication invariants: the diagnostic mirror must never
// fail a render, queue jobs are store-only by policy, and the Drive seam
// enforces the local_sha == objectstore_sha == db_sha == drive_sha chain.
package processor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestProcessMirrorFailureKeepsRenderCompleted pins the diagnostic-mirror
// contract: when the worker-local SQLite mirror fails to Record, the render
// stays completed (Process returns a certified artifact and no error), the
// artifact bytes are still in the object store, and the completed artifact's
// metrics carry mirror_failure=1 so the divergence is observable.
func TestProcessMirrorFailureKeepsRenderCompleted(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	proc.SetArtifactRecorder(failingRecorder{})
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Process(context.Background(), validJob())
	if err != nil {
		t.Fatalf("mirror failure must not fail the render: %v", err)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey == "" {
		t.Fatalf("artifact must still be certified when the mirror fails: %+v", artifact)
	}
	if artifact.Metrics == nil || artifact.Metrics["mirror_failure"] != 1 {
		t.Fatalf("mirror_failure metric = %v, want 1 (artifact %+v)", artifact.Metrics["mirror_failure"], artifact)
	}
	if _, err := store.Get(context.Background(), artifact.StorageKey); err != nil {
		t.Fatalf("artifact bytes must be stored even when the mirror fails: %v", err)
	}
}

// TestPublishSkipsDriveForQueueJobs verifies the canonical publication policy:
// a queue-served render job resolves to object-store-only (its submitter —
// PipelineGen master — owns Drive delivery of clips), so even when the worker
// holds a Drive publisher capability it never uploads the segment a second
// time. The skip is recorded on the artifact so a run can never confuse "no
// Drive phase" with a fast upload.
func TestPublishSkipsDriveForQueueJobs(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, jobType := range []string{queue.JobTypeRenderSegment, queue.JobTypeOverlayRender, queue.JobTypeOverlayPrepare} {
		published, err := proc.Publish(context.Background(), validJob().ID, jobType, artifact)
		if err != nil {
			t.Fatalf("publish (%s): %v", jobType, err)
		}
		if published.DriveFileID != "" || published.DriveLink != "" {
			t.Fatalf("queue job %s must not reach Drive: %+v", jobType, published)
		}
		if published.Metrics["publication_drive_skipped_by_policy"] != 1 {
			t.Fatalf("queue job %s must record the policy skip: %v", jobType, published.Metrics)
		}
	}
	if rec, _ := ledger.Get(validJob().ID); rec.DriveUploadUS != 0 {
		t.Fatalf("queue job publish must not touch the drive ledger: %+v", rec)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1", renderer.calls)
	}
}

// TestPublishUpdatesLedgerDriveMetric verifies the Drive implementation seam
// (publishToDrive, reached only after the resolver returned
// object_store_and_drive) updates drive_upload_us in the ledger without
// re-rendering (RENDERED -> PUBLISH_RETRY -> PUBLISHED, never a Chronon
// re-render).
func TestPublishUpdatesLedgerDriveMetric(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	rec, _ := ledger.Get(validJob().ID)
	if rec.DriveUploadUS != 0 {
		t.Fatalf("drive_upload_us before publish = %d, want 0", rec.DriveUploadUS)
	}

	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))
	if _, err := proc.publishToDrive(context.Background(), validJob().ID, artifact); err != nil {
		t.Fatalf("publish: %v", err)
	}
	rec, _ = ledger.Get(validJob().ID)
	if rec.DriveUploadUS <= 0 {
		t.Fatalf("drive_upload_us after publish = %d, want > 0", rec.DriveUploadUS)
	}
	if rec.ArtifactHash != artifact.ArtifactHash {
		t.Fatalf("drive update must not touch artifact identity: %+v", rec)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1 (no re-render on publish retry)", renderer.calls)
	}
}

func TestRenderThenPublishRetrySkipsRender(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	proc.SetPublisher(drive.NewMock(t.TempDir(), 1)) // first upload fails

	// Render succeeds and stores the artifact in the object store.
	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if artifact.StorageKey == "" || artifact.ArtifactHash == "" {
		t.Fatalf("artifact not stored: %+v", artifact)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1", renderer.calls)
	}

	// First publication fails (simulated Drive failure) on the Drive
	// implementation seam (the worker's publishToDrive, reachable only for
	// object_store_and_drive jobs).
	if _, err := proc.publishToDrive(context.Background(), validJob().ID, artifact); err == nil {
		t.Fatal("expected publish error on first upload")
	}

	// The publication retry succeeds and never touches the renderer.
	published, err := proc.publishToDrive(context.Background(), validJob().ID, artifact)
	if err != nil {
		t.Fatalf("publish retry: %v", err)
	}
	if published.DriveFileID == "" || published.DriveLink == "" {
		t.Fatalf("drive fields not set after retry: %+v", published)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer re-ran on publish retry: calls = %d, want 1", renderer.calls)
	}
}

// TestPublishEnforcesStoreDBInvariant proves the store_sha == db_sha leg of
// the chain: if the object store returns bytes that do not hash to the
// artifact hash the worker recorded (corruption), Publish must fail BEFORE
// anything is uploaded to Drive.
func TestPublishEnforcesStoreDBInvariant(t *testing.T) {
	// A backend that returns corrupted bytes for every fetch: the store cache
	// never overwrites a content-addressed key, so the corruption must be
	// simulated at the L3 backend where a real object store could drift. The
	// artifact key is NOT in the L1/L2 cache (it was never Put through this
	// client), so Get falls through to the corrupted backend.
	store := storage.New(corruptBackend{}, storage.Options{})
	proc := New(t.TempDir(), "software", "0.1.0", "http://store:9000", store, &fakeRenderer{})
	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))

	// A claimed rendered job whose stored bytes no longer match the recorded
	// hash (the object store drifted from what the worker hashed).
	artifact := queue.Artifact{
		Kind:         "segment",
		StorageKey:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactHash: storage.Hash([]byte("expected-bytes")),
		ContentType:  "video/mp4",
	}
	if _, err := proc.publishToDrive(context.Background(), "job-1", artifact); err == nil {
		t.Fatal("publish must fail when store bytes do not match db_sha")
	}
}

// TestPublishEnforcesDriveInvariant proves the provider identity leg of the
// chain: a publisher that reports an incorrect uploaded size fails the job.
func TestPublishEnforcesDriveInvariant(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	proc.SetPublisher(badSizePublisher{})

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := proc.publishToDrive(context.Background(), validJob().ID, artifact); err == nil {
		t.Fatal("publish must fail when drive_sha != db_sha")
	}
}

// TestPublishSHAChainInvariant walks the whole chain end to end — render,
// store, ledger and Drive — and proves local_sha == store_sha == db_sha ==
// drive_sha: the hashes recorded in the artifact, the ledger, and the Drive
// result are all the same, and the bytes Drive received are the bytes the
// worker hashed.
func TestPublishSHAChainInvariant(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	ledger := artifactdb.NewMemory()
	proc.SetArtifactRecorder(ledger)
	driveDir := t.TempDir()
	publisher := drive.NewMock(driveDir, 0)
	proc.SetPublisher(publisher)
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	artifact, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	published, err := proc.publishToDrive(context.Background(), validJob().ID, artifact)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// local_sha == store_sha: the object store holds the hashed bytes.
	localSHA := storage.Hash([]byte("output-bytes"))
	stored, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("get stored: %v", err)
	}
	if got := storage.Hash(stored); got != localSHA {
		t.Fatalf("store_sha = %s, want %s", got, localSHA)
	}
	// db_sha: the ledger record carries the same hash.
	rec, ok := ledger.Get(validJob().ID)
	if !ok {
		t.Fatal("ledger record missing")
	}
	if rec.ArtifactHash != localSHA || rec.StorageKey != artifact.StorageKey {
		t.Fatalf("db_sha mismatch: rec=%+v", rec)
	}
	// drive_sha: the publisher reported the same hash and wrote the bytes.
	if published.DriveFileID == "" {
		t.Fatalf("drive fields missing: %+v", published)
	}
	driveFiles, err := filepath.Glob(filepath.Join(driveDir, artifact.ArtifactHash, "*.mp4"))
	if err != nil || len(driveFiles) != 1 {
		t.Fatalf("drive files = %v (err %v), want exactly one", driveFiles, err)
	}
	written, err := os.ReadFile(driveFiles[0])
	if err != nil {
		t.Fatalf("read drive file: %v", err)
	}
	if got := storage.Hash(written); got != localSHA {
		t.Fatalf("drive file sha = %s, want %s", got, localSHA)
	}
}
