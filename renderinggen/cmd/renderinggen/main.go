// Command renderinggen is the RenderingGen GPU worker.
//
// Startup sequence:
//
//	load config -> detect GPU -> verify Chronon -> connect queue -> READY -> claim jobs
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/gpu"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/health"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/progresspush"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/version"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

func main() {
	configPath := flag.String("config", "/etc/renderinggen/config.yaml", "path to config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Reap workspaces left behind by a crashed worker run: without this the
	// jobs root (often /dev/shm, i.e. RAM) grows unboundedly. Active
	// workspaces carry a lease marker (written at PrepareJob, refreshed by
	// the GPU lane while rendering) and are skipped; anything older than one
	// hour without a valid marker is removed. Parent artifacts have their
	// own cleanup (see ParentFinalizer.Finalize).
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := workspace.CleanupStale(cfg.Workspace.Root, time.Hour); err != nil {
					log.Printf("workspace stale cleanup: %v", err)
				}
			}
		}
	}()

	// 1. Detect GPU.
	gpuInfo := gpu.Detect(cfg.GPU.Device)
	if !gpuInfo.Present {
		log.Println("WARN: no GPU detected; rendering will fail")
	} else {
		log.Printf("GPU detected: backend=%s driver=%s device=%d",
			gpuInfo.Backend, gpuInfo.Driver, gpuInfo.Device)
	}

	// 2. Select the renderer backend: CLI subprocess (default) or the
	// persistent Chronon3d daemon over IPC. The installed Chronon version
	// must always be read from cfg.Chronon.Home/VERSION regardless of
	// mode: previously the IPC branch kept chrononVersion at the literal
	// "unknown" sentinel, so render_artifacts recorded chronon_version=
	// 'unknown' for every job even when /opt/chronon3d/VERSION was present.
	var renderer chronon.Renderer
	var assembler chronon.Assembler
	chrononVersion := "unknown"
	{
		probe := &chronon.Client{Home: cfg.Chronon.Home}
		// Verify only when we're about to invoke the CLI directly; the IPC
		// branch validates the daemon via NewIPCClient below. Read VERSION
		// unconditionally so the recorded version is never silently 'unknown'.
		if v := probe.Version(); v != "unknown" {
			chrononVersion = v
		}
	}
	if cfg.Chronon.Mode == "ipc" {
		ipc := chronon.NewIPCClient(cfg.Chronon.SocketPath)
		renderer = ipc
		assembler = ipc
	} else {
		// The Client carries the semantic knobs its Verify() handshake
		// validates against: a worker configured for the native GPU hot path
		// must refuse a Chronon binary compiled without that path (fail-fast
		// before READY — never after accepting jobs).
		cli := &chronon.Client{
			Home:                cfg.Chronon.Home,
			Backend:             cfg.Chronon.Backend,
			StrictNativeBackend: cfg.Chronon.StrictNativeBackend || cfg.Chronon.Profile == "gpu-vulkan-native",
			HardwareEncoder:     cfg.Chronon.HardwareEncoder,
		}
		if err := cli.Verify(); err != nil {
			log.Fatalf("chronon: %v", err)
		}
		renderer = cli
		if chrononVersion == "unknown" {
			// CLI mode with no VERSION file: prefer the value the in-process
			// client just read (covers the unlikely case where Home was empty
			// at the probe call but got populated here).
			chrononVersion = cli.Version()
		}
	}
	// CPU/I/O stages execute concurrently, and the renderer is allowed up to
	// cfg.Worker.GPULanes concurrent GPU sessions (the config default of 2
	// matches the NVENC multi-session baseline on RTX A4000-class hosts).
	gpuLanes := cfg.Worker.GPULanes
	renderer = chronon.LimitConcurrency(renderer, gpuLanes)

	// 3. Connect queue + storage.
	queueClient := queue.New(cfg.Queue.Endpoint, cfg.Worker.ID)
	store := storage.New(
		storage.NewHTTP(cfg.ArtifactStore.Endpoint),
		storage.Options{
			L1MaxBytes: 256 << 20, // 256 MiB small-object RAM cache
			L2Dir:      cfg.ArtifactStore.LocalCacheDir,
			L2MaxBytes: 10 << 30, // 10 GiB NVMe cache
		},
	)

	// The processor owns the per-job pipeline (validate -> workspace ->
	// materialize -> plan.json -> render -> hash -> publish).
	proc := processor.New(
		cfg.Workspace.Root,
		cfg.Chronon.Backend,
		chrononVersion,
		cfg.ArtifactStore.Endpoint,
		store,
		renderer,
	)
	proc.SetNativeOutputProfiles(cfg.Chronon.NativeOutputProfiles)
	proc.SetStrictNativeBackend(cfg.Chronon.StrictNativeBackend || cfg.Chronon.Profile == "gpu-vulkan-native")
	proc.SetReport(cfg.Chronon.Report)
	proc.SetHardwareEncoder(cfg.Chronon.HardwareEncoder)
	proc.SetEncodePreset(cfg.Chronon.EncodePreset)
	log.Printf("chronon report telemetry: %t, strict_native_backend: %t, encode_preset: %q", cfg.Chronon.Report, cfg.Chronon.StrictNativeBackend || cfg.Chronon.Profile == "gpu-vulkan-native", cfg.Chronon.EncodePreset)

	// 3a. Worker-local artifact ledger mirror (the "DB artifact" step): SQLite,
	// pure Go so the CGO_ENABLED=0 worker image keeps building. The mirror is
	// diagnostic: a failed write is logged and flagged on the artifact's
	// metrics (mirror_failure=1), never fatal — the central queue PostgreSQL
	// row is authoritative for the artifact.
	if cfg.ArtifactDB.Path != "" {
		recorder, err := artifactdb.NewSQLite(cfg.ArtifactDB.Path)
		if err != nil {
			log.Fatalf("artifact_db: %v", err)
		}
		proc.SetArtifactRecorder(recorder)
		log.Printf("artifact_db: ledger enabled at %q", cfg.ArtifactDB.Path)
	}

	// 3b. Google Drive publication (decoupled from rendering).
	var publisher drive.Publisher
	if cfg.Drive.Enabled {
		switch cfg.Drive.Mode {
		case "mock":
			publisher = drive.NewMock(cfg.Drive.MockDir, cfg.Drive.MockFailFirst)
			log.Printf("drive: mock publisher (fail_first=%d, dir=%q)", cfg.Drive.MockFailFirst, cfg.Drive.MockDir)
		case "oauth":
			pub, err := drive.NewGoogleOAuth(ctx, cfg.Drive.CredentialsFile, cfg.Drive.TokenFile, cfg.Drive.ParentFolderID)
			if err != nil {
				log.Fatalf("drive: %v", err)
			}
			publisher = pub
			log.Printf("drive: oauth publisher (folder=%q)", cfg.Drive.ParentFolderID)
		default:
			pub, err := drive.NewGoogle(ctx, cfg.Drive.CredentialsFile, cfg.Drive.ParentFolderID)
			if err != nil {
				log.Fatalf("drive: %v", err)
			}
			publisher = pub
			log.Printf("drive: google publisher (folder=%q)", cfg.Drive.ParentFolderID)
		}
	}
	if publisher != nil {
		proc.SetPublisher(publisher)
	}

	var parentFinalizer *processor.ParentFinalizer
	if assembler != nil {
		parentFinalizer = processor.NewParentFinalizer(
			queueClient, store, assembler, publisher, cfg.Worker.ID,
			filepath.Join(cfg.Workspace.Root, "parents"),
		)
		log.Printf("parent finalizer: enabled (Chronon assembler, output=%q)", filepath.Join(cfg.Workspace.Root, "parents"))
	}

	// 4. READY: expose health.
	healthInfo := health.Info{
		Worker:        cfg.Worker.ID,
		RenderingGen:  version.RenderingGen,
		Chronon:       chrononVersion,
		OverlaySchema: version.OverlaySchema,
		Backend:       cfg.Chronon.Backend,
		Status:        "ready",
	}
	healthServer := health.NewServer(cfg.Health.Addr, healthInfo)
	// Live render progress: the tracker receives every frame milestone the
	// renderer prints; /progress exposes it locally and the pusher relays a
	// throttled snapshot to the queue so GET /jobs/{id} shows real position
	// instead of an opaque RUNNING/0% for the whole render.
	progressTracker := chronon.NewProgressTracker()
	proc.SetProgressTracker(progressTracker)
	healthServer.SetProgressFunc(progressTracker.Current)
	go func() {
		progresspush.New(queueClient, progressTracker, progresspush.DefaultInterval).Run(ctx)
	}()
	go func() {
		if err := healthServer.Run(ctx); err != nil {
			log.Fatalf("health: %v", err)
		}
	}()

	// 4b. Register worker in queue liveness registry now that all dependencies
	// and health checks are up and verified.
	hostname, _ := os.Hostname()
	if err := queueClient.Register(ctx, queue.Worker{
		ID:                   cfg.Worker.ID,
		Hostname:             hostname,
		Status:               queue.WorkerStatusReady,
		RenderingGenVersion:  version.RenderingGen,
		ChrononVersion:       chrononVersion,
		OverlaySchemaVersion: version.OverlaySchema,
		GPUBackend:           gpuInfo.Backend,
		GPUDevice:            fmt.Sprintf("%d", gpuInfo.Device),
		GPUDriver:            gpuInfo.Driver,
		StartedAt:            time.Now().UTC(),
	}); err != nil {
		log.Fatalf("queue worker registration: %v", err)
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := queueClient.Heartbeat(ctx); err != nil {
					log.Printf("queue worker heartbeat: %v", err)
				}
			}
		}
	}()

	numWorkers := cfg.Worker.PipelineWorkers
	if numWorkers < 1 {
		numWorkers = 1
	}

	log.Printf("worker %s ready: renderinggen=%s chronon=%s schema=%d pipeline_workers=%d gpu_lanes=%d",
		cfg.Worker.ID, version.RenderingGen, chrononVersion, version.OverlaySchema, numWorkers, gpuLanes)

	// 5. Run the three-stage pipeline: CPU preparation feeds GPU lanes,
	// and CPU post-processing (probe, hash, store, publish) drains behind them.
	prepCh := make(chan *preppedJob, numWorkers*2)
	doneCh := make(chan renderOutcome, numWorkers*2)
	var workers sync.WaitGroup
	// Prep pool: claim + validate + compile + materialize (CPU/IO bound).
	for i := 0; i < numWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPrepPool(ctx, queueClient, proc, prepCh)
		}()
	}
	// GPU lanes: parallel Chronon render sessions.
	for i := 0; i < gpuLanes; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runGPULane(ctx, queueClient, proc, prepCh, doneCh)
		}()
	}
	// Post pool: finalize (probe/hash/store) + Drive publication (CPU/IO).
	for i := 0; i < numWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPostPool(ctx, queueClient, proc, parentFinalizer, doneCh)
		}()
	}
	workers.Wait()
	log.Println("shutting down")
}

