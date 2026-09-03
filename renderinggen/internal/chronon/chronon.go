// Package chronon wraps the Chronon3d renderer.
//
// The worker's processing pipeline depends only on the Renderer interface, so
// it never knows whether rendering happens through the CLI subprocess (today)
// or a persistent daemon / IPC client (later). Chronon3d knows nothing about
// the queue, PipelineGen or the artifact store: it only receives a render plan
// + asset root + output path.
package chronon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultStallTimeout is the maximum duration Chronon CLI can produce zero
// output or progress before being considered stalled.
const DefaultStallTimeout = 3 * time.Minute

// RenderRequest is the renderable contract between RenderingGen and a
// Chronon3d backend. The render plan is already on disk (plan.json), assets
// are already materialized under AssetsRoot, and OutputPath is where the
// rendered file must be written.
type RenderRequest struct {
	PlanPath   string // path to the chronon.render-plan.v1 document (plan.json)
	AssetsRoot string // directory the plan's relative asset references resolve against
	OutputPath string // destination of the rendered output (e.g. result.mp4)
	FirstFrame int64  // optional global first frame for chunk execution
	LastFrame  int64  // optional inclusive global last frame for chunk execution
	Report     bool   // emit the execution report + telemetry JSONL (--report)
	// Requirements are semantic and backend-neutral. Chronon resolves them
	// against the capabilities of the selected device.
	Requirements ExecutionRequirements
	Output       OutputSpec
	TotalFrames  int64
	Progress     func(RenderProgress)
}

// RenderProgress is emitted when Chronon reports a frame milestone. It is
// intentionally transport-neutral; CLI can parse it from process output and
// IPC implementations can provide it from daemon status messages.
type RenderProgress struct {
	FramesDone  int64
	FramesTotal int64
	FPS         float64
	At          time.Time
}

type ExecutionRequirements struct {
	GPURequired         bool `json:"gpu_required"`
	CPUFallbackAllowed  bool `json:"cpu_fallback_allowed"`
	CompositionRequired bool `json:"composition_required"`
	PacketCopyAllowed   bool `json:"packet_copy_allowed"`
}

type OutputSpec struct {
	Codec  string `json:"codec,omitempty"`
	Width  uint32 `json:"width,omitempty"`
	Height uint32 `json:"height,omitempty"`
	FPSNum uint32 `json:"fps_num,omitempty"`
	FPSDen uint32 `json:"fps_den,omitempty"`
}

// Renderer renders a RenderRequest.
type Renderer interface {
	Render(context.Context, RenderRequest) error
}

// Client renders through the Chronon3d CLI binary installed in the worker
// image. It implements Renderer.
type Client struct {
	Home string
}

// Compile-time check that Client satisfies Renderer.
var _ Renderer = (*Client)(nil)

// Binary returns the path to the Chronon CLI.
func (c *Client) Binary() string {
	if override := os.Getenv("CHRONON_BINARY"); override != "" {
		return override
	}
	p := filepath.Join(c.Home, "bin", "chronon3d_cli")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return filepath.Join(c.Home, "apps", "chronon3d_cli", "chronon3d_cli")
}

// Verify checks that the Chronon binary is present and executable.
func (c *Client) Verify() error {
	p := c.Binary()
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("chronon binary missing at %s: %w", p, err)
	}
	if st.IsDir() {
		return fmt.Errorf("chronon binary %s is a directory", p)
	}
	return nil
}

// Version returns the installed Chronon version from the VERSION file.
func (c *Client) Version() string {
	data, err := os.ReadFile(filepath.Join(c.Home, "VERSION"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// Render invokes the CLI render subcommand with the plan file, assets root and
// output path. It streams output lines with timestamps, tracks progress, and
// runs a stall watchdog to abort hung render processes.
func (c *Client) Render(ctx context.Context, req RenderRequest) error {
	stallTimeout := DefaultStallTimeout
	if env := os.Getenv("CHRONON_STALL_TIMEOUT"); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			stallTimeout = d
		}
	}

	renderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := renderArgs(req)
	cmd := exec.CommandContext(renderCtx, c.Binary(), args...)
	cmd.Dir = filepath.Dir(req.PlanPath)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("chronon stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("chronon stderr pipe: %w", err)
	}

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	streamLines := func(r io.Reader, prefix string) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			lastActivity.Store(time.Now().UnixNano())
			log.Printf("[chronon %s] %s", prefix, line)
			if req.Progress != nil {
				if progress, ok := parseProgressLine(line, req.TotalFrames); ok {
					req.Progress(progress)
				}
			}
		}
	}

	renderStart := time.Now()
	log.Printf("[chronon] launching render: %s %s", c.Binary(), strings.Join(args, " "))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("chronon start: %w", err)
	}

	go streamLines(stdoutPipe, "stdout")
	go streamLines(stderrPipe, "stderr")

	// Stall watchdog goroutine
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-renderCtx.Done():
				return
			case <-ticker.C:
				last := time.Unix(0, lastActivity.Load())
				if time.Since(last) > stallTimeout {
					log.Printf("[chronon WARN] stall detected: no output for %v; aborting render", time.Since(last).Round(time.Second))
					cancel()
					return
				}
			}
		}
	}()

	err = cmd.Wait()
	duration := time.Since(renderStart)
	if err != nil {
		if renderCtx.Err() == context.Canceled && ctx.Err() == nil {
			return fmt.Errorf("chronon render stalled: no output for %v (aborted after %v)", stallTimeout, duration)
		}
		return fmt.Errorf("chronon execution failed after %v: %w", duration, err)
	}
	log.Printf("[chronon] render finished successfully in %v", duration.Round(time.Millisecond))
	return nil
}

var progressFrameRE = regexp.MustCompile(`(?i)\b(?:frames?_rendered|frames?_done|frame)\s*[:=]\s*(\d+)`)
var progressFPSRE = regexp.MustCompile(`(?i)\bfps\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)`)

func parseProgressLine(line string, total int64) (RenderProgress, bool) {
	match := progressFrameRE.FindStringSubmatch(line)
	if len(match) != 2 {
		return RenderProgress{}, false
	}
	frames, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return RenderProgress{}, false
	}
	progress := RenderProgress{FramesDone: frames, FramesTotal: total, At: time.Now().UTC()}
	if fps := progressFPSRE.FindStringSubmatch(line); len(fps) == 2 {
		progress.FPS, _ = strconv.ParseFloat(fps[1], 64)
	}
	return progress, true
}

// renderArgs builds the chronon3d_cli arguments for the render subcommand.
func renderArgs(req RenderRequest) []string {
	// Backend selection is an implementation detail of the CLI adapter. The
	// public request carries only semantic requirements; Chronon chooses the
	// concrete backend at this boundary.
	backend := "auto"
	if req.Requirements.GPURequired {
		backend = "vulkan"
	}
	args := []string{
		"render",
		"--plan", req.PlanPath,
		"--assets-root", req.AssetsRoot,
		"--backend", backend,
		"-o", req.OutputPath,
	}
	if req.Report {
		// Emit the execution report and telemetry JSONL (render_ms, encode_ms,
		// cache_hits/misses) used by the performance benchmark.
		args = append(args, "--report")
	}
	if req.Requirements.GPURequired {
		args = append(args, "--hardware", "nvenc")
	}
	if req.LastFrame >= req.FirstFrame && (req.FirstFrame != 0 || req.LastFrame != 0) {
		args = append(args, "--start-frame", fmt.Sprint(req.FirstFrame), "--end-frame", fmt.Sprint(req.LastFrame))
	}
	return args
}
