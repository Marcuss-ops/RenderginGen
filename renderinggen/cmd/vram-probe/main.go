// Command vram-probe measures the device working set of overlay renders.
//
// Two modes, because they answer two different questions:
//
//	-mode single              one overlay render per job kind, submitted through
//	                          the normal queue path (i.e. the serializing daemon),
//	                          reporting the idle baseline, the peak and the DELTA
//	                          a second concurrent exporter would have to fit into.
//	-mode two-runtime-pool    TWO owned warm daemons (two runtimes, two pools) on
//	                          distinct sockets, one concurrent render each, with
//	                          the device sampled across idle / warm pools /
//	                          rendering / resident hold / after shutdown.
//
// The distinction is not cosmetic. `single` runs behind the daemon's
// `m_render_job_mutex`, so exactly one job is ever resident: what it measures is
// the incremental cost of ONE active job, and it cannot prove that a second one
// coexists. `two-runtime-pool` is the laboratory harness that answers the
// coexistence question for one shape, and it states in the report that it does
// not authorize removing the mutex.
//
// The probe is evidence tooling: it needs a live queue (single), the GPU and a
// Chronon binary, and it fails loudly rather than reporting a number it did not
// observe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/vramprobe"
)

func main() {
	mode := flag.String("mode", "single", "probe mode: single | two-runtime-pool")
	sourceManifest := flag.String("source-manifest", "", "certified batch manifest to take the probe jobs from (single, required)")
	probeManifest := flag.String("probe-manifest", "manifest_vram_probe.json", "where to write the perturbed two-job manifest (single)")
	batchID := flag.String("batch-id", "vram-probe", "batch id that scopes the probe jobs (single)")
	queueURL := flag.String("queue", "http://localhost:8081", "central queue endpoint (single)")
	interval := flag.Duration("interval", 50*time.Millisecond, "nvidia-smi sampling period (single)")
	timeout := flag.Duration("timeout", 10*time.Minute, "render budget for the probe jobs")
	out := flag.String("out", "", "probe report to write (required)")

	plan := flag.String("plan", "", "concrete chronon.render-plan.v2 to render (two-runtime-pool, required)")
	outputDir := flag.String("output-dir", "", "directory for the harness artifacts and samples (two-runtime-pool)")
	samples := flag.String("samples", "", "raw samples log (two-runtime-pool; default <out>.samples.log)")
	chrononBinary := flag.String("chronon-bin", "", "chronon3d_cli executable the owned daemons run (two-runtime-pool, required)")
	assetsRoot := flag.String("assets-root", "", "asset root passed to the owned daemons (two-runtime-pool)")
	backend := flag.String("backend", "vulkan", "render backend (two-runtime-pool)")
	hardware := flag.String("hardware", "nvenc", "hardware encoder: nvenc | none (two-runtime-pool)")
	encodePreset := flag.String("encode-preset", "p1", "native encode preset (two-runtime-pool)")
	gpuDevice := flag.Uint("gpu-device", 0, "Vulkan device index (two-runtime-pool)")
	daemons := flag.Int("daemons", 2, "concurrent runtimes/pools to keep resident (two-runtime-pool, minimum 2)")
	lanes := flag.Int("daemon-lanes", 1, "concurrent RENDER_JOBs per owned daemon (two-runtime-pool)")
	poolInterval := flag.Duration("pool-interval", 20*time.Millisecond, "nvidia-smi sampling period in pool mode (finer than single on purpose)")
	hold := flag.Duration("hold", 15*time.Second, "how long both warm pools stay resident with nothing rendering (two-runtime-pool)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch *mode {
	case "single":
		runSingle(ctx, singleOptions{
			sourceManifest: *sourceManifest,
			probeManifest:  *probeManifest,
			batchID:        *batchID,
			queueURL:       *queueURL,
			interval:       *interval,
			timeout:        *timeout,
			out:            *out,
		})
	case "two-runtime-pool":
		runPool(ctx, poolFlags{
			plan: *plan, outputDir: *outputDir, samples: *samples, binary: *chrononBinary,
			assetsRoot: *assetsRoot, backend: *backend, hardware: *hardware, encodePreset: *encodePreset,
			gpuDevice: *gpuDevice, daemons: *daemons, lanes: *lanes, interval: *poolInterval,
			hold: *hold, timeout: *timeout, out: *out,
		})
	default:
		fmt.Fprintf(os.Stderr, "vram-probe: -mode must be single or two-runtime-pool, got %q\n", *mode)
		os.Exit(2)
	}
}

// singleOptions groups the queue-path probe's flags.
type singleOptions struct {
	sourceManifest string
	probeManifest  string
	batchID        string
	queueURL       string
	interval       time.Duration
	timeout        time.Duration
	out            string
}

func runSingle(ctx context.Context, opts singleOptions) {
	report, err := vramprobe.Run(ctx, vramprobe.Options{
		SourceManifest: opts.sourceManifest,
		ProbeManifest:  opts.probeManifest,
		BatchID:        opts.batchID,
		QueueURL:       opts.queueURL,
		Interval:       opts.interval,
		Timeout:        opts.timeout,
		ReportPath:     opts.out,
		Logger:         log.New(os.Stderr, "vram-probe: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("batch %s: peak device %d MiB (delta %d MiB), peak daemon %d MiB\n",
		report.BatchID, report.PeakDeviceMiB, report.DeviceDeltaMiB, report.PeakDaemonMiB)
}

// poolFlags groups the two-runtime harness flags.
type poolFlags struct {
	plan         string
	outputDir    string
	samples      string
	binary       string
	assetsRoot   string
	backend      string
	hardware     string
	encodePreset string
	gpuDevice    uint
	daemons      int
	lanes        int
	interval     time.Duration
	hold         time.Duration
	timeout      time.Duration
	out          string
}

func runPool(ctx context.Context, flags poolFlags) {
	report, err := vramprobe.RunTwoRuntimePool(ctx, vramprobe.PoolOptions{
		PlanPath:        flags.plan,
		OutputDir:       flags.outputDir,
		SamplesPath:     flags.samples,
		Binary:          flags.binary,
		AssetsRoot:      flags.assetsRoot,
		Backend:         flags.backend,
		HardwareEncoder: flags.hardware,
		EncodePreset:    flags.encodePreset,
		GPUDevice:       flags.gpuDevice,
		Daemons:         flags.daemons,
		LanesPerDaemon:  flags.lanes,
		Interval:        flags.interval,
		Hold:            flags.hold,
		Timeout:         flags.timeout,
		ReportPath:      flags.out,
		Logger:          log.New(os.Stderr, "vram-probe: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}
	if !report.ArtifactsIdentical {
		fmt.Fprintf(os.Stderr, "vram-probe: WARNING the two runtimes produced different bytes; the number is still valid but the workload identity is not\n")
	}
	fmt.Printf("two-runtime pool: %d owned daemon(s), idle %d -> rendering peak %d MiB (delta %d MiB), resident hold %d MiB (~%d MiB per added warm runtime), artifacts identical=%v\n",
		report.OwnedDaemons,
		report.IdleDeviceMiB,
		report.RenderingPeakDeviceMiB,
		report.RenderingDeltaMiB,
		report.ResidentHoldDeviceMiB,
		report.SecondRuntimeResidentMiB,
		report.ArtifactsIdentical)
}
