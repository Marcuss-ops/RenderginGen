// Command chunk-overlap-bench submits two explicit disjoint chunk children of
// one plan to ONE Chronon daemon and measures whether they overlap.
//
// It is the live half of the GPU admission gate: the policy's parallel class is
// a chunk child (explicit frame_range + parallel_disjoint), so this is the only
// shape whose overlap the daemon is allowed to admit. Serialized admission shows
// up as the second job's wall containing the first one's; overlapping admission
// shows two walls of roughly one chunk each.
//
// LAB INSTRUMENT, deliberately unsafe: it asks a daemon for work the policy is
// meant to refuse. On a build without the plan-shape guard a composited plan was
// promoted into the shared domain and the daemon hung or exited (measured
// 2026-11-02, Chronon3d/docs/evidence/gpu-admission-two-jobs-2026-11-02.md).
// Never point it at a production socket; it always starts its own daemon.
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

type chunkJob struct {
	Index    int    `json:"index"`
	First    int64  `json:"first_frame"`
	Last     int64  `json:"last_frame"`
	WallMS   int64  `json:"wall_ms"`
	Bytes    int64  `json:"bytes,omitempty"`
	Failed   string `json:"failed,omitempty"`
	Started  string `json:"started_at"`
	Finished string `json:"finished_at"`
}

func main() {
	plan := flag.String("plan", "", "concrete chronon.render-plan.v2")
	bin := flag.String("chronon-bin", "", "chronon3d_cli executable")
	assets := flag.String("assets-root", "", "asset root")
	outDir := flag.String("output-dir", "", "artifact directory")
	first := flag.Int64("first", 24, "last frame of the first chunk plus one (first chunk is 0..first-1)")
	last := flag.Int64("last", 47, "inclusive last frame of the second chunk")
	jobsCount := flag.Int("jobs", 2, "number of chunk children submitted behind the barrier")
	parallel := flag.Bool("parallel-disjoint", true, "declare the trusted parallel_disjoint admission hint")
	plan2 := flag.String("plan2", "", "optional second plan for job index 1 (independent-plan measurement)")
	sameRange := flag.Bool("same-range", false, "render the whole plan with an explicit range in both jobs")
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
		SocketBase:     filepath.Join(os.TempDir(), fmt.Sprintf("chunk-overlap-%d", time.Now().UnixNano())),
		Count:          1,
		LanesPerDaemon: 2,
		Binary:         *bin,
		AssetsRoot:     *assets,
		Backend:        "vulkan",
		GPUDevice:      0,
		Stdout:         os.Stderr,
		Stderr:         os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Shutdown(ctx)

	render := func(index int, firstFrame, lastFrame int64, parallel bool) chunkJob {
		out := filepath.Join(*outDir, fmt.Sprintf("chunk-%d.mp4", index))
		planPath := *plan
		if index == 1 && *plan2 != "" {
			planPath = *plan2
		}
		req := chronon.RenderRequest{
			PlanPath:         planPath,
			AssetsRoot:       *assets,
			OutputPath:       out,
			EncodePreset:     "p1",
			HardwareEncoder:  "nvenc",
			TotalFrames:      *last + 1,
			RangeEnabled:     *sameRange || firstFrame != 0 || lastFrame != *last,
			FirstFrame:       firstFrame,
			LastFrame:        lastFrame,
			ParallelDisjoint: parallel,
			Report:           true,
			Requirements: chronon.ExecutionRequirements{
				Backend:            "vulkan",
				GPURequired:        true,
				CPUFallbackAllowed: true,
			},
		}
		res := chunkJob{Index: index, First: firstFrame, Last: lastFrame}
		res.Started = time.Now().UTC().Format(time.RFC3339Nano)
		began := time.Now()
		err := pool.Render(ctx, req)
		res.WallMS = time.Since(began).Milliseconds()
		res.Finished = time.Now().UTC().Format(time.RFC3339Nano)
		if err != nil {
			res.Failed = err.Error()
			return res
		}
		if info, statErr := os.Stat(out); statErr == nil {
			res.Bytes = info.Size()
		}
		return res
	}

	// Warm the daemon's pools and plan cache before the measured pair.
	warm := render(99, 0, *last, false)
	if warm.Failed != "" {
		fmt.Fprintf(os.Stderr, "warmup failed: %s\n", warm.Failed)
		os.Exit(1)
	}

	jobs := make([]chunkJob, *jobsCount)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < *jobsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-barrier
			switch {
			case *sameRange:
				jobs[index] = render(index, 0, *last, *parallel)
			case index == 0:
				jobs[index] = render(index, 0, *first-1, *parallel)
			default:
				jobs[index] = render(index, *first, *last, *parallel)
			}
		}(i)
	}
	began := time.Now()
	close(barrier)
	wg.Wait()
	wall := time.Since(began).Milliseconds()

	var work int64
	for _, job := range jobs {
		work += job.WallMS
	}
	report := map[string]any{
		"schema":    "chronon.chunk-overlap-bench.v1",
		"plan":      *plan,
		"daemons":   1,
		"lanes":     2,
		"jobs":      jobs,
		"wall_ms":   wall,
		"work_ms":   work,
		"speedup":   float64(work) / float64(wall),
		"warmup_ms": warm.WallMS,
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
}
