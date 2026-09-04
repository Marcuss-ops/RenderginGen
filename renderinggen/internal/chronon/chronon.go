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
	// AudioSourcePath is an optional source media file whose audio stream is
	// muxed into the native video output. It is deliberately separate from
	// the visual render plan: Chronon renders video, while the native encoder
	// copies/transcodes the declared master audio stream.
	AudioSourcePath string
	FirstFrame      int64 // optional global first frame for chunk execution
	LastFrame       int64 // optional inclusive global last frame for chunk execution
	Report          bool  // emit the execution report + telemetry JSONL (--report)
	// Progress, when non-nil, receives a milestone snapshot every time the
	// renderer reports a frame position. It is invoked from the output
	// streaming goroutines and must be cheap and concurrency-safe.
	Progress func(RenderProgress)
	// Requirements are semantic and backend-neutral. Chronon resolves them
	// against the capabilities of the selected device.
	Requirements ExecutionRequirements
	Output       OutputSpec
	TotalFrames  int64
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
	VideoSourceRequired bool `json:"video_source_required"`
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
	Home                string
	Backend             string
	StrictNativeBackend bool
	HardwareEncoder     string
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

// Verify checks that the Chronon binary is present, executable, and reports
// the capabilities this worker's configuration requires. Capability parsing
// lives once in capabilities.go — this method is a thin config-specific
// wrapper over Capabilities(ctx), never a second decoder.
func (c *Client) Verify() error {
	p := c.Binary()
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("chronon binary missing at %s: %w", p, err)
	}
	if st.IsDir() {
		return fmt.Errorf("chronon binary %s is a directory", p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()

	caps, err := c.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("chronon capability verification failed: %w", err)
	}

	// Requirement derivation mirrors Processor.RunGPU's gpuRequired and
	// renderArgs' GPU branch: whenever the worker would force the native GPU
	// hot path (--backend vulkan --hardware nvenc --encoder-backend native
	// --gpu-hot-path-mode require_direct_yuv), the whole chain must be
	// declared and reachable up front. Anything less and the first job would
	// fail mid-queue instead of the worker refusing to start.
	gpuRequired := c.StrictNativeBackend ||
		(c.Backend == "vulkan" && c.HardwareEncoder != "" && c.HardwareEncoder != "none")
	req := Requirements{}
	if c.Backend == "vulkan" {
		req.Vulkan = true
	}
	if gpuRequired {
		req = GPUHotPathRequirements()
	}
	if err := caps.Validate(req); err != nil {
		return fmt.Errorf("chronon binary at %s: %w", p, err)
	}
	return nil
}

// Version returns the installed Chronon version. Source-tree installs ship
// a VERSION file; build-tree runtimes (cmake --preset linux-video-release)
// ship sdk_version.txt instead. Both are honored so the recorded version is
// never the silent "unknown" sentinel.
func (c *Client) Version() string {
	for _, name := range []string{"VERSION", "sdk_version.txt"} {
		data, err := os.ReadFile(filepath.Join(c.Home, name))
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(data)); v != "" {
			return v
		}
	}
	return "unknown"
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

// progressFrameRE matches the frame-position progress lines Chronon emits on
// stdout/stderr for both execution paths (pipe export and direct-YUV):
//
//	[video]   485/1800 frames
//	frames_done=485
//	frames rendered: 485
//
// It also captures an optional fps=<n> field on the same line. Memory-alloc
// lines ("allocated 1329 MiB VRAM") never match: GPU allocation is not
// progress evidence.
var progressFrameRE = regexp.MustCompile(`(?i)(?:\[\s*video\s*]\s*)?(\d+)\s*/\s*(\d+)\s+frames|\b(?:frames?_rendered|frames?_done|frame)\s*[:=]\s*(\d+)`)
var progressFPSRE = regexp.MustCompile(`(?i)\bfps\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)`)

func parseProgressLine(line string, total int64) (RenderProgress, bool) {
	match := progressFrameRE.FindStringSubmatch(line)
	if len(match) != 4 {
		return RenderProgress{}, false
	}
	progress := RenderProgress{FramesTotal: total, At: time.Now().UTC()}
	if match[1] != "" {
		// "[video] N/M frames" form: N is absolute, M is the authoritative total.
		done, err1 := strconv.ParseInt(match[1], 10, 64)
		tot, err2 := strconv.ParseInt(match[2], 10, 64)
		if err1 != nil || err2 != nil {
			return RenderProgress{}, false
		}
		progress.FramesDone, progress.FramesTotal = done, tot
	} else {
		frames, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			return RenderProgress{}, false
		}
		progress.FramesDone = frames
	}
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
	if req.Requirements.GPURequired || req.Requirements.CompositionRequired {
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
		// GPU requirements must select the complete native handoff. Passing
		// only --hardware leaves Chronon in its auto/direct-yuv resolver, which
		// can feed a decoded host surface to the native encoder. RenderingGen
		// has already declared the semantic requirement, so make that contract
		// explicit at the CLI boundary.
		args = append(args, "--hardware", "nvenc")
		// DirectYUV is the native fast path for plans with a video source.
		// Image/text-only compositions have no decoder surface to feed it, so
		// they must use Chronon's native composition graph instead.
		hotPath := "require_direct_yuv"
		if req.Requirements.CompositionRequired && !req.Requirements.VideoSourceRequired {
			// Image/text-only plans have no native decoder surface. Use the
			// Vulkan compositor and the supported FFmpeg pipe encoder.
			args = append(args, "--encoder-backend", "pipe")
			hotPath = "auto"
		} else {
			args = append(args, "--encoder-backend", "native")
		}
		args = append(args, "--gpu-hot-path-mode", hotPath)
	} else if req.Requirements.CompositionRequired {
		// Authored image/text-only compositions use Vulkan for rasterization and
		// the CLI's supported pipe encoder; no source-video NVENC contract is
		// applicable here.
		args = append(args, "--encoder-backend", "pipe", "--gpu-hot-path-mode", "auto")
	}
	if req.AudioSourcePath != "" {
		// Chronon's native A/V mux path uses --gop-source for the source audio
		// stream. The worker passes an absolute workspace path, so the CLI can
		// open it after materialization.
		args = append(args, "--gop-source", req.AudioSourcePath)
	}
	if req.LastFrame >= req.FirstFrame && (req.FirstFrame != 0 || req.LastFrame != 0) {
		args = append(args, "--start-frame", fmt.Sprint(req.FirstFrame), "--end-frame", fmt.Sprint(req.LastFrame))
	}
	return args
}
