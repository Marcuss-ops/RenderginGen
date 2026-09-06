package processor

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
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

// TestPublishWithoutDriveCapabilityIsStoreOnly verifies the capability
// constraint: with no configured Drive publisher the artifact is returned
// unchanged even when the resolved policy asks for Drive — the config only
// declares what the worker CAN do, never what a job SHOULD do.
func TestPublishWithoutDriveCapabilityIsStoreOnly(t *testing.T) {
	proc, _, _ := newProcessor(t)
	artifact := queue.Artifact{StorageKey: "k", ArtifactHash: "h"}
	published, err := proc.Publish(context.Background(), "job-1", queue.JobTypeRenderSegment, artifact)
	if err != nil {
		t.Fatalf("publish without capability: %v", err)
	}
	if published.DriveFileID != "" || published.DriveLink != "" {
		t.Fatalf("publish without capability must not reach Drive: %+v", published)
	}
}
