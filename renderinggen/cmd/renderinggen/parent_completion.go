package main

import (
	"context"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// tryFinalizeParent is the low-latency, synchronous trigger after a child is
// completed. An incomplete parent is normal while sibling work is outstanding;
// the finalizer performs the children read and atomic claim so concurrent
// triggers and worker restarts are safe. A periodic RecoverableParents sweep
// independently retries missed triggers and expired finalizer leases; it runs
// at the configured ClaimLongPoll interval, so lease recovery is bounded by
// that sweep cadence rather than immediate at lease expiry.
// What it does NOT do is pay for the child family twice: the finalizer derives
// the expected frame range from the same read it validates
// (ParentFinalizer.FinalizeFromChildren), so a completed chunk no longer issues
// a Children() round-trip only to learn the range it spans.
func tryFinalizeParent(ctx context.Context, finalizer *processor.ParentFinalizer, parentID string) {
	finalized, artifact, err := finalizer.FinalizeFromChildren(ctx, parentID)
	if err != nil {
		// Incomplete children and a competing finalizer are expected during the
		// normal fan-in; the queue remains the source of truth for retry.
		workerlog.ByJobID(parentID).Warnf("parent not finalized yet: %v", err)
		return
	}
	if finalized {
		workerlog.ByJobID(parentID).Infof("parent finalized: storage_key=%q sha256=%q size=%d", artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes)
	}
}
