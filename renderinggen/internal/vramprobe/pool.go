// pool.go owns the LABORATORY harness that answers a question the production
// probe cannot: do TWO warm render runtimes of the same shape fit on this device
// at the same time?
//
// Why it exists. The single-job probe (probe.go) submits through the normal
// queue path, i.e. through the daemon that serializes RENDER_JOB behind
// `m_render_job_mutex`. While that mutex is held, exactly one job is resident and
// executed, so the delta it measures is the incremental cost of ONE active job —
// it cannot prove that a second one coexists, and the two-runtime number that
// was recorded separately (two cold-start CLI processes) is not the same object:
// a CLI process allocates for one plan and exits, while a daemon keeps its pools,
// caches and atlases resident and charges VRAM per plan at admission.
//
// This harness therefore holds TWO owned daemons (two processes, two runtimes,
// two warm pools) on distinct sockets, renders one job on each CONCURRENTLY, and
// samples the device across five labelled phases — idle, warm pools, rendering,
// resident hold (pools alive, nothing rendering) and after shutdown — so the
// incremental cost of the second runtime is separable from the in-flight cost of
// a render and from the steady-state residency of a warm pool.
//
// What it does NOT do: it never touches production semantics, it never removes
// `m_render_job_mutex`, and it reports
// `authorizes_mutex_removal: false` unconditionally. Feeding
// `derive_vram_working_set` is a calibration decision that needs this number on
// several shapes (overlay-only, source-video FullGraph, DirectYUV, 4K) plus
// internal allocator telemetry; one shape on one host is evidence, not a gate.
//
// File layout: this file owns the run itself and the phase sequence;
// pool_types.go the published report contract, pool_jobs.go the per-job
// helpers and pool_sampler.go the sampling goroutine.
package vramprobe

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// RunTwoRuntimePool starts the configured number of OWNED daemons, renders one
// job on each concurrently and reports what the device did.
//
// Every step that could make the number meaningless fails loudly instead of
// being reported: a pool that could not own its sockets (a stale socket file
// means another process already serves it), a plan that is not the concrete
// chronon plan, a job that failed, or a device that cannot be sampled.
func RunTwoRuntimePool(ctx context.Context, opts PoolOptions) (*PoolReport, error) {
	if opts.PlanPath == "" {
		return nil, fmt.Errorf("vramprobe: -plan is required")
	}
	if opts.ReportPath == "" {
		return nil, fmt.Errorf("vramprobe: -out is required")
	}
	if opts.Daemons < 2 {
		return nil, fmt.Errorf("vramprobe: the two-runtime harness needs at least 2 daemons, got %d", opts.Daemons)
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("vramprobe: -chronon-bin is required (the owned daemons need an executable)")
	}
	if opts.Backend == "" {
		opts.Backend = "vulkan"
	}
	if opts.HardwareEncoder == "" {
		opts.HardwareEncoder = "nvenc"
	}
	if opts.EncodePreset == "" {
		opts.EncodePreset = "p1"
	}
	if opts.LanesPerDaemon <= 0 {
		opts.LanesPerDaemon = 1
	}
	if opts.Interval <= 0 {
		opts.Interval = 20 * time.Millisecond
	}
	if opts.Hold <= 0 {
		opts.Hold = 15 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Dir(opts.ReportPath)
	}
	if opts.SamplesPath == "" {
		opts.SamplesPath = opts.ReportPath + ".samples.log"
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "vram-two-runtime: ", log.LstdFlags)
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stderr
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	// Every path handed to a daemon or recorded as evidence is absolutized here.
	// The engine's AssetResolver rejects a relative asset root outright ("root
	// must be an absolute path"), and a report that names a path relative to
	// whatever directory the harness happened to run in is not reproducible.
	if err := absolutizePoolPaths(&opts); err != nil {
		return nil, err
	}
	if info, err := os.Stat(opts.AssetsRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("vramprobe: assets root %q is not a readable directory", opts.AssetsRoot)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("vramprobe: create output dir: %w", err)
	}

	shape, planDigest, err := readPoolPlan(opts.PlanPath)
	if err != nil {
		return nil, err
	}
	device, err := poolDeviceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("vramprobe: nvidia-smi: %w", err)
	}

	// A fresh socket base, so a stale socket file can never make the harness
	// render through a daemon it does not own: StartDaemonPool reuses a socket
	// that already exists, which is right for production and fatal for a
	// measurement of "two runtimes I started".
	socketDir, err := os.MkdirTemp("", "vram-two-runtime-")
	if err != nil {
		return nil, fmt.Errorf("vramprobe: create socket dir: %w", err)
	}
	defer os.RemoveAll(socketDir)
	socketBase := filepath.Join(socketDir, "chronon.sock")

	collector := newPoolSampler(ctx, opts.Interval)
	collector.setPhase(phaseIdle)
	// Settle the idle baseline before any daemon exists: this phase is what every
	// later delta is read against, and it must not contain this harness's own
	// start-up.
	collector.snapshot()
	time.Sleep(4 * opts.Interval)

	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase:     socketBase,
		Count:          opts.Daemons,
		LanesPerDaemon: opts.LanesPerDaemon,
		Binary:         opts.Binary,
		AssetsRoot:     opts.AssetsRoot,
		Backend:        opts.Backend,
		GPUDevice:      opts.GPUDevice,
		Stdout:         opts.Stdout,
		Stderr:         opts.Stderr,
	})
	if err != nil {
		collector.stop()
		return nil, fmt.Errorf("vramprobe: start daemon pool: %w", err)
	}
	owned := pool.OwnedCount()
	if owned != opts.Daemons {
		pool.Shutdown(context.Background())
		collector.stop()
		return nil, fmt.Errorf("vramprobe: %d of %d daemons were OWNED (the rest were already served); the measurement would not be two fresh runtimes", owned, opts.Daemons)
	}
	sockets := pool.Sockets()
	logger.Printf("%d owned daemon(s) on %s..%s, %d lane(s) each", owned, sockets[0], sockets[len(sockets)-1], opts.LanesPerDaemon)

	// Warm pools: both runtimes are resident and idle. This is the phase that
	// answers "what does a second warm runtime cost while nothing renders".
	collector.setPhase(phaseWarmPools)
	time.Sleep(3 * time.Second)

	collector.setPhase(phaseRendering)
	jobs := runPoolJobs(ctx, opts, pool, sockets, shape, logger)

	collector.setPhase(phaseResidentHold)
	time.Sleep(opts.Hold)

	pool.Shutdown(context.Background())
	collector.setPhase(phaseAfterStop)
	time.Sleep(3 * time.Second)
	collector.stop()

	report := &PoolReport{
		Schema:                 PoolReportSchema,
		PlanPath:               opts.PlanPath,
		PlanSHA256:             planDigest,
		Shape:                  shape,
		Device:                 device,
		Execution:              PoolRuntime{Binary: opts.Binary, Backend: opts.Backend, HardwareEncoder: opts.HardwareEncoder, EncodePreset: opts.EncodePreset, SocketBase: socketBase, GPUDevice: opts.GPUDevice},
		DaemonCount:            opts.Daemons,
		OwnedDaemons:           owned,
		LanesPerDaemon:         opts.LanesPerDaemon,
		SampleIntervalMS:       int(opts.Interval / time.Millisecond),
		HoldSeconds:            opts.Hold.Seconds(),
		Phases:                 collector.phaseFacts(),
		Samples:                collector.count(),
		SamplesFile:            opts.SamplesPath,
		Jobs:                   jobs,
		ArtifactsIdentical:     jobsIdentical(jobs),
		AuthorizesMutexRemoval: false,
		Caveat: "laboratory harness: two OWNED warm daemons, one concurrent job each, one plan shape on one host. " +
			"It falsifies a per-job VRAM calibration for this shape and measures the second runtime's resident cost; " +
			"it does NOT authorize removing m_render_job_mutex (that needs several certified shapes plus internal allocator telemetry).",
	}
	// Headline numbers. The per-runtime figure is derived, and the report says
	// how: this host keeps one chronon3d_cli daemon resident (the systemd unit),
	// so the idle phase already contains one warm runtime and the warm-pools
	// phase contains three. Dividing the increment by the number of ADDED
	// daemons is therefore the resident cost of one extra runtime — not the cost
	// of a render, and not a license to raise concurrency.
	//
	// The increment is clamped at zero because a fresh daemon allocates its
	// device memory lazily: at the moment the warm phase is sampled the engine may
	// have touched nothing at all, and a negative "cost" would be an artefact of
	// the sample timing rather than a measurement. When that happens the flag says
	// so and the resident-hold phase is the number to read.
	report.IdleDeviceMiB = report.Phases[string(phaseIdle)].PeakDeviceMiB
	report.WarmPoolsDeviceMiB = report.Phases[string(phaseWarmPools)].PeakDeviceMiB
	report.RenderingPeakDeviceMiB = report.Phases[string(phaseRendering)].PeakDeviceMiB
	report.RenderingDeltaMiB = report.RenderingPeakDeviceMiB - report.IdleDeviceMiB
	report.ResidentHoldDeviceMiB = report.Phases[string(phaseResidentHold)].PeakDeviceMiB
	report.IdleResidentDaemonMiB = report.Phases[string(phaseIdle)].PeakDaemonMiB
	report.BaselineHasResidentDaemon = report.IdleResidentDaemonMiB > 0
	warmGrowth := report.WarmPoolsDeviceMiB - report.IdleDeviceMiB
	report.WarmPoolsTouchedDevice = warmGrowth > 0
	if warmGrowth < 0 {
		warmGrowth = 0
	}
	report.SecondRuntimeResidentMiB = warmGrowth / opts.Daemons

	if err := collector.writeSamples(opts.SamplesPath); err != nil {
		return nil, err
	}
	if err := writeJSONFile(opts.ReportPath, report); err != nil {
		return nil, err
	}
	if failed := failedJobs(jobs); failed != "" {
		// The document stays on disk (it is the evidence of what happened, and
		// of WHY this is not a two-runtime number), but the call is an error:
		// returning nil here would let a run in which nothing rendered exit
		// zero, which is the opposite of what the header of this file promises.
		return report, fmt.Errorf("vramprobe: two-runtime measurement incomplete: %s (report written to %s)", failed, opts.ReportPath)
	}
	logger.Printf("phases: idle %d MiB -> warm pools %d -> rendering %d -> resident hold %d -> after shutdown %d",
		report.IdleDeviceMiB, report.WarmPoolsDeviceMiB, report.RenderingPeakDeviceMiB,
		report.ResidentHoldDeviceMiB, report.Phases[string(phaseAfterStop)].PeakDeviceMiB)
	logger.Printf("derived: rendering delta vs idle %d MiB, second warm runtime resident ~%d MiB (%d added daemons, idle baseline daemon %d MiB)",
		report.RenderingDeltaMiB, report.SecondRuntimeResidentMiB, opts.Daemons, report.IdleResidentDaemonMiB)
	return report, nil
}
