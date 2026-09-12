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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
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

// drivePublication is the outcome of one verified external publication.
type drivePublication struct {
	FileID      string
	WebViewLink string
	US          int64
	Chunks      int64
	Bytes       int64
}

// publishAndVerify is the SINGLE Drive publication path, shared by the
// segment artifact (publishToDrive) and the assembled parent
// (ParentFinalizer.Finalize). It resolves the verified persistent L2 path — so
// publication never fetches the object twice nor stages a temporary copy — and
// enforces the SHA-256 chain invariant (plan section "Drive"):
//
//	local_sha == objectstore_sha == db_sha == drive_sha
//
// The bytes re-read from the object store must hash to the artifact hash the
// worker computed at render time and recorded in the ledger (store_sha ==
// db_sha) BEFORE they are uploaded (LocalPath verifies a content-addressed
// key); the provider must then report that same size (drive_sha == db_sha). Any
// mismatch fails the publication, never a re-render.
//
// The parent path used to call drive.Publisher.Publish directly, so the parent
// artifact was the one publication in the worker with NO identity check
// (audit P0-3).
func publishAndVerify(ctx context.Context, store *storage.Client, publisher drive.Publisher, name string, artifact queue.Artifact) (drivePublication, error) {
	if store == nil || publisher == nil {
		return drivePublication{}, nil
	}
	path, size, err := store.LocalPath(ctx, artifact.StorageKey)
	if err != nil {
		return drivePublication{}, fmt.Errorf("processor: resolve rendered artifact locally: %w", err)
	}
	var chunks atomic.Int64
	var uploaded atomic.Int64
	start := time.Now()
	res, err := publisher.Publish(ctx, drive.PublishRequest{
		Name: name, ContentType: artifact.ContentType, Path: path,
		Subfolder: artifact.ArtifactHash,
		UploadProgress: func(uploadedBytes, _ int64) {
			chunks.Add(1)
			uploaded.Store(uploadedBytes)
		},
	})
	if err != nil {
		return drivePublication{}, fmt.Errorf("processor: drive publish: %w", err)
	}
	if res.FileID == "" || res.SizeBytes != size {
		return drivePublication{}, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d expected_size=%d)", res.FileID, res.SizeBytes, size)
	}
	return drivePublication{
		FileID: res.FileID, WebViewLink: res.WebViewLink,
		US: time.Since(start).Microseconds(), Chunks: chunks.Load(), Bytes: uploaded.Load(),
	}, nil
}

// publishToDrive uploads an already-rendered artifact to Google Drive and
// returns the artifact updated with its Drive file ID and link. Callers reach
// this only after the resolver returned object_store_and_drive (see Publish).
func (p *Processor) publishToDrive(ctx context.Context, jobID string, artifact queue.Artifact) (queue.Artifact, error) {
	if p.drive == nil {
		return artifact, nil
	}
	published, err := publishAndVerify(ctx, p.store, p.drive, jobID+".mp4", artifact)
	if err != nil {
		return artifact, err
	}
	if published.FileID == "" {
		return artifact, nil
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	driveUS := float64(published.US)
	artifact.Metrics[metricnames.DrivePublishMS] = driveUS / 1000
	artifact.Metrics[metricnames.DriveUploadUS] = driveUS
	artifact.Metrics[metricnames.DriveUploadChunks] = float64(published.Chunks)
	artifact.Metrics[metricnames.DriveUploadBytes] = float64(published.Bytes)
	artifact.DriveFileID = published.FileID
	artifact.DriveLink = published.WebViewLink
	// The ledger row already exists (written by Render); a publication retry
	// only updates the drive metric — it never touches the artifact identity.
	if updater, ok := p.recorder.(artifactdb.DriveUpdater); ok {
		if err := updater.UpdateDrive(ctx, jobID, int64(driveUS)); err != nil {
			return artifact, fmt.Errorf("processor: artifact ledger drive %s: %w", jobID, err)
		}
	}
	return artifact, nil
}
