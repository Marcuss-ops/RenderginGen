package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// TestResolvePublicationPolicy pins the canonical resolver table: a declared
// policy wins; every undeclared queue render type defaults to
// object-store-only because the queue contract is "render a segment" and the
// submitter (PipelineGen master) owns Drive delivery. Unknown declarations
// must never guess a Drive upload.
func TestResolvePublicationPolicy(t *testing.T) {
	for _, jobType := range []string{queue.JobTypeRenderSegment, queue.JobTypeOverlayRender, queue.JobTypeOverlayPrepare} {
		if got := ResolvePublicationPolicy("", jobType); got != PublicationObjectStoreOnly {
			t.Errorf("ResolvePublicationPolicy(\"\", %q) = %q, want %q", jobType, got, PublicationObjectStoreOnly)
		}
	}
	if got := ResolvePublicationPolicy(string(PublicationObjectStoreAndDrive), queue.JobTypeRenderSegment); got != PublicationObjectStoreAndDrive {
		t.Errorf("declared and-drive = %q, want %q", got, PublicationObjectStoreAndDrive)
	}
	if got := ResolvePublicationPolicy(string(PublicationObjectStoreOnly), queue.JobTypeRenderSegment); got != PublicationObjectStoreOnly {
		t.Errorf("declared store-only = %q, want %q", got, PublicationObjectStoreOnly)
	}
	// Unknown/empty declarations resolve to the store-only default, never to a
	// guessed Drive upload.
	if got := ResolvePublicationPolicy("publish_everywhere", queue.JobTypeRenderSegment); got != PublicationObjectStoreOnly {
		t.Errorf("unknown declaration = %q, want store-only default", got)
	}
}

// TestPublishWithoutDriveCapabilityFailsClosed verifies a required Drive
// destination cannot be silently skipped or reported successful.
func TestPublishWithoutDriveCapabilityFailsClosed(t *testing.T) {
	proc, _, _ := newProcessor(t)
	artifact := queue.Artifact{StorageKey: "k", ArtifactHash: "h"}
	published, err := proc.Publish(context.Background(), "job-1", queue.JobTypeRenderSegment, string(PublicationObjectStoreAndDrive), artifact)
	if !errors.Is(err, ErrDrivePublicationPermanent) {
		t.Fatalf("publish without required capability error = %v, want ErrDrivePublicationPermanent", err)
	}
	if published.DriveFileID != "" || published.DriveLink != "" {
		t.Fatalf("publish without capability must not reach Drive: %+v", published)
	}
	if published.Metrics["publication_drive_skipped_no_capability"] != 1 {
		t.Fatalf("missing-capability metric = %v, want 1", published.Metrics)
	}
}

func TestPublishWithoutDriveCapabilityAllowsExplicitStoreOnly(t *testing.T) {
	proc, _, _ := newProcessor(t)
	artifact := queue.Artifact{StorageKey: "k", ArtifactHash: "h"}
	published, err := proc.Publish(context.Background(), "job-1", queue.JobTypeRenderSegment, string(PublicationObjectStoreOnly), artifact)
	if err != nil {
		t.Fatalf("store-only publication should not require Drive: %v", err)
	}
	if published.Metrics["publication_drive_skipped_by_policy"] != 1 {
		t.Fatalf("store-only policy metric missing: %v", published.Metrics)
	}
}