// preppedJob is a claimed job whose CPU preparation succeeded; it is ready
// for the GPU lane. The workspace transfers ownership to the GPU lane.
type preppedJob struct {
	job      *queue.Job
	prepared *processor.PreparedJob
}

// renderOutcome carries a GPU-completed job to the post pool.
type renderOutcome struct {
	job      *queue.Job
	prepared *processor.PreparedJob
	err      error
}

// runPrepPool claims jobs and runs their CPU-bound preparation. Prepare-only
// jobs complete here without touching the GPU lane. A rendered job observed
// by the prep pool (worker restart / stage hand-off) is forwarded straight to
// the post pool: its artifact is already durable and must never re-render.
func runPrepPool(ctx context.Context, q *queue.Client, proc *processor.Processor, prepCh chan<- *preppedJob) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Long-poll claim: the server wakes this request as soon as a job may
		// be claimable (submit/rendered/fail/expiry). The atomic claim remains
		// unchanged (SKIP LOCKED on the DB side), so the wait never assigns
		// work, it only removes the empty-queue sleep between renders.
		job, err := q.ClaimPendingWait(ctx, 25*time.Second)
		if err != nil {
			log.Printf("prep claim: %v", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if job == nil {
			continue
		}

		// Publication retry of a rendered job: never render again.
		if job.Artifact != nil {
			log.Printf("job %s prep received durable artifact; switching to publication", job.ID)
			artifact := *job.Artifact
			artifact.Metrics = nil
			published, pubErr := proc.Publish(ctx, job.ID, artifact)
			if pubErr != nil {
				reportFailure(ctx, q, job, pubErr)
				continue
			}
			reportComplete(ctx, q, job.ID, published)
			continue
		}

		// Prepare-only jobs (overlay.prepare warm-up) finish here.
		if job.JobType == queue.JobTypeOverlayPrepare {
			artifact, prepErr := withLease(ctx, job, q, func(jobCtx context.Context) (queue.Artifact, error) {
				return proc.Prepare(jobCtx, job)
			})
			if prepErr != nil {
				reportFailure(ctx, q, job, prepErr)
				continue
			}
			reportComplete(ctx, q, job.ID, artifact)
			continue
		}

		// CPU preparation for a render job. The lease covers prep + GPU +
		// hand-off to the post pool; renewals run inside withLease.
		var prepared *processor.PreparedJob
		prepErr := withLeaseVoid(ctx, job, q, func(jobCtx context.Context) error {
			var err error
			prepared, err = proc.PrepareJob(jobCtx, job)
			return err
		})
		if prepErr != nil {
			reportFailure(ctx, q, job, prepErr)
			continue
		}
		select {
		case prepCh <- &preppedJob{job: job, prepared: prepared}:
		case <-ctx.Done():
			_ = prepared.Workspace.Cleanup()
			return
		}
	}
}

