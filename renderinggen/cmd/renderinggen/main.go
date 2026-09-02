// Command renderinggen is the RenderingGen GPU worker.
//
// Startup sequence:
//
//	load config -> detect GPU -> verify Chronon -> connect queue -> READY -> claim jobs
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
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
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/version"
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
		renderer = chronon.NewIPCClient(cfg.Chronon.SocketPath)
	} else {
		cli := &chronon.Client{Home: cfg.Chronon.Home}
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
	// CPU/I/O stages may execute concurrently, but the renderer is wrapped in
	// one context-aware lane so only one Chronon job can occupy the GPU at once.
	renderer = chronon.Serialize(renderer)

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
	proc.SetStrictNativeBackend(cfg.Chronon.Profile == "gpu-vulkan-native")
	proc.SetReport(cfg.Chronon.Report)
	proc.SetHardwareEncoder(cfg.Chronon.HardwareEncoder)
	log.Printf("chronon report telemetry: %t", cfg.Chronon.Report)

	// 3a. Artifact ledger (the "DB artifact" step): SQLite, pure Go so the
	// CGO_ENABLED=0 worker image keeps building. A failed ledger write fails
	// the job — the ledger is the source of truth for what was produced.
	if cfg.ArtifactDB.Path != "" {
		recorder, err := artifactdb.NewSQLite(cfg.ArtifactDB.Path)
		if err != nil {
			log.Fatalf("artifact_db: %v", err)
		}
		proc.SetArtifactRecorder(recorder)
		log.Printf("artifact_db: ledger enabled at %q", cfg.ArtifactDB.Path)
	}

	// 3b. Google Drive publication (decoupled from rendering).
	if cfg.Drive.Enabled {
		switch cfg.Drive.Mode {
		case "mock":
			proc.SetPublisher(drive.NewMock(cfg.Drive.MockDir, cfg.Drive.MockFailFirst))
			log.Printf("drive: mock publisher (fail_first=%d, dir=%q)", cfg.Drive.MockFailFirst, cfg.Drive.MockDir)
		case "oauth":
			pub, err := drive.NewGoogleOAuth(ctx, cfg.Drive.CredentialsFile, cfg.Drive.TokenFile, cfg.Drive.ParentFolderID)
			if err != nil {
				log.Fatalf("drive: %v", err)
			}
			proc.SetPublisher(pub)
			log.Printf("drive: oauth publisher (folder=%q)", cfg.Drive.ParentFolderID)
		default:
			pub, err := drive.NewGoogle(ctx, cfg.Drive.CredentialsFile, cfg.Drive.ParentFolderID)
			if err != nil {
				log.Fatalf("drive: %v", err)
			}
			proc.SetPublisher(pub)
			log.Printf("drive: google publisher (folder=%q)", cfg.Drive.ParentFolderID)
		}
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
	go func() {
		if err := healthServer.Run(ctx); err != nil {
			log.Fatalf("health: %v", err)
		}
	}()

	log.Printf("worker %s ready: renderinggen=%s chronon=%s schema=%d pipeline_workers=%d gpu_lanes=1",
		cfg.Worker.ID, version.RenderingGen, chrononVersion, version.OverlaySchema, cfg.Worker.PipelineWorkers)

	// 5. Run the three-stage pipeline: CPU preparation feeds a single GPU
	// lane, and CPU post-processing (probe, hash, store, publish) drains
	// behind it. While Chronon renders job N, the prep pool materializes
	// job N+1 and the post pool finalizes job N-1. The queue remains the
	// source of truth and leases prevent duplicate claims.
	prepCh := make(chan *preppedJob, 2)
	doneCh := make(chan renderOutcome, 2)
	var workers sync.WaitGroup
	// Prep pool: claim + validate + compile + materialize (CPU/IO bound).
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPrepPool(ctx, queueClient, proc, prepCh)
		}()
	}
	// GPU lane: the only stage that touches Chronon; strictly one at a time.
	workers.Add(1)
	go func() {
		defer workers.Done()
		runGPULane(ctx, proc, prepCh, doneCh)
	}()
	// Post pool: finalize (probe/hash/store) + Drive publication (CPU/IO).
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPostPool(ctx, queueClient, proc, doneCh)
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
		go func(job *queue.Job) {
			var prepared *processor.PreparedJob
			prepErr := withLeaseVoid(ctx, job, q, func(jobCtx context.Context) error {
				var err error
				prepared, err = proc.PrepareJob(jobCtx, job)
				return err
			})
			if prepErr != nil {
				reportFailure(ctx, q, job, prepErr)
				return
			}
			select {
			case prepCh <- &preppedJob{job: job, prepared: prepared}:
			case <-ctx.Done():
				_ = prepared.Workspace.Cleanup()
			}
		}(job)
	}
}

// runGPULane is the only stage that invokes Chronon, strictly one render at a
// time so the GPU never runs concurrent encoder sessions. While Chronon works
// here, the prep pool prepares the next job and the post pool finalizes the
// previous one.
func runGPULane(ctx context.Context, proc *processor.Processor, prepCh <-chan *preppedJob, doneCh chan<- renderOutcome) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-prepCh:
			gpuErr := proc.RunGPU(ctx, p.prepared)
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
func runPostPool(ctx context.Context, q *queue.Client, proc *processor.Processor, doneCh <-chan renderOutcome) {
	for {
		select {
		case <-ctx.Done():
			return
		case out := <-doneCh:
			job, prepared := out.job, out.prepared
			if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
				defer func(id string) {
					if err := prepared.Workspace.Cleanup(); err != nil {
						log.Printf("job %s: workspace cleanup: %v", id, err)
					}
				}(job.ID)
			}
			if out.err != nil {
				reportFailure(ctx, q, job, out.err)
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
				continue
			}
			log.Printf("job %s artifact: storage_key=%q sha256=%q size=%d copy_eligible=%t backend=%q frames=%d %dx%d",
				job.ID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes,
				artifact.CopyEligible, artifact.Backend, artifact.FrameCount, artifact.Width, artifact.Height)
			reportComplete(ctx, q, job.ID, artifact)
		}
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

// withLease runs fn while renewing the job's lease in the background. If the
// lease cannot be renewed (e.g. it expired and the job was requeued to another
// worker), the job context is cancelled so the work aborts instead of
// double-processing.
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
				if err := q.Renew(jobCtx, job.ID); err != nil {
					log.Printf("job %s: lease renew failed, aborting: %v", job.ID, err)
					cancel()
					return
				}
			}
		}
	}()
	return fn(jobCtx)
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
