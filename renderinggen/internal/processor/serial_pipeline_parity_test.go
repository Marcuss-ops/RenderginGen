package processor

import (
	"context"
	"os"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
)

// TestSerialProcessAppliesPublicationPolicy pins the serial/pool parity that
// the staged-pipeline docs claim: the worker pools run FinalizeJob and then
// Publish, and Process must be exactly that composition. Render alone stops
// before publication (so a failed upload can be retried without a re-render),
// but Process must always resolve and apply the canonical publication policy.
//
// The observable difference is the policy decision recorded on the artifact:
// Publish stamps publication_drive_skipped_by_policy for queue render
// segments. If a future refactor drops the Publish call from Process, the
// serial path would silently stop applying publication policy while the pools
// keep doing so — a split-brain this test makes impossible.
func TestSerialProcessAppliesPublicationPolicy(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}
	// A Drive capability is configured, so the absence of an upload is a
	// POLICY decision, not a missing dependency.
	proc.SetPublisher(drive.NewMock(t.TempDir(), 0))

	rendered, err := proc.Render(context.Background(), validJob())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.Metrics["publication_drive_skipped_by_policy"] != 0 {
		t.Fatalf("Render must stop before publication, got metrics %v", rendered.Metrics)
	}

	published, err := proc.Process(context.Background(), validJob())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if published.Metrics["publication_drive_skipped_by_policy"] != 1 {
		t.Fatalf("Process must apply the publication policy (same as the pools), got metrics %v", published.Metrics)
	}
}