// runGPULane is the only stage that invokes Chronon. One goroutine runs per
// configured GPU lane (cfg.Worker.GPULanes); the renderer itself is wrapped in
// chronon.LimitConcurrency(renderer, gpuLanes), so up to gpuLanes Chronon
// sessions — and therefore encoder sessions — run concurrently, matching the
// NVENC multi-session baseline. While Chronon works here, the prep pool
// prepares the next job and the post pool finalizes the previous one.
func runGPULane(ctx context.Context, q *queue.Client, proc *processor.Processor, prepCh <-chan *preppedJob, doneCh chan<- renderOutcome) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-prepCh:
			// Chronon is the long-running stage. Keep the queue lease alive
			// while it renders; renewing only during prepare/post would let a
			// normal software render expire and be claimed a second time.
			//
			// A running render may write nothing to its workspace for longer
			// than the stale-sweeper horizon (1h), so the workspace liveness
			// marker is refreshed here for the whole GPU stage: without it the
			// sweeper could RemoveAll a live render's directory.
			renderDone := make(chan struct{})
			go func() {
				ticker := time.NewTicker(10 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-renderDone:
						return
					case <-ticker.C:
						if err := p.prepared.Workspace.WriteLease(time.Now().Add(2 * time.Hour)); err != nil {
							log.Printf("job %s: refresh workspace lease marker: %v", p.job.ID, err)
						}
					}
				}
			}()
			gpuErr := withLeaseVoid(ctx, p.job, q, func(jobCtx context.Context) error {
				return proc.RunGPU(jobCtx, p.prepared)
			})
			close(renderDone)
			// Duty-cycle telemetry: record this render's end so the next job's
			// gpu_gap_us metric measures the true dead time between renders.
			processor.PutGPURenderEnd(time.Now())
			select {
			case doneCh <- renderOutcome{job: p.job, prepared: p.prepared, err: gpuErr}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// runPostPool finalizes GPU-completed jobs (probe, hash, store, ledger) and
// publishes them. Workspace cleanup always runs here: it is the last owner.
func runPostPool(ctx context.Context, q *queue.Client, proc *processor.Processor, parentFinalizer *processor.ParentFinalizer, doneCh <-chan renderOutcome) {
	keepWorkspaces := os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") == "1"
	for {
		select {
		case <-ctx.Done():
			return
		case out := <-doneCh:
			job, prepared := out.job, out.prepared
			// The post pool is the last owner of the workspace, so cleanup runs
			// at the end of THIS iteration — never as a deferred call inside
			// the loop, which would postpone it until pool shutdown and let
			// every finished job's workspace accumulate (jobs root is often
			// /dev/shm, i.e. RAM).
			cleanup := func() {
				if keepWorkspaces {
					return
				}
				if err := prepared.Workspace.Cleanup(); err != nil {
					log.Printf("job %s: workspace cleanup: %v", job.ID, err)
				}
			}
			if out.err != nil {
				reportFailure(ctx, q, job, out.err)
				cleanup()
				continue
			}
			var artifact queue.Artifact
			err := withLeaseVoid(ctx, job, q, func(jobCtx context.Context) error {
				var finalizeErr error
				artifact, finalizeErr = proc.FinalizeJob(jobCtx, prepared)
				if finalizeErr != nil {
					return finalizeErr
				}
				// Rendering stops as soon as the artifact is durable in object
				// storage; Drive publication is part of this pool, not the GPU
				// lane's critical path.
				artifact, finalizeErr = proc.Publish(jobCtx, job.ID, artifact)
				return finalizeErr
			})
			if err != nil {
				reportFailure(ctx, q, job, err)
				cleanup()
				continue
			}
			log.Printf("job %s artifact: storage_key=%q sha256=%q size=%d copy_eligible=%t backend=%q frames=%d %dx%d",
				job.ID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes,
				artifact.CopyEligible, artifact.Backend, artifact.FrameCount, artifact.Width, artifact.Height)
			reportComplete(ctx, q, job.ID, artifact)
			if parentFinalizer != nil && job.ParentJobID != "" {
				tryFinalizeParent(ctx, q, parentFinalizer, job.ParentJobID)
			}
			// Cleanup is intentionally after Complete/parent-finalize: the
			// queue is the durable record and parent assembly reads the
			// children's artifacts from the object store/L2, never from the
			// child workspace.
			cleanup()
		}
	}
}

// tryFinalizeParent is intentionally best-effort: a child completion can be
// observed before its siblings, so an incomplete parent is normal. The
// finalizer itself performs the second children read and atomic claim, which
// makes concurrent attempts and worker restarts safe.
func tryFinalizeParent(ctx context.Context, q *queue.Client, finalizer *processor.ParentFinalizer, parentID string) {
	children, err := q.Children(ctx, parentID)
	if err != nil || len(children) == 0 {
		if err != nil {
			log.Printf("parent %s inspect children: %v", parentID, err)
		}
		return
	}
	first, last := children[0], children[len(children)-1]
	if first == nil || last == nil || first.FrameRange == nil || last.FrameRange == nil {
		return
	}
	finalized, artifact, err := finalizer.Finalize(ctx, parentID, int64(len(children)), first.FrameRange.Start, last.FrameRange.End)
	if err != nil {
		// Incomplete children and a competing finalizer are expected during the
		// normal fan-in; the queue remains the source of truth for retry.
		log.Printf("parent %s not finalized yet: %v", parentID, err)
		return
	}
	if finalized {
		log.Printf("parent %s finalized: storage_key=%q sha256=%q size=%d", parentID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes)
	}
}

// reportFailure applies the queue transition rules: a failure while the job
// still holds a durable artifact is a publication failure (retry re-publishes
// only); otherwise it is a render failure (retry re-renders).
func reportFailure(ctx context.Context, q *queue.Client, job *queue.Job, err error) {
	log.Printf("job %s failed: %v", job.ID, err)
	if job.Artifact != nil {
		if reportErr := q.Rendered(ctx, job.ID, err.Error(), *job.Artifact); reportErr != nil {
			log.Printf("job %s report rendered: %v", job.ID, reportErr)
		}
		return
	}
	if reportErr := q.Fail(ctx, job.ID, err.Error()); reportErr != nil {
		log.Printf("job %s report fail: %v", job.ID, reportErr)
	}
}

// reportComplete marks a job completed on the queue.
func reportComplete(ctx context.Context, q *queue.Client, id string, artifact queue.Artifact) {
	if err := q.Complete(ctx, id, artifact); err != nil {
		log.Printf("job %s report complete: %v", id, err)
	}
}

// sleepCtx sleeps for d or until ctx is cancelled; reports whether the sleep
// completed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// withLease runs fn while renewing the job's lease in the background.
// If the lease cannot be renewed (e.g. it expired and the job was requeued to
// another worker), the job context is cancelled so the work aborts instead of
// double-processing.
//
// A transient renewal failure (network blip, queue restart) must not abort a
// render that took minutes: each renewal attempt is retried with backoff, and
// the job only aborts when the queue definitively reports the lease as lost
// (a 409-class conflict from the queue) or every retry has been exhausted.
func withLeaseVoid(ctx context.Context, job *queue.Job, q *queue.Client, fn func(context.Context) error) error {
	if job.Lease <= 0 {
		return fn(ctx)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := job.Lease / 2
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if renewWithRetry(jobCtx, job.ID, q, interval) {
					continue
				}
				log.Printf("job %s: lease renew failed permanently, aborting", job.ID)
				cancel()
				return
			}
		}
	}()
	return fn(jobCtx)
}

