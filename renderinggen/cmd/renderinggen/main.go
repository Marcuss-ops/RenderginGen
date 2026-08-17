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
	"os/signal"
	"syscall"
	"time"

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

	// 5. Claim loop.
	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			return
		default:
		}

		job, err := queueClient.Claim(ctx)
		if err != nil {
			log.Printf("claim: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if job == nil {
			// Queue empty: back off before polling again.
			time.Sleep(2 * time.Second)
			continue
		}

		artifact, renderErr, publishErr := processJob(ctx, job, queueClient, proc)
		switch {
		case renderErr != nil:
			log.Printf("job %s failed to render: %v", job.ID, renderErr)
			if rerr := queueClient.Fail(ctx, job.ID, renderErr.Error()); rerr != nil {
				log.Printf("report fail: %v", rerr)
			}
		case publishErr != nil:
			log.Printf("job %s rendered but publication failed: %v", job.ID, publishErr)
			if rerr := queueClient.Rendered(ctx, job.ID, publishErr.Error(), artifact); rerr != nil {
				log.Printf("report rendered: %v", rerr)
			}
		default:
			if err := queueClient.Complete(ctx, job.ID, artifact); err != nil {
				log.Printf("report complete: %v", err)
			}
		}
	}
}

// processJob processes a claimed job while renewing its lease in the
// background. If the lease cannot be renewed (e.g. it expired and the job was
// requeued to another worker), the job context is cancelled so the render
// aborts instead of double-processing. On success it returns the published
// artifact, which the caller reports via Complete.
func processJob(ctx context.Context, job *queue.Job, q *queue.Client, proc *processor.Processor) (queue.Artifact, error, error) {
	if job.Lease <= 0 {
		return runJob(ctx, job, proc)
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

	return runJob(jobCtx, job, proc)
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
