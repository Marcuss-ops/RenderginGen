// Package chronon wraps the Chronon3d renderer.
//
// The worker's processing pipeline depends only on the Renderer interface, so
// it never knows whether rendering happens through the CLI subprocess (today)
// or a persistent daemon / IPC client (later). Chronon3d knows nothing about
// the queue, PipelineGen or the artifact store: it only receives a render plan
// + asset root + output path.
package chronon

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultStallTimeout is the maximum duration Chronon CLI can produce zero
// output or progress before being considered stalled.
const DefaultStallTimeout = 3 * time.Minute

// EnvReceiptVerify is the environment variable through which RenderingGen
// tells Chronon which output-verification policy to run (fast | normal |
// certify). It is set explicitly on the CLI subprocess by Client.Render; the
// worker never relies on ambient inheritance, so the two cannot drift. The
// daemon IPC path forwards the same policy per job (RenderRequest.ReceiptVerify
// → receipt_verify JSON field), so the CLI and daemon boundaries stay
// policy-identical.
const EnvReceiptVerify = "CHRONON_RECEIPT_VERIFY"

// maxRenderOutputLine caps the length of a single output line forwarded for
// progress/log processing. Chronon lines are short (frame milestones, log
// records); the cap is a guard against a pathological single line (a debug
// dump) freezing progress tracking and the stall heartbeat. Exceeding it
// aborts the render loudly instead of letting it stall into a misleading
// watchdog kill.
const maxRenderOutputLine = 8 << 20 // 8 MiB

// NVENC presets accepted by RenderingGen's encode_preset configuration.
// These are the worker's curated throughput/quality tier for h264_nvenc
// (Chronon's native encoder backend). The engine itself additionally accepts
// FFmpeg-style aliases such as "slow"/"medium"/"fast" and legacy NVENC
// names ("hq", "llhq", ...), but the worker contract deliberately exposes
// only the p1..p7 tier so a typo fails at config load, not on the first job.
var ValidEncodePresets = []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"}

// ValidateEncodePreset checks an encode_preset value against the worker's
// NVENC whitelist. Empty is valid (preserve the engine default). The check
// lives once in the chronon package — the same place that forwards the value
// to the CLI — so config validation and any future callers share one
// vocabulary instead of maintaining parallel switch statements.
func ValidateEncodePreset(preset string) error {
	if preset == "" {
		return nil
	}
	for _, candidate := range ValidEncodePresets {
		if preset == candidate {
			return nil
		}
	}
	return fmt.Errorf("chronon.encode_preset must be one of %v or empty (engine default), got %q", ValidEncodePresets, preset)
}

// RenderRequest is the renderable contract between RenderingGen and a
// Chronon3d backend. The render plan is already on disk (plan.json), assets
// are already materialized under AssetsRoot, and OutputPath is where the
// rendered file must be written.
type RenderRequest struct {
	PlanPath   string // path to the render-plan document the worker wrote (plan.json)
	AssetsRoot string // directory the plan's relative asset references resolve against
	OutputPath string // destination of the rendered output (e.g. result.mp4)
	// AudioSourcePath is an optional source media file whose audio stream is
	// muxed into the native video output. It is deliberately separate from
	// the visual render plan: Chronon renders video, while the native encoder
	// copies/transcodes the declared master audio stream.
	AudioSourcePath string
	// RangeEnabled marks FirstFrame/LastFrame as an explicit chunk range.
	// It exists because a chunk covering exactly frame 0 (FirstFrame=0,
	// LastFrame=0) is otherwise indistinguishable from "no range" — and a
	// missing range must render the WHOLE plan, never a single frame. When
	// RangeEnabled is false, FirstFrame/LastFrame are ignored.
	RangeEnabled bool
	FirstFrame   int64 // first frame to render (only when RangeEnabled)
	LastFrame    int64 // inclusive last frame to render (only when RangeEnabled)
	Report       bool  // emit the execution report + telemetry JSONL (--report)
	// EncodePreset is an explicit FFmpeg NVENC preset (e.g. "p2") forwarded
	// to the chronon CLI for native GPU jobs. Empty preserves the engine
	// default.
	EncodePreset string
	// HardwareEncoder is the FFmpeg hardware encoder the configured worker
	// selected (e.g. "nvenc"). Empty preserves the native default for the
	// GPU-required path; "none" disables the GPU handoff entirely (the
	// requirements then never carry GPURequired). It exists so the configured
	// value is actually forwarded to the CLI instead of being accepted by the
	// config and silently ignored at the render boundary.
	HardwareEncoder string
	// ReceiptVerify is the explicit output-verification policy
	// ("fast" | "normal" | "certify") RenderingGen requests for this render.
	// The worker is the single policy authority: it forwards the resolved
	// policy as CHRONON_RECEIPT_VERIFY on the subprocess environment so the
	// CLI can never silently drift from the worker (no reliance on ambient
	// env inheritance). Empty leaves the CLI's own default in place for
	// callers outside the worker pipeline.
	ReceiptVerify string
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
	// Backend is the configured Chronon implementation. Empty preserves the
	// historical auto-selection behavior for callers that do not choose one.
	Backend             string `json:"backend,omitempty"`
	GPURequired         bool   `json:"gpu_required"`
	CPUFallbackAllowed  bool   `json:"cpu_fallback_allowed"`
	CompositionRequired bool   `json:"composition_required"`
	VideoSourceRequired bool   `json:"video_source_required"`
	PacketCopyAllowed   bool   `json:"packet_copy_allowed"`
}

