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
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/gpu"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/health"
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

	// 2. Verify Chronon.
	chrononClient := &chronon.Client{Home: cfg.Chronon.Home, Backend: cfg.Chronon.Backend}
	if err := chrononClient.Verify(); err != nil {
		log.Fatalf("chronon: %v", err)
	}

	// 3. Connect queue + storage.
	queueClient := queue.New(cfg.Queue.Endpoint, cfg.Worker.ID)
	store := storage.New(cfg.ArtifactStore.Endpoint, cfg.ArtifactStore.LocalCacheDir)

	// 4. READY: expose health.
	healthInfo := health.Info{
		Worker:        cfg.Worker.ID,
		RenderingGen:  version.RenderingGen,
		Chronon:       chrononClient.Version(),
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
		cfg.Worker.ID, version.RenderingGen, chrononClient.Version(), version.OverlaySchema)

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

		if err := process(ctx, job, store, chrononClient); err != nil {
			log.Printf("job %s failed: %v", job.ID, err)
			if rerr := queueClient.Fail(ctx, job.ID, err.Error()); rerr != nil {
				log.Printf("report fail: %v", rerr)
			}
			continue
		}

		if err := queueClient.Complete(ctx, job.ID, queue.Result{}); err != nil {
			log.Printf("report complete: %v", err)
		}
	}
}

func process(ctx context.Context, job *queue.Job, store *storage.Client, chrononClient *chronon.Client) error {
	// Resolve assets from L2/L3, then render the overlay spec.
	for _, a := range job.Assets {
		if _, err := store.Get(ctx, a.Hash); err != nil {
			return err
		}
	}
	return chrononClient.Render(ctx, string(job.OverlaySpec), "/var/lib/renderinggen/out")
}
