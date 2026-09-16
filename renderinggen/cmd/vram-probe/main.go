// Command vram-probe measures the device working set of one overlay render per
// job kind.
//
// It takes two jobs out of a certified manifest, perturbs each plan (so no
// content-addressed cache can answer for the render), submits them through the
// normal queue path and samples nvidia-smi while they are in flight, reporting
// the idle baseline, the peak, and the DELTA a second concurrent exporter would
// have to fit into.
//
// The probe is evidence tooling: it needs a live queue, a warm daemon and the
// GPU, and it fails loudly rather than reporting a number it did not observe.
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
	sourceManifest := flag.String("source-manifest", "", "certified batch manifest to take the probe jobs from (required)")
	probeManifest := flag.String("probe-manifest", "manifest_vram_probe.json", "where to write the perturbed two-job manifest")
	batchID := flag.String("batch-id", "vram-probe", "batch id that scopes the probe jobs")
	queueURL := flag.String("queue", "http://localhost:8081", "central queue endpoint")
	interval := flag.Duration("interval", 50*time.Millisecond, "nvidia-smi sampling period")
	timeout := flag.Duration("timeout", 10*time.Minute, "render budget for the two probe jobs")
	out := flag.String("out", "", "probe report to write (required)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	report, err := vramprobe.Run(ctx, vramprobe.Options{
		SourceManifest: *sourceManifest,
		ProbeManifest:  *probeManifest,
		BatchID:        *batchID,
		QueueURL:       *queueURL,
		Interval:       *interval,
		Timeout:        *timeout,
		ReportPath:     *out,
		Logger:         log.New(os.Stderr, "vram-probe: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "vram-probe: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("batch %s: peak device %d MiB (delta %d MiB), peak daemon %d MiB\n",
		report.BatchID, report.PeakDeviceMiB, report.DeviceDeltaMiB, report.PeakDaemonMiB)
}
