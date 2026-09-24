// Command batch-verify certifies a finished batch against its own manifest.
//
// Three independent checks, all fail-closed:
//
//   - STRUCTURE: every job is `completed` and its artifact carries the contract's
//     facts (canvas, fps, frames, duration, certified backend), and the
//     downloaded bytes hash to the advertised content address;
//   - CONTENT: a phrase overlay must rasterise its text in the anchored band, and
//     an image overlay must show its card in every sampled frame — both fail an
//     artifact that "completed" while drawing almost nothing (the missing-font
//     signature for phrases, the pure-canvas signature for images);

//   - DISTINCTNESS: two languages whose translated text differs must not produce
//     identical bytes.
//
// With -baseline-manifest it additionally compares the same overlays against a
// previous run: bytes changed, and the current artifact rasterises at least the
// ink floor, so a font fix is certified by measurement instead of by assertion.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlaybatch"
)

func main() {
	manifestPath := flag.String("manifest", "", "batch manifest to verify (required)")
	queueURL := flag.String("queue", "http://localhost:8081", "central queue endpoint")
	reportPath := flag.String("report", "", "verification report to write (required)")
	stageDir := flag.String("stage", "", "directory to cache the downloaded artifacts in")
	requireBackend := flag.String("require-backend", "vulkan", "certified backend every artifact must declare (empty disables)")
	inkFloor := flag.Int("min-ink", overlaybatch.DefaultInkFloor, "minimum glyph pixels a phrase band must carry (negative disables)")
	imageInkFloor := flag.Int("min-image-ink", overlaybatch.DefaultImageInkFloor,
		"minimum non-canvas pixels the least inked sampled frame of an image overlay must carry (negative disables)")
	inkFrame := flag.Int("ink-frame", 60, "frame the pixel check samples")
	backgroundHex := flag.String("background", "#EEF1E7", "canvas colour the pixel check counts against")
	baselineManifest := flag.String("baseline-manifest", "", "previous run of the same overlays to compare against")
	baselineDir := flag.String("baseline-dir", "", "directory to cache the baseline downloads in")
	flag.Parse()

	background, err := overlaybatch.ParseHexColor(*backgroundHex)
	if err != nil {
		fatal("%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	report, err := overlaybatch.Verify(ctx, overlaybatch.VerifyOptions{
		ManifestPath:         *manifestPath,
		QueueURL:             *queueURL,
		ReportPath:           *reportPath,
		StageDir:             *stageDir,
		RequireBackend:       *requireBackend,
		InkFloor:             *inkFloor,
		ImageInkFloor:        *imageInkFloor,
		InkFrame:             *inkFrame,
		Background:           background,
		BaselineManifestPath: *baselineManifest,
		BaselineDir:          *baselineDir,
		Logger:               log.New(os.Stderr, "batch-verify: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "batch-verify: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("batch %s: %s (%d/%d structural)\n", report.BatchID, report.Verdict, report.Passed, report.Jobs)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "batch-verify: "+format+"\n", args...)
	os.Exit(1)
}