// renewWithRetry attempts one lease renewal with bounded backoff retries.
// It reports whether the lease is still held: true after a successful renew,
// false when the queue reports the job is no longer owned by this worker
// (permanent — the job was requeued elsewhere) or the retry budget is spent.
// Cancellation of ctx always reports false immediately.
func renewWithRetry(ctx context.Context, jobID string, q *queue.Client, interval time.Duration) bool {
	const maxAttempts = 3
	backoff := 2 * time.Second
	for attempt := 1; ; attempt++ {
		err := q.Renew(ctx, jobID)
		if err == nil {
			return true
		}
		// A conflict means the lease is definitively gone (expired and
		// requeued, completed, or owned by another worker): no retry helps.
		if errors.Is(err, queue.RenewConflictError) {
			return false
		}
		if attempt >= maxAttempts || ctx.Err() != nil {
			return false
		}
		log.Printf("job %s: lease renew attempt %d/%d failed, retrying: %v", jobID, attempt, maxAttempts, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff >= interval {
			backoff = interval
		}
	}
}

// withLease is withLeaseVoid for functions returning an artifact.
func withLease(ctx context.Context, job *queue.Job, q *queue.Client, fn func(context.Context) (queue.Artifact, error)) (queue.Artifact, error) {
	var artifact queue.Artifact
	err := withLeaseVoid(ctx, job, q, func(jobCtx context.Context) error {
		var err error
		artifact, err = fn(jobCtx)
		return err
	})
	return artifact, err
}
