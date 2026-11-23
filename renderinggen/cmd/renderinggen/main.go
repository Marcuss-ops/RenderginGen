// Command renderinggen is the RenderingGen GPU worker.
//
// Startup sequence:
//
//	load config -> detect GPU -> verify Chronon -> connect queue -> READY -> claim jobs
//
// File layout: main.go only sequences the phases. Config load, renderers,
// pipeline construction, health/registration and the three-stage pools live in
// bootstrap.go (pool implementations in worker_pools.go, lease renewal in
// lease.go).
package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/buildinfo"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// workerRegistrar is the queue surface the shutdown path needs. Keeping it an
// interface makes the drain registration testable without a live queue.
type workerRegistrar interface {
	Register(context.Context, queue.Worker) error
}

// markWorkerDraining re-registers the worker with the draining lifecycle
// status. The whole registration is sent: the queue upsert overwrites every
// column, so a partial payload would erase the worker identity.
func markWorkerDraining(ctx context.Context, registrar workerRegistrar, registration queue.Worker) error {
	registration.Status = queue.WorkerStatusDraining
	return registrar.Register(ctx, registration)
}

func main() {
	configPath := flag.String("config", "/etc/renderinggen/config.yaml", "path to config file")
	flag.Parse()

	// Publish the process identity before anything can fail: /health then
	// answers "which binary, which commit, which config is this worker running?"
	// which previously required auditing the unit file, the `ps` line and the
	// config backups under /etc by hand.
	buildinfo.SetRuntime(buildinfo.RuntimeInfo{
		ConfigPath: *configPath,
		Mode:       "renderinggen-worker",
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := loadWorkerConfig(ctx, *configPath)
	gpuInfo := detectGPU(cfg)
	renderers := buildRenderers(cfg)
	pipeline := buildWorkerPipeline(ctx, cfg, renderers)
	registration := startWorkerSurface(ctx, cfg, gpuInfo, renderers, pipeline)

	runWorkerPools(ctx, cfg, renderers, pipeline, registration)
}
