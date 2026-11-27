package main

// Bootstrap for the renderinggen worker: config load, renderer/assembler
// wiring, processor construction, health surface and the three-stage pools.
// main() only sequences these phases; every phase keeps the initialization
// order and the log/exit contract it had inline.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/buildinfo"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/camerabake"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/gpu"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/health"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/processor"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/progresspush"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/version"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workermetrics"
)

// loadWorkerConfig loads configuration, installs logging and media binaries,
// then starts the crash-recovery sweeps. Everything a job can rely on is in
// place before the first claim.
func loadWorkerConfig(ctx context.Context, configPath string) *config.Config {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Logging before anything else can log. The level and format are
	// configuration (logging.level / logging.format), so a failed startup is
	// already a structured record instead of a bare line, and the per-job log
	// files the collector reads exist from the first claimed job. A bad value can
	// not reach here: config validates both spellings at load.
	if err := workerlog.Init(workerlog.Options{Level: cfg.Logging.Level, Format: cfg.Logging.Format}); err != nil {
		log.Fatalf("logging: %v", err)
	}

	// Install the configured certification tools once, before any render can
	// reach the probe/decode paths. Without this the media package falls back to
	// the bare "ffprobe"/"ffmpeg" PATH lookup, which is what the pre-settings
	// worker did unconditionally.
	media.Configure(media.Binaries{FFprobe: cfg.Media.FFprobeBinary, FFmpeg: cfg.Media.FFmpegBinary})

	// Geographic catalog: load and validate it at the composition root, before
	// the first claim. The loader deliberately does not search the working
	// directory for a sibling ChrononTemplate checkout (that made the worker
	// depend on where it was started and worked only on machines with the
	// source tree). A missing/partial catalog now stops the worker at startup
	// with an error naming CHRONONTEMPLATE_CATALOG instead of failing the first
	// fly-to map job minutes later.
	registry, err := geofeatures.DefaultRegistry()
	if err != nil {
		log.Fatalf("geofeatures catalog: %v", err)
	}
	log.Printf("geofeatures catalog loaded: %d features from CHRONONTEMPLATE_CATALOG", len(registry.Features()))

	// Reap workspaces left behind by a crashed worker run: without this the
	// jobs root (often /dev/shm, i.e. RAM) grows unboundedly. Active
	// workspaces carry a lease marker (written at PrepareJob, refreshed by
	// the GPU lane while rendering) and are skipped; anything older than one
	// hour without a valid marker is removed. Parent artifacts have their
	// own cleanup (see ParentFinalizer.Finalize).
	go startWorkspaceCleanup(ctx, cfg.Workspace.Root, cfg.Pipeline)
	// Bound the durable per-job log directory before any job can add to it (the
	// jobs root is often tmpfs, i.e. RAM). See pruneJobLogs.
	pruneJobLogs(cfg)
	return cfg
}

// detectGPU logs which device will render (or the reason it cannot) before the
// first job is claimed.
func detectGPU(cfg *config.Config) gpu.Info {
	gpuInfo := gpu.Detect(cfg.GPU.Device)
	if !gpuInfo.Present {
		// Name the reason: "no GPU" and "device index 2 does not exist" are
		// different operator problems, and the probe reports which one it is.
		log.Printf("WARN: no GPU detected at device %d (%s); rendering will fail", cfg.GPU.Device, gpuInfo.Reason)
		return gpuInfo
	}
	log.Printf("GPU detected: backend=%s driver=%s device=%d name=%q vram_mib=%d",
		gpuInfo.Backend, gpuInfo.Driver, gpuInfo.Device, gpuInfo.Name, gpuInfo.MemoryMiB)
	return gpuInfo
}

// rendererSet is the rendering surface the processor receives: the primary
// renderer (IPC spread or CLI), the serialized software lane, assembly and
// prefetch ports, plus the facts the rest of the bootstrap records.
type rendererSet struct {
	renderer       chronon.Renderer
	software       chronon.Renderer
	assembler      chronon.Assembler
	prefetcher     chronon.AssetPrefetcher
	capabilities   []string
	chrononVersion string
	gpuLanes       int
	sockets        []string
}

