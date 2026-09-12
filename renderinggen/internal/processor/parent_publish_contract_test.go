package processor

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// parentArtifact builds the artifact shape ParentFinalizer produces for an
// assembled chunk parent (see artifactFromFile): content-addressed key equal to
// the hash, mp4 content type.
func parentArtifact(body []byte) queue.Artifact {
	hash := storage.Hash(body)
	return queue.Artifact{
		Kind:         "parent",
		StorageKey:   hash,
		ArtifactHash: hash,
		ContentType:  "video/mp4",
		SizeBytes:    int64(len(body)),
	}
}

// TestParentPublicationUsesTheVerifiedDrivePath pins the fix for the split
// publication authority (audit P0-3): the assembled parent used to call
// drive.Publisher.Publish directly, so it was the one publication in the worker
// with NO store_sha == db_sha check and no provider identity check, while the
// segment path enforced the whole chain. Both now go through publishAndVerify,
// so a lying publisher must fail the parent publication exactly as it fails a
// segment publication.
func TestParentPublicationUsesTheVerifiedDrivePath(t *testing.T) {
	_, store, _ := newProcessor(t)
	body := []byte("assembled-parent-bytes")
	artifact := parentArtifact(body)
	if err := store.Put(context.Background(), artifact.StorageKey, body); err != nil {
		t.Fatalf("put parent: %v", err)
	}

	// Honest publisher: the chain verifies and the identity is returned.
	okPublisher := drive.NewMock(t.TempDir(), 0)
	published, err := publishAndVerify(context.Background(), store, okPublisher, "parent-1.mp4", artifact)
	if err != nil {
		t.Fatalf("parent publish through the canonical path: %v", err)
	}
	if published.FileID == "" {
		t.Fatalf("parent publication produced no Drive identity: %+v", published)
	}

	// Lying publisher (reports a different size): must fail, never complete a
	// parent whose bytes on Drive are not the bytes the worker hashed.
	if _, err := publishAndVerify(context.Background(), store, badSizePublisher{}, "parent-1.mp4", artifact); err == nil {
		t.Fatal("parent publication must fail when the provider identity does not match db_sha")
	}
}

// TestParentPublicationFailsOnStoreDrift proves the store_sha == db_sha leg for
// the parent too: corrupted object-store bytes must fail before upload.
func TestParentPublicationFailsOnStoreDrift(t *testing.T) {
	store := storage.New(corruptBackend{}, storage.Options{})
	body := []byte("expected-parent-bytes")
	artifact := parentArtifact(body) // never Put through this client: Get hits the corrupt backend
	if _, err := publishAndVerify(context.Background(), store, drive.NewMock(t.TempDir(), 0), "parent-1.mp4", artifact); err == nil {
		t.Fatal("parent publication must fail when the stored bytes do not hash to db_sha")
	}
}

// TestPublishAndVerifyIsTotalOnMissingCapabilities pins that the shared helper
// degrades to a no-op (no panic, no upload) when the worker has no store or no
// publisher: the parent finalizer calls it with a possibly-absent publisher.
func TestPublishAndVerifyIsTotalOnMissingCapabilities(t *testing.T) {
	artifact := parentArtifact([]byte("bytes"))
	if published, err := publishAndVerify(context.Background(), nil, drive.NewMock(t.TempDir(), 0), "parent.mp4", artifact); err != nil || published.FileID != "" {
		t.Fatalf("no store must be a no-op, got (%+v, %v)", published, err)
	}
	_, store, _ := newProcessor(t)
	if err := store.Put(context.Background(), artifact.StorageKey, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if published, err := publishAndVerify(context.Background(), store, nil, "parent.mp4", artifact); err != nil || published.FileID != "" {
		t.Fatalf("no publisher must be a no-op, got (%+v, %v)", published, err)
	}
}