type OutputSpec struct {
	Codec      string `json:"codec,omitempty"`
	Width      uint32 `json:"width,omitempty"`
	Height     uint32 `json:"height,omitempty"`
	FPSNum     uint32 `json:"fps_num,omitempty"`
	FPSDen     uint32 `json:"fps_den,omitempty"`
	PipePixFmt string `json:"pipe_pixfmt,omitempty"`
	// HardwareEncoder / EncoderBackend / GpuHotPathMode carry the native
	// encoder selection across the IPC transport.
	//
	// They live in output_spec on purpose: the Chronon daemon resolves them
	// with spec_or_root(request, ...), i.e. it reads the output_spec sub-object
	// and falls back to the payload root. Sending them as TOP-LEVEL payload keys
	// is a contract violation (see TestIPCClientRenderPayloadContract), and
	// sending them NOWHERE — which is what this adapter used to do — left the
	// daemon with no encoder request at all, so it silently resolved the
	// software FFmpeg pipe lane and then handed the NVENC-only --encode-preset
	// ("p2") to libx264, which rejects it outright. They are derived from the
	// same authority as the CLI arguments (resolveNativeEncodeSelection) so the
	// CLI and IPC transports cannot drift.
	HardwareEncoder string `json:"hardware_encoder,omitempty"`
	EncoderBackend  string `json:"encoder_backend,omitempty"`
	GpuHotPathMode  string `json:"gpu_hot_path_mode,omitempty"`
}

// Renderer renders a RenderRequest.
type Renderer interface {
	Render(context.Context, RenderRequest) error
}

// AssetPrefetcher imports a materialized asset into a persistent renderer's
// process-local cache. It is deliberately separate from Renderer so CLI
// renderers and test doubles do not need a warm-up implementation.
type AssetPrefetcher interface {
	PrefetchAsset(context.Context, string) error
}