// buildRenderers selects the renderer backend: CLI subprocess (default) or the
// persistent Chronon3d daemon over IPC.
func buildRenderers(cfg *config.Config) rendererSet {
	set := rendererSet{
		chrononVersion: "unknown",
		gpuLanes:       cfg.Worker.GPULanes,
		sockets:        cfg.Chronon.RenderSockets(),
	}

	// The installed Chronon version must always be read from
	// cfg.Chronon.Home/VERSION regardless of mode: previously the IPC branch
	// kept chrononVersion at the literal "unknown" sentinel, so
	// render_artifacts recorded chronon_version='unknown' for every job even
	// when /opt/chronon3d/VERSION was present.
	probe := &chronon.Client{Home: cfg.Chronon.Home, BinaryPath: cfg.Chronon.Binary}
	// Verify only when we're about to invoke the CLI directly; the IPC branch
	// validates the daemon via NewIPCClient below. Read VERSION unconditionally
	// so the recorded version is never silently 'unknown'.
	if v := probe.Version(); v != "unknown" {
		set.chrononVersion = v
	}

	if cfg.Chronon.Mode == "ipc" {
		// Renders spread round-robin over every configured daemon socket
		// (chronon.socket_paths; a single socket_path keeps the historical
		// one-daemon shape), so gpu_lanes > 1 across N sockets is N daemons
		// rendering concurrently instead of one daemon queueing every lane.
		// Assembly and asset warm-up are serialized parent-side operations and
		// stay on the first socket — spreading them would buy nothing and would
		// make the parent's ordering depend on which daemon answered.
		spread, spreadErr := chronon.SpreadAcrossSocketsWithStallTimeout(set.sockets, cfg.Worker.GPULanes, cfg.Chronon.StallTimeout)
		if spreadErr != nil {
			log.Fatalf("chronon: %v", spreadErr)
		}
		ipc := chronon.NewIPCClient(set.sockets[0])
		ipc.SetStallTimeout(cfg.Chronon.StallTimeout)
		set.renderer = spread
		set.assembler = ipc
		set.prefetcher = ipc
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
			StallTimeout:        cfg.Chronon.StallTimeout,
		}
		if err := cli.Verify(); err != nil {
			log.Fatalf("chronon: %v", err)
		}
		set.renderer = cli
		if set.chrononVersion == "unknown" {
			// CLI mode with no VERSION file: prefer the value the in-process
			// client just read (covers the unlikely case where Home was empty
			// at the probe call but got populated here).
			set.chrononVersion = cli.Version()
		}
	}
	// Publish effect support from the same Chronon doctor catalog used by the
	// renderer. The queue's live capability endpoint is consumed by producers
	// during preflight; an empty inventory stays fail-closed.
	declared, err := probe.Capabilities(context.Background())
	if err != nil {
		log.Fatalf("chronon renderer capabilities: %v", err)
	}
	set.capabilities = declared.RendererCapabilities()
	// Advertise the GPU lane itself, not only the certified effect ids. The
	// queue refuses to hand a gpu_only job to a worker whose inventory cannot
	// prove a GPU, and a host with Vulkan but no certified effect would
	// otherwise advertise an empty inventory even though it can execute the
	// lane. The capability comes from the same doctor verdict as the effects.
	//
	// Two proofs are required: the host must have a GPU lane (doctor verdict)
	// AND this worker must actually be configured to serve it. A worker started
	// on the software profile is a CPU lane by operator choice; advertising
	// gpu would let the queue hand it gpu_only work it would then execute
	// without a GPU, which is exactly the silent downgrade the policy forbids.
	if declared.ProvidesGPULane() && !strings.EqualFold(cfg.Chronon.Backend, "software") {
		set.capabilities = append(set.capabilities, queue.CapabilityGPU)
	}

	// CPU/I/O stages execute concurrently, and the renderer is allowed up to
	// cfg.Worker.GPULanes concurrent GPU sessions (the config default of 2
	// matches the NVENC multi-session baseline on RTX A4000-class hosts).
	set.renderer = chronon.LimitConcurrency(set.renderer, set.gpuLanes)

	// A separate CLI renderer owns the libx264 lane for chain camera pans. It
	// stays serialized: software composition is CPU-bound, so running it
	// beside the GPU lanes would only steal their preparation cores.
	softwareCLI := &chronon.Client{
		Home:                cfg.Chronon.Home,
		BinaryPath:          cfg.Chronon.Binary,
		Backend:             "software",
		StrictNativeBackend: false,
		HardwareEncoder:     chronon.HardwareEncoderNone,
		StallTimeout:        cfg.Chronon.StallTimeout,
	}
	if err := softwareCLI.Verify(); err != nil {
		log.Printf("WARN: software CLI lane unavailable, chain pans keep the configured renderer: %v", err)
	} else {
		set.software = chronon.LimitConcurrency(softwareCLI, 1)
	}
	return set
}

