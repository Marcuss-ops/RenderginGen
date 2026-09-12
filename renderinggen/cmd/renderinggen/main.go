// Command renderinggen is the RenderingGen GPU worker.
//
// Startup sequence:
//
//	load config -> detect GPU -> verify Chronon -> connect queue -> READY -> claim jobs
//
// File layout: main.go wires startup and the three-stage pools (whose
// implementations live in worker_pools.go, with lease renewal in lease.go).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/gpu"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/health"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/progresspush"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/version"
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
	// Reap workspaces left behind by a crashed worker run (see
	// startWorkspaceCleanup for the full rationale).
	go startWorkspaceCleanup(ctx, cfg.Workspace.Root)

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
	var assetPrefetcher chronon.AssetPrefetcher
	chrononVersion := "unknown"
	{
		probe := &chronon.Client{Home: cfg.Chronon.Home, BinaryPath: cfg.Chronon.Binary}
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
		assetPrefetcher = ipc
	} else {
		// The Client carries the semantic knobs its Verify() handshake
		// validates against: a worker configured for the native GPU hot path
		// must refuse a Chronon binary compiled without that path (fail-fast
		// before READY — never after accepting jobs).
		cli := &chronon.Client{
			Home:                cfg.Chronon.Home,
			BinaryPath:          cfg.Chronon.Binary,
			Backend:             cfg.Chronon.Backend,
			StrictNativeBackend: cfg.Chronon.StrictNative(),
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
	proc.SetAssetPrefetcher(assetPrefetcher)
	proc.SetNativeOutputProfiles(cfg.Chronon.NativeOutputProfiles)
	proc.SetStrictNativeBackend(cfg.Chronon.StrictNative())
	proc.SetReport(cfg.Chronon.Report)
	proc.SetHardwareEncoder(cfg.Chronon.HardwareEncoder)
	proc.SetEncodePreset(cfg.Chronon.EncodePreset)
	proc.SetPipePixFmt(cfg.Chronon.PipePixFmt)
	log.Printf("chronon report telemetry: %t, strict_native_backend: %t, encode_preset: %q, pipe_pixfmt: %q", cfg.Chronon.Report, cfg.Chronon.StrictNative(), cfg.Chronon.EncodePreset, cfg.Chronon.PipePixFmt)

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
	// Canonical publication authority: queue-served render segments resolve
	// to object-store-only (the submitter owns Drive delivery), so a Drive
	// capability here is never a silent second upload of master-routed clips.
	log.Printf("publication: queue render jobs resolve to %s (drive capability=%t); parent assembly and declared jobs keep the configured publisher",
		processor.ResolvePublicationPolicy("", queue.JobTypeRenderSegment), cfg.Drive.Enabled)

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
	// Fail-open degradations (workspace cleanup failures today) must be visible
	// on the worker's own surface, not only in the log stream.
	healthServer.SetDegradationFunc(proc.Degradations)
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
	// Surface sustained queue disconnect on /health and in the logs instead of
	// logging-and-forgetting: a worker whose heartbeat cannot reach the queue
	// is not fully ready even while it may still be processing a claim.
	var heartbeatFailures atomic.Int64
	healthServer.SetQueueStatus(func() string {
		if heartbeatFailures.Load() >= degradedHeartbeatThreshold {
			return "degraded"
		}
		return "ready"
	})
	go runHeartbeatLoop(ctx, queueClient, &heartbeatFailures)

	numWorkers := cfg.Worker.PipelineWorkers
	if numWorkers < 1 {
		numWorkers = 1
	}

	log.Printf("worker %s ready: renderinggen=%s chronon=%s schema=%d pipeline_workers=%d gpu_lanes=%d",
		cfg.Worker.ID, version.RenderingGen, chrononVersion, version.OverlaySchema, numWorkers, gpuLanes)

	// 5. Run the three-stage pipeline: CPU preparation feeds GPU lanes,
	// and CPU post-processing (probe, hash, store, publish) drains behind them.
	// prepCh is rendezvous (unbuffered) so a prepared job never dwells in a
	// buffer without a lease renewer: the prep pool's withLease covers the
	// blocking send, guaranteeing continuous renewal until the GPU lane takes
	// ownership. A buffered channel would let renewal stop in dwell and expire
	// under backlog >10 min → requeue + double-render.
	prepCh := make(chan *preppedJob)
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