// Client renders through the Chronon3d CLI binary installed in the worker
// image. It implements Renderer.
type Client struct {
	Home string
	// BinaryPath is the explicit chronon3d_cli path for this deployment. It
	// is intentionally separate from Home: native hosts commonly install the
	// executable in /usr/local/bin while container runtimes use /opt/chronon3d.
	// CHRONON_BINARY remains the highest-priority emergency/test override.
	BinaryPath          string
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
	if c.BinaryPath != "" {
		return c.BinaryPath
	}
	p := filepath.Join(c.Home, "bin", "chronon3d_cli")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	p = filepath.Join(c.Home, "apps", "chronon3d_cli", "chronon3d_cli")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	// Keep the conventional Home/bin path as the diagnostic fallback when no
	// candidate exists, so the startup error still names the configured prefix.
	return filepath.Join(c.Home, "bin", "chronon3d_cli")
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
	if st.Mode()&0o111 == 0 {
		return fmt.Errorf("chronon binary %s is not executable", p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()

	caps, err := c.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("chronon capability verification failed: %w", err)
	}

	// Whenever the worker requests the native GPU capability (--backend
	// vulkan --hardware nvenc --encoder-backend native --gpu-hot-path-mode
	// auto), the whole chain must be declared and reachable up front. The rule
	// itself lives in StrictNativeRequired, shared with Processor.RunGPU and
	// renderArgs. Chronon still chooses DirectYUV vs FullGraph per compiled job.
	gpuRequired := c.StrictNativeBackend ||
		StrictNativeRequired(c.Backend, c.HardwareEncoder)
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
	if err := validateRenderRequest(req); err != nil {
		return err
	}
	stallTimeout := DefaultStallTimeout
	if env := os.Getenv("CHRONON_STALL_TIMEOUT"); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			stallTimeout = d
		} else {
			// Never silently ignore a misconfiguration: an operator who set a
			// bogus value must learn the default was applied instead of
			// assuming their timeout took effect.
			log.Printf("[chronon WARN] ignoring invalid CHRONON_STALL_TIMEOUT=%q: %v; using default %v", env, err, DefaultStallTimeout)
		}
	}

	renderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := renderArgs(req)
	cmd := exec.CommandContext(renderCtx, c.Binary(), args...)
	cmd.Dir = filepath.Dir(req.PlanPath)
	if req.ReceiptVerify != "" {
		// Canonical verification boundary: RenderingGen requests the policy,
		// Chronon verifies. Passing the resolved policy explicitly (rather
		// than inheriting whatever CHRONON_RECEIPT_VERIFY the ambient
		// environment holds) makes a worker/CLI policy split impossible. The
		// IPC daemon path is unaffected: the daemon reads the same policy
		// from the worker env at startup, and the production hot path is CLI
		// mode.
		cmd.Env = append(os.Environ(), EnvReceiptVerify+"="+req.ReceiptVerify)
	}

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

	var streamFailed atomic.Bool
	streamLines := func(r io.Reader, prefix string) {
		if err := scanRenderOutput(r, func(line string) {
			lastActivity.Store(time.Now().UnixNano())
			log.Printf("[chronon %s] %s", prefix, line)
			if req.Progress != nil {
				if progress, ok := parseProgressLine(line, req.TotalFrames); ok {
					req.Progress(progress)
				}
			}
		}); err != nil {
			// An output-stream error (a single line beyond the cap, or a pipe
			// failure) must abort loudly. Silently stopping the scanner would
			// freeze progress and the stall heartbeat and end in a misleading
			// watchdog kill of a healthy render.
			streamFailed.Store(true)
			log.Printf("[chronon WARN] output stream %s failed: %v; aborting render", prefix, err)
			cancel()
		}
	}

	renderStart := time.Now()
	log.Printf("[chronon] launching render: %s %s", c.Binary(), strings.Join(args, " "))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("chronon start: %w", err)
	}

	// Render must not return while a scanner is still running: the Progress
	// callback is invoked from these goroutines, so returning early lets the
	// caller read its final observation (gpu_run's lastProgress / ledger
	// metrics) while a goroutine is still writing it — a data race that also
	// silently drops the last milestone near exit. The scanners must also
	// finish BEFORE cmd.Wait(): Wait closes the pipe read ends once the child
	// exits, discarding any lines the child had already written but the
	// scanner had not yet consumed. A scanner reaches EOF on its own when the
	// child exit closes the write ends, so draining first is both lossless and
	// deadlock-free (the stall watchdog still kills a silent child).
	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go func() { defer streamWG.Done(); streamLines(stdoutPipe, "stdout") }()
	go func() { defer streamWG.Done(); streamLines(stderrPipe, "stderr") }()

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

	streamWG.Wait()
	err = cmd.Wait()
	duration := time.Since(renderStart)
	if err != nil {
		if renderCtx.Err() == context.Canceled && ctx.Err() == nil {
			if streamFailed.Load() {
				return fmt.Errorf("chronon render aborted: output stream failure (aborted after %v): %w", duration, err)
			}
			return fmt.Errorf("chronon render stalled: no output for %v (aborted after %v)", stallTimeout, duration)
		}
		return fmt.Errorf("chronon execution failed after %v: %w", duration, err)
	}
	log.Printf("[chronon] render finished successfully in %v", duration.Round(time.Millisecond))
	return nil
}

// Output streaming (scanRenderOutput), progress-line parsing and the CLI
// renderArgs translation live in output_scan.go and render_args.go.