// workerPipeline is everything the pools share: the queue client, the
// processor, parent assembly, and the observer feeds wired into /health.
type workerPipeline struct {
	queue           *queue.Client
	proc            *processor.Processor
	parentFinalizer *processor.ParentFinalizer
	progress        *chronon.ProgressTracker
	metrics         *workermetrics.Metrics
}

// buildWorkerPipeline connects queue, storage, artifact ledger, publisher and
// processor. Every collaborator is built BEFORE the processor, so the single
// Options literal is the worker's only configuration point — the historical
// arrangement spread 17 Set* calls across startup, where one omitted line was
// invisible until a job rendered with the wrong knob.
func buildWorkerPipeline(ctx context.Context, cfg *config.Config, renderers rendererSet) workerPipeline {
	queueClient := queue.New(cfg.Queue.Endpoint, cfg.Worker.ID)
	store := storage.New(
		storage.NewHTTPWithToken(cfg.ArtifactStore.Endpoint, cfg.ArtifactStore.Token),
		storage.Options{
			// Cache budgets are configuration: a host with a different RAM/NVMe
			// balance must be able to rebalance them without a rebuild.
			L1MaxBytes: cfg.ArtifactStore.L1MaxBytes,
			L2Dir:      cfg.ArtifactStore.LocalCacheDir,
			L2MaxBytes: cfg.ArtifactStore.L2MaxBytes,
		},
	)

	recorder := buildArtifactRecorder(cfg)
	publisher := buildPublisher(ctx, cfg)

	// Render progress tracker and Prometheus metrics: both are observers the
	// processor and the health surface forward into, so they exist before it
	// does and are handed to the processor through the same Options literal.
	progressTracker := chronon.NewProgressTracker()
	workerMetrics := workermetrics.New()
	processor.SetJobOutcomeHook(workerMetrics.OutcomeHook())

	// The processor owns the per-job pipeline (validate -> workspace ->
	// materialize -> plan.json -> render -> hash -> publish).
	// The catalog was validated by loadWorkerConfig before the first claim.
	// Re-read the cached registry (no second filesystem load) and inject it so
	// the compiler never resolves a package global mid-request.
	geoRegistry, err := geofeatures.DefaultRegistry()
	if err != nil {
		log.Fatalf("geofeatures catalog: %v", err)
	}

	proc := processor.NewWithOptions(
		cfg.Workspace.Root,
		cfg.Chronon.Backend,
		renderers.chrononVersion,
		cfg.ArtifactStore.Endpoint,
		store,
		renderers.renderer,
		processor.Options{
			GeoRegistry:               geoRegistry,
			AssetPrefetcher:           renderers.prefetcher,
			CameraBaker:               camerabake.FromEnv(),
			SoftwareRenderer:          renderers.software,
			NativeOutputProfiles:      cfg.Chronon.NativeOutputProfiles,
			AcceptedCodecConfigSHA256: cfg.Pipeline.NativeAVCCSHA256,
			StrictNativeBackend:       cfg.Chronon.StrictNative(),
			Report:                    cfg.Chronon.Report,
			HardwareEncoder:           cfg.Chronon.HardwareEncoder,
			EncoderBackend:            cfg.Chronon.EncoderBackend,
			EncodePreset:              cfg.Chronon.EncodePreset,
			PipePixFmt:                cfg.Chronon.PipePixFmt,
			NoDirtyRects:              cfg.Chronon.NoDirtyRects,
			WorkspaceLeaseTTL:         cfg.Pipeline.WorkspaceLeaseTTL,
			// Verification policy, deep visual validation and workspace
			// retention come from configuration, so the post pool (the
			// workspace's last owner) and the processor cannot be told
			// different things.
			ReceiptVerify:        cfg.Pipeline.ReceiptVerify,
			DeepVisualValidation: cfg.Pipeline.DeepVisualValidation,
			KeepWorkspace:        cfg.Pipeline.KeepWorkspace,
			Publisher:            publisher,
			ArtifactRecorder:     recorder,
			ProgressTracker:      progressTracker,
			PhaseHook:            workerMetrics.PhaseHook(),
			GPUGapHook:           workerMetrics.GPUGapHook(),
			LaneWaitHook:         workerMetrics.LaneWaitHook(),
		},
	)
	if len(cfg.Pipeline.NativeAVCCSHA256) > 0 {
		log.Printf("NVENC avcC recertification: %d allowlisted canonical parameter-set hash(es); matching artifacts skip the x264 re-encode", len(cfg.Pipeline.NativeAVCCSHA256))
	}
	log.Printf("chronon report telemetry: %t, strict_native_backend: %t, encode_preset: %q, pipe_pixfmt: %q", cfg.Chronon.Report, cfg.Chronon.StrictNative(), cfg.Chronon.EncodePreset, cfg.Chronon.PipePixFmt)
	// Canonical publication authority: queue-served render segments resolve
	// to object-store-only (the submitter owns Drive delivery), so a Drive
	// capability here is never a silent second upload of master-routed clips.
	log.Printf("publication: queue render jobs resolve to %s (drive capability=%t); parent assembly and declared jobs keep the configured publisher",
		processor.ResolvePublicationPolicy("", queue.JobTypeRenderSegment), cfg.Drive.Enabled)

	var parentFinalizer *processor.ParentFinalizer
	if renderers.assembler != nil {
		parentFinalizer = processor.NewParentFinalizer(
			queueClient, store, renderers.assembler, publisher, cfg.Worker.ID,
			filepath.Join(cfg.Workspace.Root, "parents"),
		)
		log.Printf("parent finalizer: enabled (Chronon assembler, output=%q)", filepath.Join(cfg.Workspace.Root, "parents"))
		go runParentFinalizationRecovery(ctx, queueClient, parentFinalizer, cfg.Pipeline.ClaimLongPoll)
	}

	// CameraRig baker: the overlay compiler owns what a resolved camera sample
	// must satisfy, but it must not start processes or touch temp files. The
	// exec-backed runner is injected through processor.Options above, as an
	// explicit compile dependency. An unset binary fails the animated-camera job
	// with a named configuration error instead of silently degrading.

	// Overlay catalog: the motion vocabulary and the preset ids this worker
	// renders come from ChrononTemplate's emitted catalog, embedded at build
	// time. If the embedded catalog and this build's rendering preset registry
	// disagree, every job naming the missing preset would be rejected after it
	// was claimed — so the disagreement is resolved here, before READY.
	if err := overlay.ValidateCatalogParity(); err != nil {
		log.Fatalf("overlay catalog: %v", err)
	}

	return workerPipeline{
		queue:           queueClient,
		proc:            proc,
		parentFinalizer: parentFinalizer,
		progress:        progressTracker,
		metrics:         workerMetrics,
	}
}

