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
//	-mode calibration-suite   the two-runtime measurement REPEATED over a
//	                          manifest of 3-5 shapes, >=3 runs each, aggregated
//	                          into one report (the exit condition's calibration
//	                          input). `-dry-run` resolves the matrix with no GPU.
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
	shapeManifest := flag.String("shape-manifest", "", "calibration matrix: labels + concrete plans + repeat (calibration-suite, required)")
	dryRun := flag.Bool("dry-run", false, "calibration-suite: resolve the matrix (shape/SHA/canvas/frames) and exit without rendering")
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
		runPool(ctx, runtimeFlags{
			plan: *plan, outputDir: *outputDir, samples: *samples, binary: *chrononBinary,
			assetsRoot: *assetsRoot, backend: *backend, hardware: *hardware, encodePreset: *encodePreset,
			gpuDevice: *gpuDevice, daemons: *daemons, lanes: *lanes, interval: *poolInterval,
			hold: *hold, timeout: *timeout, out: *out,
		})
	case "calibration-suite":
		runSuite(ctx, suiteFlags{
			manifest: *shapeManifest, dryRun: *dryRun,
			runtimeFlags: runtimeFlags{
				plan: *plan, outputDir: *outputDir, samples: *samples, binary: *chrononBinary,
				assetsRoot: *assetsRoot, backend: *backend, hardware: *hardware, encodePreset: *encodePreset,
				gpuDevice: *gpuDevice, daemons: *daemons, lanes: *lanes, interval: *poolInterval,
				hold: *hold, timeout: *timeout, out: *out,
			},
		})
	default:
		fmt.Fprintf(os.Stderr, "vram-probe: -mode must be single, two-runtime-pool or calibration-suite, got %q\n", *mode)
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

// runtimeFlags are the launch settings EVERY mode that spawns owned daemons
// shares. The calibration suite IS the two-runtime measurement repeated, so a
// runtime flag added for one mode has to reach the other: one struct makes that
// structural, instead of two identical literals that drift the moment someone
// adds a flag to only the one they are working on.
type runtimeFlags struct {
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

// options is the ONE translation from runtime flags to harness options. A
// second copy of this mapping is how a mode ends up measuring something the
// operator did not ask for.
func (f runtimeFlags) options() vramprobe.PoolOptions {
	return vramprobe.PoolOptions{
		PlanPath:        f.plan,
		OutputDir:       f.outputDir,
		SamplesPath:     f.samples,
		Binary:          f.binary,
		AssetsRoot:      f.assetsRoot,
		Backend:         f.backend,
		HardwareEncoder: f.hardware,
		EncodePreset:    f.encodePreset,
		GPUDevice:       f.gpuDevice,
		Daemons:         f.daemons,
		LanesPerDaemon:  f.lanes,
		Interval:        f.interval,
		Hold:            f.hold,
		Timeout:         f.timeout,
		ReportPath:      f.out,
	}
}

// suiteFlags is the runtime settings plus the calibration matrix, which is the
// only thing the suite has that the harness does not.
type suiteFlags struct {
	runtimeFlags
	manifest string
	dryRun   bool
}

func runSuite(ctx context.Context, flags suiteFlags) {
	if flags.manifest == "" {
		fmt.Fprintln(os.Stderr, "vram-probe: -shape-manifest is required in calibration-suite mode")
		os.Exit(2)
	}
	if flags.out == "" {
		fmt.Fprintln(os.Stderr, "vram-probe: -out is required in calibration-suite mode")
		os.Exit(2)
	}
	manifest, err := vramprobe.LoadSuiteManifest(flags.manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}

	if flags.dryRun {
		// The matrix is resolved (and the plans validated) before any GPU time
		// is spent: a shape whose plan the daemon cannot render must fail here.
		plans, err := vramprobe.PlanSuite(manifest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
			os.Exit(1)
		}
		total := 0
		for _, plan := range plans {
			total += plan.Repeat
			fmt.Printf("shape %-24s %dx%d %.0f fps %d frames layers=%v repeat=%d plan_sha256=%s\n",
				plan.Label, plan.Shape.Width, plan.Shape.Height,
				float64(plan.Shape.FPSNum)/float64(max(plan.Shape.FPSDen, 1)),
				plan.Shape.Frames, plan.Shape.LayerTypes, plan.Repeat, plan.PlanSHA256[:12])
		}
		fmt.Printf("dry-run: %d shape(s), %d run(s) to execute; nothing was rendered\n", len(plans), total)
		return
	}

	options := flags.options()
	options.Logger = log.New(os.Stderr, "vram-probe: ", log.LstdFlags)
	report, err := vramprobe.RunSuite(ctx, flags.manifest, manifest, options)
	// The shape table comes from the RETURNED report and is printed BEFORE the
	// error is handled: an incomplete suite still returns the document it wrote,
	// and the operator needs the rows and the re-run list together. An early
	// failure (manifest, plans) returns an empty report and prints no rows.
	for _, shape := range report.Shapes {
		fmt.Printf("shape %-24s runs %d/%d attribution MiB min/p50/max=%d/%d/%d device peak %d resident hold %d identical=%v same-device=%v\n",
			shape.Label, shape.RunsCompleted, shape.RunsRequested,
			shape.DaemonAttributedMiB.Min, shape.DaemonAttributedMiB.P50, shape.DaemonAttributedMiB.Max,
			shape.DevicePeakMiB.Max, shape.ResidentHoldDeviceMiB.P50,
			shape.ArtifactsIdenticalAcrossRuns, shape.DeviceConsistentAcrossRuns)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}
}

func runPool(ctx context.Context, flags runtimeFlags) {
	options := flags.options()
	options.Logger = log.New(os.Stderr, "vram-probe: ", log.LstdFlags)
	report, err := vramprobe.RunTwoRuntimePool(ctx, options)
	// The line is printed from the RETURNED report, before the error: an
	// incomplete measurement still returns the document it wrote, and what it
	// did measure is the context for the failure. An early failure (plan,
	// nvidia-smi, daemons) returns no report and prints no line.
	if report != nil {
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}
}
