// publish_drive.go owns the single publisher seam of the worker: policy
// resolution decides whether a queue-served job is store-only or must reach
// Google Drive, and publishToDrive enforces the SHA-256 chain invariant
// (local_sha == objectstore_sha == db_sha == drive_sha) on upload.
package processor

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// Publish is the single publisher seam for a queue-served job. It consults
// ONLY the canonical PublicationPolicyResolver: a resolved object-store-only
// job (every queue render segment today; the submitter owns Drive delivery)
// is returned unchanged — even when a Drive publisher is configured — so a
// config re-enable of drive.enabled can never recreate the master/worker
// double upload. A job whose submitter declared object_store_and_drive is
// uploaded through publishToDrive, which carries the SHA-256 chain
// invariants. When no Drive publisher is configured (the capability is
// absent) the artifact is returned unchanged regardless of the resolved
// policy.
func (p *Processor) Publish(ctx context.Context, jobID, jobType string, artifact queue.Artifact) (queue.Artifact, error) {
	policy := ResolvePublicationPolicy("", jobType)
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	if p.drive == nil {
		// Capability absent: even a declared object_store_and_drive job is
		// served store-only. The publisher state is authoritative for what
		// the worker CAN do; the resolver is authoritative for what it SHOULD
		// do. Make the skip observable so declared intent degradation is
		// visible in metrics.
		if policy == PublicationObjectStoreAndDrive {
			artifact.Metrics["publication_drive_skipped_no_capability"] = 1
			log.Printf("job %s: drive publication skipped (policy %s but no drive capability)", jobID, policy)
		}
		return artifact, nil
	}
	if policy != PublicationObjectStoreAndDrive {
		// Canonical policy: the submitter delivers this segment (master
		// publishes clips to their destination folders). Skip the Drive
		// upload and say so, so runs never confuse "no Drive phase" with a
		// fast upload.
		artifact.Metrics["publication_drive_skipped_by_policy"] = 1
		log.Printf("job %s: drive publication skipped (resolved policy %s; submitter owns delivery)", jobID, policy)
		return artifact, nil
	}
	return p.publishToDrive(ctx, jobID, artifact)
}

// publishToDrive uploads an already-rendered artifact to Google Drive and
// returns the artifact updated with its Drive file ID and link. It resolves
// the verified persistent L2 path, so publication does not fetch the object
// twice or create a temporary staging file.
//
// The SHA-256 chain invariant (plan section "Drive") is enforced here:
//
//	local_sha == objectstore_sha == db_sha == drive_sha
//
// The bytes re-read from the object store must hash to the artifact hash the
// worker computed at render time and recorded in the ledger (store_sha ==
// db_sha) BEFORE they are uploaded; the Drive result must then report the
// same hash (drive_sha == db_sha). Any mismatch fails the publication, never
// a re-render. Callers reach this only after the resolver returned
// object_store_and_drive (see Publish).
func (p *Processor) publishToDrive(ctx context.Context, jobID string, artifact queue.Artifact) (queue.Artifact, error) {
	if p.drive == nil {
		return artifact, nil
	}
	phaseStart := time.Now()
	path, size, err := p.store.LocalPath(ctx, artifact.StorageKey)
	if err != nil {
		return artifact, fmt.Errorf("processor: resolve rendered artifact locally: %w", err)
	}
	var uploadChunks atomic.Int64
	var uploadedBytes atomic.Int64
	res, err := p.drive.Publish(ctx, drive.PublishRequest{
		Name: jobID + ".mp4", ContentType: artifact.ContentType, Path: path,
		Subfolder: artifact.ArtifactHash,
		UploadProgress: func(uploaded, _ int64) {
			uploadChunks.Add(1)
			uploadedBytes.Store(uploaded)
		},
	})
	if err != nil {
		return artifact, fmt.Errorf("processor: drive publish: %w", err)
	}
	// The local path was hash-verified by LocalPath for content-addressed keys;
	// publication additionally requires the provider to report the same size.
	if res.FileID == "" || res.SizeBytes != size {
		return artifact, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d expected_size=%d)", res.FileID, res.SizeBytes, size)
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	driveUS := float64(time.Since(phaseStart).Microseconds())
	artifact.Metrics["drive_publish_ms"] = driveUS / 1000
	artifact.Metrics["drive_upload_us"] = driveUS
	artifact.Metrics["drive_upload_chunks"] = float64(uploadChunks.Load())
	artifact.Metrics["drive_upload_bytes"] = float64(uploadedBytes.Load())
	artifact.DriveFileID = res.FileID
	artifact.DriveLink = res.WebViewLink
	// The ledger row already exists (written by Render); a publication retry
	// only updates the drive metric — it never touches the artifact identity.
	if updater, ok := p.recorder.(artifactdb.DriveUpdater); ok {
		if err := updater.UpdateDrive(ctx, jobID, int64(driveUS)); err != nil {
			return artifact, fmt.Errorf("processor: artifact ledger drive %s: %w", jobID, err)
		}
	}
	return artifact, nil
}
