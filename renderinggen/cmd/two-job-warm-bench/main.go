// Command two-job-warm-bench measures two independent RENDER_JOBs on warm
// Chronon daemons, first sequentially and then behind a start barrier.
//
// The vram-probe two-runtime-pool harness measures the same two jobs but pays
// daemon startup inside each job's wall, so its numbers cannot answer "does
// admission concurrency pay on a warm production daemon". This bench warms both
// pools first, then times the same two jobs both ways on the same runtimes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

type jobResult struct {
	Index  int    `json:"index"`
	WallMS int64  `json:"wall_ms"`
	Failed string `json:"failed,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type phaseResult struct {
	Phase   string      `json:"phase"`
	WallMS  int64       `json:"wall_ms"`
	Jobs    []jobResult `json:"jobs"`
	WorkMS  int64       `json:"work_ms"`
	Speedup float64     `json:"speedup"`
}

func main() {
	plan := flag.String("plan", "", "concrete chronon.render-plan.v2")
	bin := flag.String("chronon-bin", "", "chronon3d_cli executable")
	assets := flag.String("assets-root", "", "asset root")
	outDir := flag.String("output-dir", "", "artifact directory")
	frames := flag.Int64("frames", 48, "plan duration in frames")
	flag.Parse()
	if *plan == "" || *bin == "" || *assets == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "plan, chronon-bin, assets-root and output-dir are required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "output dir: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase:     filepath.Join(os.TempDir(), fmt.Sprintf("two-job-warm-%d", time.Now().UnixNano())),
		Count:          2,
		LanesPerDaemon: 1,
		Binary:         *bin,
		AssetsRoot:     *assets,
		Backend:        "vulkan",
		GPUDevice:      0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Shutdown(ctx)

	render := func(name string) jobResult {
		req := chronon.RenderRequest{
			PlanPath:        *plan,
			AssetsRoot:      *assets,
			OutputPath:      filepath.Join(*outDir, name+".mp4"),
			EncodePreset:    "p1",
			HardwareEncoder: "nvenc",
			TotalFrames:     *frames,
			Requirements: chronon.ExecutionRequirements{
				Backend:            "vulkan",
				GPURequired:        true,
				CPUFallbackAllowed: true,
			},
		}
		began := time.Now()
		err := pool.Render(ctx, req)
		res := jobResult{WallMS: time.Since(began).Milliseconds()}
		if err != nil {
			res.Failed = err.Error()
		} else if info, statErr := os.Stat(req.OutputPath); statErr == nil {
			res.Bytes = info.Size()
		}
		return res
	}

	// Warm both pools: the render path a production job finds is a warm one.
	for i := 0; i < 2; i++ {
		if res := render(fmt.Sprintf("warmup-%d", i)); res.Failed != "" {
			fmt.Fprintf(os.Stderr, "warmup %d failed: %s\n", i, res.Failed)
			os.Exit(1)
		}
	}

	serial := phaseResult{Phase: "serial"}
	started := time.Now()
	for i := 0; i < 2; i++ {
		res := render(fmt.Sprintf("serial-%d", i))
		res.Index = i
		serial.Jobs = append(serial.Jobs, res)
		serial.WorkMS += res.WallMS
		if res.Failed != "" {
			fmt.Fprintf(os.Stderr, "serial %d failed: %s\n", i, res.Failed)
			os.Exit(1)
		}
	}
	serial.WallMS = time.Since(started).Milliseconds()
	serial.Speedup = float64(serial.WorkMS) / float64(serial.WallMS)

	concurrent := phaseResult{Phase: "concurrent"}
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]jobResult, 2)
	started = time.Now()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-barrier
			results[index] = render(fmt.Sprintf("concurrent-%d", index))
			results[index].Index = index
		}(i)
	}
	close(barrier)
	wg.Wait()
	concurrent.WallMS = time.Since(started).Milliseconds()
	for _, res := range results {
		concurrent.Jobs = append(concurrent.Jobs, res)
		concurrent.WorkMS += res.WallMS
		if res.Failed != "" {
			fmt.Fprintf(os.Stderr, "concurrent %d failed: %s\n", res.Index, res.Failed)
			os.Exit(1)
		}
	}
	concurrent.Speedup = float64(concurrent.WorkMS) / float64(concurrent.WallMS)

	report := map[string]any{
		"schema":              "renderinggen.two-job-warm-bench.v1",
		"plan":                *plan,
		"frames":              *frames,
		"serial":              serial,
		"concurrent":          concurrent,
		"concurrency_pays_ms": serial.WallMS - concurrent.WallMS,
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
}
