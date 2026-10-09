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
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// pruneJobLogs bounds the durable per-job log directory at startup.
//
// Those files are the worker's own record of a run — the artifact that makes the
// collector independent of the host's journald retention — so they are kept, not
// deleted after the job. What they must NOT be is unbounded: the directory lives
// under the jobs root (frequently tmpfs, i.e. RAM), so retention and a file
// count cap are applied once at startup, before any job can add to it.
func pruneJobLogs(cfg *config.Config) {
	dir := workerlog.DurableJobLogDir(cfg.Workspace.Root)
	if dir == "" {
		return
	}
	removed, err := workerlog.PruneJobLogs(dir, cfg.Logging.JobLogRetention, cfg.Logging.JobLogMaxFiles)
	if err != nil {
		log.Printf("worker job logs: prune %s: %v", dir, err)
		return
	}
	if removed > 0 {
		log.Printf("worker job logs: pruned %d file(s) from %s (retention=%s, max_files=%d)",
			removed, dir, cfg.Logging.JobLogRetention, cfg.Logging.JobLogMaxFiles)
	}
}

// startWorkspaceCleanup reaps workspaces left behind by a crashed worker
// run: without this the jobs root (often /dev/shm, i.e. RAM) grows
// unboundedly. A valid lease marker OR an active process lock protects a live
// workspace; only unlocked candidates older than pipeline.workspace_stale_after
// without a valid marker are removed. Parent artifacts have their own cleanup (see
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

// runParentFinalizationRecovery is the retry path for missed child-completion
// triggers and expired finalizer leases. It polls at interval (the worker passes
// ClaimLongPoll), so an expired lease becomes eligible for adoption on a later
// sweep, not synchronously at the expiration instant.
func runParentFinalizationRecovery(ctx context.Context, q *queue.Client, finalizer *processor.ParentFinalizer, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := q.RecoverableParents(ctx)
			if err != nil {
				log.Printf("parent finalization recovery list: %v", err)
				continue
			}
			for _, id := range ids {
				tryFinalizeParent(ctx, finalizer, id)
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
