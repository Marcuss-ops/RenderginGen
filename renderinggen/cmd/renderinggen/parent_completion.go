package main

import (
	"context"
	"strconv"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// deadFamilyGrace is how long a terminally dead chunk may sit before the sweep
// cancels its family. A terminal failure is not retried by the queue, but an
// operator may still Retry it shortly after; the grace period keeps the sweep
// from racing that decision while still bounding the damage a dead family can
// do to the queue (2026-11-16: four orphan chunk jobs held queue depth for
// 95.964 s and blocked the final job behind them).
const deadFamilyGrace = 10 * time.Minute

// familyReaper is the queue surface the orphan sweep needs. It is an interface
// so the sweep is testable without a live queue.
type familyReaper interface {
	Children(ctx context.Context, parentID string) ([]*queue.Job, error)
	Cancel(ctx context.Context, id string) error
}

// deadFamilyReason returns a non-empty explanation when the chunk family of
// parentID can never be assembled, because at least one child reached a
// terminal state that is not "completed with a certified artifact". A family
// whose children are all pending/running/rendered is healthy: finalization is
// simply not due yet.
func deadFamilyReason(children []*queue.Job) string {
	for _, child := range children {
		if child == nil {
			continue
		}
		switch child.State {
		case queue.StateFailed:
			return "chunk " + strconv.Itoa(child.ChunkIndex) + " failed permanently"
		case queue.StateCancelled:
			return "chunk " + strconv.Itoa(child.ChunkIndex) + " was cancelled"
		}
	}
	return ""
}

// deadFamilyGraceElapsed reports whether the family has been dead long enough
// to reap. A zero QueuedAt means the queue did not report a start time, which
// is treated as "old enough": the caller already proved the family is dead.
func deadFamilyGraceElapsed(children []*queue.Job, now time.Time) bool {
	for _, child := range children {
		if child == nil || (child.State != queue.StateFailed && child.State != queue.StateCancelled) {
			continue
		}
		if child.QueuedAt.IsZero() {
			return true
		}
		return now.Sub(child.QueuedAt) >= deadFamilyGrace
	}
	return false
}

// reapDeadFamily cancels a parent and its still-active children when the family
// can never be assembled. Without this the remaining chunks stay claimable
// forever and every recovery sweep re-logs the same "parent not finalized yet"
// line, which is exactly how four orphan chunks blocked the queue for ~26 h.
//
// It returns true when the family was reaped. Cancellation is best-effort per
// job: a job that reached a terminal state between the read and the cancel is
// already gone, and that is not an error.
func reapDeadFamily(ctx context.Context, q familyReaper, parentID string, now time.Time) bool {
	children, err := q.Children(ctx, parentID)
	if err != nil {
		workerlog.ByJobID(parentID).Warnf("dead family check: %v", err)
		return false
	}
	reason := deadFamilyReason(children)
	if reason == "" || !deadFamilyGraceElapsed(children, now) {
		return false
	}
	workerlog.ByJobID(parentID).Warnf("reaping dead chunk family: %s (children=%d)", reason, len(children))
	cancelled := 0
	if err := q.Cancel(ctx, parentID); err != nil {
		workerlog.ByJobID(parentID).Warnf("reap parent: %v", err)
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		switch child.State {
		case queue.StateCompleted, queue.StateFailed, queue.StateCancelled:
			continue
		}
		if err := q.Cancel(ctx, child.ID); err != nil {
			workerlog.ByJobID(parentID).Warnf("reap child %s: %v", child.ID, err)
			continue
		}
		cancelled++
	}
	workerlog.ByJobID(parentID).Infof("dead chunk family cancelled: parent=%s children_cancelled=%d", parentID, cancelled)
	return true
}

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