// buildArtifactRecorder opens the worker-local SQLite ledger mirror when
// configured. The mirror is diagnostic: a failed write is logged and flagged
// on the artifact's metrics (mirror_failure=1), never fatal — the central queue
// PostgreSQL row is authoritative for the artifact.
func buildArtifactRecorder(cfg *config.Config) artifactdb.Recorder {
	if cfg.ArtifactDB.Path == "" {
		return nil
	}
	recorder, err := artifactdb.NewSQLite(cfg.ArtifactDB.Path)
	if err != nil {
		log.Fatalf("artifact_db: %v", err)
	}
	log.Printf("artifact_db: ledger enabled at %q", cfg.ArtifactDB.Path)
	return recorder
}

// buildPublisher builds the optional Drive publication port, decoupled from
// rendering.
func buildPublisher(ctx context.Context, cfg *config.Config) drive.Publisher {
	if !cfg.Drive.Enabled {
		return nil
	}
	switch cfg.Drive.Mode {
	case "mock":
		pub := drive.NewMock(cfg.Drive.MockDir, cfg.Drive.MockFailFirst)
		log.Printf("drive: mock publisher (fail_first=%d, dir=%q)", cfg.Drive.MockFailFirst, cfg.Drive.MockDir)
		return pub
	case "oauth":
		pub, err := drive.NewGoogleOAuthWithOptions(ctx, cfg.Drive.CredentialsFile, cfg.Drive.TokenFile, cfg.Drive.ParentFolderID,
			drive.Options{ChunkBytes: cfg.Drive.ChunkBytes})
		if err != nil {
			log.Fatalf("drive: %v", err)
		}
		log.Printf("drive: oauth publisher (folder=%q, chunk_bytes=%d)", cfg.Drive.ParentFolderID, cfg.Drive.ChunkBytes)
		return pub
	default:
		pub, err := drive.NewGoogleWithOptions(ctx, cfg.Drive.CredentialsFile, cfg.Drive.ParentFolderID,
			drive.Options{ChunkBytes: cfg.Drive.ChunkBytes})
		if err != nil {
			log.Fatalf("drive: %v", err)
		}
		log.Printf("drive: google publisher (folder=%q, chunk_bytes=%d)", cfg.Drive.ParentFolderID, cfg.Drive.ChunkBytes)
		return pub
	}
}

