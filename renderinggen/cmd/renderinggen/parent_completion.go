package main

import (
	"context"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// tryFinalizeParent is intentionally best-effort: a child completion can be
// observed before its siblings, so an incomplete parent is normal. The
// finalizer itself performs the second children read and atomic claim, which
// makes concurrent attempts and worker restarts safe.
//
// It stays SYNCHRONOUS on the post pool, deliberately. A child completion is the
// only trigger for parent finalization in this worker — there is no periodic
// sweep that would adopt a detached attempt — so detaching it would let one
// failed attempt strand the parent until an unrelated event re-triggered it.
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
