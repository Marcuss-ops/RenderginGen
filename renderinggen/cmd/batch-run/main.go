// Command batch-run runs a batch manifest end to end on the production path:
// submit to the central queue, wait every job terminal, download the certified
// artifacts and their raw timing sidecars, and write the run report.
//
// It is the Go replacement for the per-corpus producer scripts (the Python
// render/poll/download loops): submission keeps the ids and idempotency keys of
// cmd/batch-submit, the transport is queue/client, and the report is derived
// only from queue timestamps and certified artifact facts — never from a
// stopwatch or an estimate. A job that never reaches a terminal state is
// reported as NOT TERMINAL and the command exits non-zero.
//
// A corpus that needs its assets served over HTTP can ask for the built-in file
// server instead of starting one outside the run:
//
//	batch-run -manifest manifest.json -report report.json \
//	  -serve-assets ../.. -serve-addr 127.0.0.1:8099
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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlaybatch"
)

func main() {
	manifestPath := flag.String("manifest", "", "batch manifest to run (required)")
	queueURL := flag.String("queue", "http://localhost:8081", "central queue endpoint")
	reportPath := flag.String("report", "", "run report to write (required)")
	snapshotPath := flag.String("snapshot", "", "where to preserve the final queue body of every job")
	downloadDir := flag.String("download-dir", "", "directory for the rendered MP4s, one subdirectory per family")
	timingDir := flag.String("timing-dir", "", "directory for the raw per-frame timing sidecars (default: beside each render)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall budget for submit + render + download")
	concurrency := flag.Int("concurrency", 8, "jobs waited on and downloaded at once")
	serveDir := flag.String("serve-assets", "", "serve this directory over HTTP for the duration of the run")
	serveAddr := flag.String("serve-addr", "127.0.0.1:8099", "listen address for -serve-assets")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	report, err := overlaybatch.Run(ctx, overlaybatch.Options{
		ManifestPath: *manifestPath,
		QueueURL:     *queueURL,
		ReportPath:   *reportPath,
		SnapshotPath: *snapshotPath,
		DownloadDir:  *downloadDir,
		TimingDir:    *timingDir,
		Timeout:      *timeout,
		Concurrency:  *concurrency,
		ServeDir:     *serveDir,
		ServeAddr:    *serveAddr,
		Logger:       log.New(os.Stderr, "batch-run: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "batch-run: %v\n", err)
		os.Exit(1)
	}
	if report != nil {
		fmt.Printf("batch %s: %d/%d completed\n", report.BatchID, report.Summary.Completed, report.Summary.JobsTotal)
	}
}