// startWorkerSurface exposes /health, /progress and /metrics, registers the
// worker as READY and starts the heartbeat. It returns the full registration
// so the drain path can re-send it without losing identity.
func startWorkerSurface(ctx context.Context, cfg *config.Config, gpuInfo gpu.Info, renderers rendererSet, pipeline workerPipeline) queue.Worker {
	// The worker id is part of the identity tuple, so it is recorded before
	// the identity document is assembled.
	buildinfo.SetRuntime(buildinfo.RuntimeInfo{WorkerID: cfg.Worker.ID})
	identity := buildinfo.Current()
	healthInfo := health.Info{
		Worker:        cfg.Worker.ID,
		RenderingGen:  version.RenderingGen,
		Chronon:       renderers.chrononVersion,
		OverlaySchema: version.OverlaySchema,
		Backend:       cfg.Chronon.Backend,
		Status:        "ready",
		Build:         &identity,
	}
	healthServer := health.NewServer(cfg.Health.Addr, healthInfo)
	// Live render progress: the tracker receives every frame milestone the
	// renderer prints; /progress exposes it locally and the pusher relays a
	// throttled snapshot to the queue so GET /jobs/{id} shows real position
	// instead of an opaque RUNNING/0% for the whole render. The tracker and
	// workerMetrics were built before the processor and handed to it through
	// Options, so this step only wires the health surface to them.
	healthServer.SetProgressFunc(pipeline.progress.Current)
	// Fail-open degradations (workspace cleanup failures today) must be visible
	// on the worker's own surface, not only in the log stream.
	healthServer.SetDegradationFunc(pipeline.proc.Degradations)
	// Prometheus exposition on the same admin port. The worker was the only
	// process in the chain with no scrapeable surface (GET :8085/metrics → 404),
	// so the per-phase timings it already measures were unreachable. Every feed
	// is the EXISTING instrumentation: the processor's phase hook, its terminal
	// report funnel, and its gpu_gap_us duty-cycle KPI (the one number that says
	// whether the GPU idles BETWEEN renders as opposed to inside them).
	healthServer.SetMetricsHandler(pipeline.metrics.Handler())
	go func() {
		progresspush.New(pipeline.queue, pipeline.progress, progresspush.DefaultInterval).Run(ctx)
	}()
	go func() {
		if err := healthServer.Run(ctx); err != nil {
			log.Fatalf("health: %v", err)
		}
	}()

	// Register worker in queue liveness registry now that all dependencies
	// and health checks are up and verified.
	hostname, _ := os.Hostname()
	workerRegistration := queue.Worker{
		ID:                   cfg.Worker.ID,
		Hostname:             hostname,
		Status:               queue.WorkerStatusReady,
		RenderingGenVersion:  version.RenderingGen,
		ChrononVersion:       renderers.chrononVersion,
		OverlaySchemaVersion: version.OverlaySchema,
		GPUBackend:           gpuInfo.Backend,
		GPUDevice:            fmt.Sprintf("%d", gpuInfo.Device),
		GPUDriver:            gpuInfo.Driver,
		Capabilities:         append([]string(nil), renderers.capabilities...),
		StartedAt:            time.Now().UTC(),
	}
	if err := pipeline.queue.Register(ctx, workerRegistration); err != nil {
		log.Fatalf("queue worker registration: %v", err)
	}
	// Surface sustained queue disconnect on /health and in the logs instead of
	// logging-and-forgetting: a worker whose heartbeat cannot reach the queue
	// is not fully ready even while it may still be processing a claim.
	var heartbeatFailures atomic.Int64
	healthServer.SetQueueStatus(func() string {
		if heartbeatFailures.Load() >= int64(cfg.Pipeline.DegradedAfterFailures) {
			return "degraded"
		}
		return "ready"
	})
	go runHeartbeatLoop(ctx, pipeline.queue, &heartbeatFailures, cfg.Pipeline)
	return workerRegistration
}

