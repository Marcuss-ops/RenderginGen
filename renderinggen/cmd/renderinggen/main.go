// Command renderinggen is the RenderingGen GPU worker.
//
// Startup sequence:
//
//	load config -> detect GPU -> verify Chronon -> connect queue -> READY -> claim jobs
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	// persistent Chronon3d daemon over IPC.
	var renderer chronon.Renderer
	chrononVersion := "unknown"
	if cfg.Chronon.Mode == "ipc" {
		renderer = chronon.NewIPCClient(cfg.Chronon.SocketPath)
	} else {
		cli := &chronon.Client{Home: cfg.Chronon.Home}
		if err := cli.Verify(); err != nil {
			log.Fatalf("chronon: %v", err)
		}
		renderer = cli
		chrononVersion = cli.Version()
	}

	// 3. Connect queue + storage.
	queueClient := queue.New(cfg.Queue.Endpoint, cfg.Worker.ID)
	store := storage.New(
		storage.NewHTTP(cfg.ArtifactStore.Endpoint),
		storage.Options{
			L1MaxBytes: 256 << 20, // 256 MiB VRAM cache
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

	log.Printf("worker %s ready: renderinggen=%s chronon=%s schema=%d",
		cfg.Worker.ID, version.RenderingGen, chrononVersion, version.OverlaySchema)

	// 5. Run independent render and publication stages. Rendering stops as soon
	// as the artifact is durable in object storage; Drive publication no longer
	// occupies the GPU worker's critical path.
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		runStageLoop(ctx, queueClient, proc, stageRender)
	}()
	go func() {
		defer workers.Done()
		runStageLoop(ctx, queueClient, proc, stagePublish)
	}()
	workers.Wait()
	log.Println("shutting down")
}

type workerStage uint8

const (
	stageRender workerStage = iota
	stagePublish
)

// runStageLoop owns one queue stage. Both stages use the same atomic claim API:
// pending jobs enter the render stage, while rendered jobs enter publication.
func runStageLoop(ctx context.Context, q *queue.Client, proc *processor.Processor, stage workerStage) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var job *queue.Job
		var err error
		if stage == stagePublish {
			job, err = q.ClaimRendered(ctx)
		} else {
			job, err = q.ClaimPending(ctx)
		}
		if err != nil {
			log.Printf("%s claim: %v", stageName(stage), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		artifact, err := processStageJob(ctx, job, q, proc, stage)
		if err != nil {
			log.Printf("job %s %s failed: %v", job.ID, stageName(stage), err)
			if stage == stagePublish && job.Artifact != nil {
				if reportErr := q.Rendered(ctx, job.ID, err.Error(), *job.Artifact); reportErr != nil {
					log.Printf("job %s report rendered: %v", job.ID, reportErr)
				}
			} else if reportErr := q.Fail(ctx, job.ID, err.Error()); reportErr != nil {
				log.Printf("job %s report fail: %v", job.ID, reportErr)
			}
			continue
		}

		if stage == stageRender && job.JobType != queue.JobTypeOverlayPrepare && job.Artifact == nil {
			log.Printf("job %s render artifact: storage_key=%q sha256=%q size=%d copy_eligible=%t backend=%q frames=%d %dx%d",
				job.ID, artifact.StorageKey, artifact.ArtifactHash, artifact.SizeBytes,
				artifact.CopyEligible, artifact.Backend, artifact.FrameCount, artifact.Width, artifact.Height)
			if err := q.Rendered(ctx, job.ID, "render complete; awaiting publication", artifact); err != nil {
				log.Printf("job %s report rendered: %v", job.ID, err)
			}
			continue
		}
		if err := q.Complete(ctx, job.ID, artifact); err != nil {
			log.Printf("job %s report complete: %v", job.ID, err)
		}
	}
}

func stageName(stage workerStage) string {
	if stage == stagePublish {
		return "publish"
	}
	return "render"
}

func processStageJob(ctx context.Context, job *queue.Job, q *queue.Client, proc *processor.Processor, stage workerStage) (queue.Artifact, error) {
	return withLease(ctx, job, q, func(jobCtx context.Context) (queue.Artifact, error) {
		// A rendered job can be observed by a render loop during a stage
		// hand-off (or after a worker restart). Its artifact is already durable;
		// never render it again. Continue with publication idempotently.
		if stage == stageRender && job.Artifact != nil {
			log.Printf("job %s render stage received durable artifact; switching to publication", job.ID)
			artifact := *job.Artifact
			artifact.Metrics = nil
			return proc.Publish(jobCtx, job.ID, artifact)
		}
		if stage == stagePublish {
			if job.Artifact == nil {
				return queue.Artifact{}, fmt.Errorf("rendered job has no artifact")
			}
			artifact := *job.Artifact
			artifact.Metrics = nil
			return proc.Publish(jobCtx, job.ID, artifact)
		}
		if job.JobType == queue.JobTypeOverlayPrepare {
			return proc.Prepare(jobCtx, job)
		}
		if job.Artifact != nil {
			return queue.Artifact{}, fmt.Errorf("render stage received already-rendered job")
		}
		return proc.Render(jobCtx, job)
	})
}

// processJob processes a claimed job while renewing its lease in the
// background. If the lease cannot be renewed (e.g. it expired and the job was
// requeued to another worker), the job context is cancelled so the render
// aborts instead of double-processing. On success it returns the published
// artifact, which the caller reports via Complete.
func processJob(ctx context.Context, job *queue.Job, q *queue.Client, proc *processor.Processor) (queue.Artifact, error, error) {
	var artifact queue.Artifact
	var renderErr, publishErr error
	artifact, err := withLease(ctx, job, q, func(jobCtx context.Context) (queue.Artifact, error) {
		var a queue.Artifact
		a, renderErr, publishErr = runJob(jobCtx, job, proc)
		return a, firstError(renderErr, publishErr)
	})
	if err != nil && renderErr == nil && publishErr == nil {
		renderErr = err
	}
	return artifact, renderErr, publishErr
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func withLease(ctx context.Context, job *queue.Job, q *queue.Client, fn func(context.Context) (queue.Artifact, error)) (queue.Artifact, error) {
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

// runJob runs the render + publication pipeline for a claimed job. It returns
// the artifact plus the render and publication errors separately, so the caller
// reports the right queue transition: Fail on a render error (retry re-renders),
// Rendered on a publication error (retry only re-publishes), Complete otherwise.
func runJob(ctx context.Context, job *queue.Job, proc *processor.Processor) (queue.Artifact, error, error) {
	if job.JobType == queue.JobTypeOverlayPrepare {
		a, err := proc.Prepare(ctx, job)
		return a, err, nil
	}
	if job.Artifact != nil {
		// Publication-only retry of a rendered job: never re-render.
		artifact := *job.Artifact
		artifact.Metrics = nil // fresh metrics for this publish attempt
		a, err := proc.Publish(ctx, job.ID, artifact)
		return a, nil, err
	}
	a, err := proc.Render(ctx, job)
	if err != nil {
		return queue.Artifact{}, err, nil
	}
	a, err = proc.Publish(ctx, job.ID, a)
	return a, nil, err
}
