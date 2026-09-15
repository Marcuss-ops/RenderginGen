package main

// Startup helpers extracted from main.go so the command stays under the
// per-file LOC budget. Behavior is identical to the original inline
// goroutines.

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// startWorkspaceCleanup reaps workspaces left behind by a crashed worker
// run: without this the jobs root (often /dev/shm, i.e. RAM) grows
// unboundedly. Active workspaces carry a lease marker (written at
// PrepareJob, refreshed by the GPU lane while rendering) and are skipped;
// anything older than pipeline.workspace_stale_after without a valid marker is
// removed. Parent artifacts have their own cleanup (see
// ParentFinalizer.Finalize).
func startWorkspaceCleanup(ctx context.Context, root string, timings config.PipelineConfig) {
	ticker := time.NewTicker(timings.WorkspaceSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := workspace.CleanupStale(root, timings.WorkspaceStaleAfter); err != nil {
				log.Printf("workspace stale cleanup: %v", err)
			}
		}
	}
}

// runHeartbeatLoop keeps the worker's queue heartbeat alive, surfacing
// sustained queue disconnect on /health (via heartbeatFailures) instead of
// logging-and-forgetting: a worker whose heartbeat cannot reach the queue
// is not fully ready even while it may still be processing a claim.
func runHeartbeatLoop(ctx context.Context, q *queue.Client, heartbeatFailures *atomic.Int64, timings config.PipelineConfig) {
	degradedThreshold := int64(timings.DegradedAfterFailures)
	ticker := time.NewTicker(timings.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := q.Heartbeat(ctx); err != nil {
				failures := heartbeatFailures.Add(1)
				log.Printf("queue worker heartbeat: %v (consecutive failures=%d)", err, failures)
				if failures == degradedThreshold {
					log.Printf("queue worker heartbeat degraded: %d consecutive failures; /health reports degraded until heartbeat recovers", failures)
				}
				continue
			}
			if prev := heartbeatFailures.Swap(0); prev >= degradedThreshold {
				log.Printf("queue worker heartbeat recovered after %d consecutive failures", prev)
			}
		}
	}
}