// runWorkerPools runs the three-stage pipeline (prep -> GPU lanes -> post) and
// marks the worker draining once ctx is cancelled.
func runWorkerPools(ctx context.Context, cfg *config.Config, renderers rendererSet, pipeline workerPipeline, workerRegistration queue.Worker) {
	numWorkers := cfg.Worker.PipelineWorkers
	if numWorkers < 1 {
		numWorkers = 1
	}

	log.Printf("worker %s ready: %s", cfg.Worker.ID, config.LaneParityGuidance(renderers.gpuLanes, cfg.Chronon.Mode, renderers.sockets))
	log.Printf("worker %s ready: renderinggen=%s chronon=%s schema=%d pipeline_workers=%d gpu_lanes=%d log_level=%s log_format=%s per_job_logs=%s",
		cfg.Worker.ID, version.RenderingGen, renderers.chrononVersion, version.OverlaySchema, numWorkers, renderers.gpuLanes,
		cfg.Logging.Level, cfg.Logging.Format, workerlog.DurableJobLogDir(cfg.Workspace.Root))

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
			runPrepPool(ctx, pipeline.queue, pipeline.proc, prepCh, cfg.Pipeline)
		}()
	}
	// GPU lanes: parallel Chronon render sessions.
	for i := 0; i < renderers.gpuLanes; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runGPULane(ctx, pipeline.queue, pipeline.proc, prepCh, doneCh, cfg.Pipeline)
		}()
	}
	// Post pool: finalize (probe/hash/store) + Drive publication (CPU/IO).
	for i := 0; i < numWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPostPool(ctx, pipeline.queue, pipeline.proc, pipeline.parentFinalizer, doneCh, cfg.Pipeline)
		}()
	}
	workers.Wait()

	// SIGTERM/SIGINT has already cancelled ctx and drained the pools. Mark the
	// registration draining with a FRESH context: ctx is done, so reusing it
	// would make the final status update a silent no-op. The FULL registration
	// is re-sent because the queue upsert replaces every column; sending only
	// {id,status} would erase hostname and versions.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 5*time.Second)
	if err := markWorkerDraining(drainCtx, pipeline.queue, workerRegistration); err != nil {
		log.Printf("queue worker drain registration: %v", err)
	} else {
		log.Printf("worker %s marked draining", cfg.Worker.ID)
	}
	cancelDrain()
	log.Println("shutting down")
}
