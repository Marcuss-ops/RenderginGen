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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chrononbin"
)

// DefaultStallTimeout is the maximum duration Chronon CLI can produce zero
// output or progress before being considered stalled. It is the value used when
// neither the configured StallTimeout nor the legacy CHRONON_STALL_TIMEOUT
// variable supplies one (see Client.StallTimeout).
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
	// JobID is queue correlation metadata, not render semantics: the IPC
	// transport sends it to the daemon so the progress watchdog can query
	// GET_JOB_STATUS for this render. Empty keeps the legacy untracked job.
	JobID    string
	PlanPath string // path to the render-plan document the worker wrote (plan.json)
	// PreparedPackagePath is the immutable overlay preparation sidecar written
	// beside PlanPath. Empty keeps compatibility for callers that only submit a
	// canonical render plan.
	PreparedPackagePath string
	AssetsRoot          string // directory the plan's relative asset references resolve against
	OutputPath          string // destination of the rendered output (e.g. result.mp4)
	// AudioSourcePath is an optional source media file whose audio stream is
	// muxed into the native video output. It is deliberately separate from
	// the visual render plan: Chronon renders video, while the native encoder
	// copies/transcodes the declared master audio stream.
	AudioSourcePath string
	// AudioTargetSampleRate is the OUTPUT sample rate (Hz) the muxed master
	// audio must carry, as declared by the sealed plan's audio block. 0 keeps
	// the legacy copy semantics (the artifact inherits the source rate).
	// Chronon only transcodes when the requested rate differs from the
	// source, so a 48 kHz source stays on the zero-cost copy path.
	AudioTargetSampleRate int
	// RangeEnabled marks FirstFrame/LastFrame as an explicit chunk range.
	// It exists because a chunk covering exactly frame 0 (FirstFrame=0,
	// LastFrame=0) is otherwise indistinguishable from "no range" — and a
	// missing range must render the WHOLE plan, never a single frame. When
	// RangeEnabled is false, FirstFrame/LastFrame are ignored.
	RangeEnabled bool
	FirstFrame   int64 // first frame to render (only when RangeEnabled)
	LastFrame    int64 // inclusive last frame to render (only when RangeEnabled)
	// ParallelDisjoint is a trusted execution hint emitted only for a chunk
	// child (parent id + explicit frame range). It is deliberately separate
	// from RangeEnabled: a range is a rendering semantic, while this flag is a
	// scheduler/admission contract. Whole-plan and legacy callers stay false.
	ParallelDisjoint bool
	Report           bool // emit the execution report + telemetry JSONL (--report)
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
	// EncoderBackend optionally overrides the automatic native backend for a
	// non-strict per-batch GPU route (for example Vulkan composition through
	// the pipe writer into NVENC). Empty keeps the profile-derived selection.
	EncoderBackend string
	// ReceiptVerify is the explicit output-verification policy
	// ("fast" | "normal" | "certify") RenderingGen requests for this render.
	// The worker is the single policy authority: it forwards the resolved
	// policy as CHRONON_RECEIPT_VERIFY on the subprocess environment so the
	// CLI can never silently drift from the worker (no reliance on ambient
	// env inheritance). Empty leaves the CLI's own default in place for
	// callers outside the worker pipeline.
	ReceiptVerify string
	// NoDirtyRects disables dirty-rectangle invalidation for this render. The
	// dirty-rect optimization is a performance feature, not a semantic one; a
	// software-composed plan whose layers overlap can leave a torn band at a
	// layer boundary, so a worker can opt out for those jobs. It travels on
	// both transports (--no-dirty-rects on the CLI, no_dirty_rects in the
	// RENDER_JOB payload) because the production hot path is the daemon.
	NoDirtyRects bool
	// Progress, when non-nil, receives a milestone snapshot every time the
	// renderer reports a frame position. It is invoked from the output
	// streaming goroutines and must be cheap and concurrency-safe.
	Progress func(RenderProgress)
	// LaneTiming is called inside the selected lane's admission boundary;
	// unlike processor-wide gaps it excludes time queued behind that lane.
	LaneTiming func(idle time.Duration)
	// Requirements are semantic and backend-neutral. Chronon resolves them
	// against the capabilities of the selected device.
	Requirements ExecutionRequirements
	Output       OutputSpec
	// OutputSegments requests the P4 batch-superjob contract: one MP4 per
	// item cut from ONE encoder session (frame windows ARE encoder PTS
	// windows; they must tile [0, TotalFrames) contiguously with unique
	// output paths). Empty preserves the single-output path. Video-only: it
	// is mutually exclusive with AudioSourcePath and with an explicit chunk
	// range, and it is supported on the daemon IPC transport only —
	// validateRenderRequest enforces the semantics, the transports enforce
	// their own capability.
	OutputSegments []OutputSegment
	TotalFrames    int64
}

// OutputSegment is one per-item output window of a segmented render: the
// item's frame interval [StartFrame, EndFrame) is cut out of the shared
// encoded timeline into its own MP4 with its own identity and retry. Frame
// units ARE Chronon's encoder PTS units (the codec time base is the output
// frame rate).
type OutputSegment struct {
	OutputPath string `json:"output_path"`
	StartFrame int64  `json:"start_frame"`
	EndFrame   int64  `json:"end_frame"` // exclusive
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
	// EncoderProfile optionally selects a certified encoder recipe. Empty
	// keeps the generic lane. "canonical_x264" asks the renderer to emit the
	// canonical single-session x264 stream itself (the same avcC, closed GOP
	// 48 and 1/90000 track timescale the post-render canonicalization pass
	// would otherwise have to produce with a second encode).
	EncoderProfile string `json:"encoder_profile,omitempty"`
	// FragmentedMP4 requests a FRAGMENTED MP4 (moof/mdat fragments, no
	// start-of-file backpatch) for the output container. The artifact bytes
	// are final the moment they are appended, so the upload lane can stream
	// and hash them while the mux is still open. Overlay artifacts — silent,
	// video-only intermediates — request it; a classic MP4 stays the default
	// for artifacts that mux source audio.
	FragmentedMP4 bool `json:"fragmented_mp4,omitempty"`
	// OutputSegments is the wire spelling of RenderRequest.OutputSegments
	// (P4 batch superjob). It lives in output_spec like the other output
	// contract fields; the daemon's closed vocabulary reads it here first and
	// falls back to the payload root.
	OutputSegments []OutputSegment `json:"output_segments,omitempty"`
}

// Renderer renders a RenderRequest.
type Renderer interface {
	Render(context.Context, RenderRequest) error
}

// OutputSegmentRenderer is the transport capability the P4 batch-superjob
// contract needs: a renderer whose wire can carry per-item output windows
// (one MP4 per item cut from ONE encoder session). The daemon IPC transport
// can (RENDER_JOB output_spec.output_segments); the CLI subprocess adapter
// cannot and refuses such requests explicitly. Wrappers (pools, spreaders,
// serializers) forward the capability to their lanes so the worker only
// segments a batch when the transport underneath can honour it.
type OutputSegmentRenderer interface {
	Renderer
	SupportsOutputSegments() bool
}

// BatchRenderer is the transport capability behind RENDER_BATCH: N independent
// RENDER_JOBs carried by one connection, one render domain. Callers type-assert
// it and fall back to per-job Render when the transport (the CLI subprocess
// adapter) has no batch spelling. Wrappers that forward other transport
// capabilities must forward this one too.
type BatchRenderer interface {
	Renderer
	RenderBatch(context.Context, []RenderRequest) ([]BatchRenderResult, error)
}

// SupportsOutputSegments: the CLI subprocess adapter has no spelling for
// per-item output windows (see Client.Render), so it declares the capability
// false and multi-item plans keep the single-output path there.
func (c *Client) SupportsOutputSegments() bool { return false }

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
	// StallTimeout is how long a render may produce no output at all before the
	// watchdog aborts it. Zero falls back to DefaultStallTimeout.
	//
	// It is a field rather than an os.Getenv read inside Render because the
	// worker configuration already owns the value (chronon.stall_timeout, with
	// the legacy CHRONON_STALL_TIMEOUT variable handled as an alias by the
	// config environment overlay). Reading the environment here as well made two
	// authorities for one knob, and left a bogus value to be discovered only in
	// a log line at render time.
	StallTimeout time.Duration
}

// Compile-time check that Client satisfies Renderer.
var _ Renderer = (*Client)(nil)

// Binary returns the path to the Chronon CLI. The resolution order lives in
// internal/chrononbin so the production client and the test gates cannot drift.
func (c *Client) Binary() string {
	return chrononbin.Resolve(c.Home, c.BinaryPath, os.Getenv("CHRONON_BINARY"))
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
type cliRenderProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
	ctx    context.Context
	cancel context.CancelFunc
	args   []string
}

func newCLIRenderProcess(ctx context.Context, c *Client, req RenderRequest) (*cliRenderProcess, error) {
	renderCtx, cancel := context.WithCancel(ctx)
	args := renderArgs(req)
	cmd := exec.CommandContext(renderCtx, c.Binary(), args...)
	cmd.Dir = filepath.Dir(req.PlanPath)
	if req.ReceiptVerify != "" {
		// Canonical verification boundary: RenderingGen requests the policy,
		// Chronon verifies. The explicit value makes a worker/CLI policy split
		// impossible.
		cmd.Env = append(os.Environ(), EnvReceiptVerify+"="+req.ReceiptVerify)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("chronon stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("chronon stderr pipe: %w", err)
	}
	return &cliRenderProcess{cmd: cmd, stdout: stdoutPipe, stderr: stderrPipe, ctx: renderCtx, cancel: cancel, args: args}, nil
}

func (c *Client) Render(ctx context.Context, req RenderRequest) error {
	if err := validateRenderRequest(req); err != nil {
		return err
	}
	if len(req.OutputSegments) > 0 {
		// The CLI argument set has no spelling for per-item output windows;
		// the daemon IPC transport carries them in output_spec.
		return fmt.Errorf("chronon: output segments are only supported on the daemon IPC transport, not the CLI subprocess adapter")
	}
	stallTimeout := c.StallTimeout
	if stallTimeout <= 0 {
		stallTimeout = DefaultStallTimeout
	}
	proc, err := newCLIRenderProcess(ctx, c, req)
	if err != nil {
		return err
	}
	defer proc.cancel()
	activity := newStallActivity(time.Now())
	var streamFailed atomic.Bool
	var failureDiagnostics renderFailureDiagnostics
	outputLog := newRenderOutputSampler(renderOutputLogInterval)
	renderStart := time.Now()
	log.Printf("[chronon] launching render: %s %s", c.Binary(), strings.Join(proc.args, " "))
	if err := proc.cmd.Start(); err != nil {
		return fmt.Errorf("chronon start: %w", err)
	}
	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go func() {
		defer streamWG.Done()
		streamRenderOutput(proc.stdout, "stdout", req, activity, outputLog, &failureDiagnostics, &streamFailed, proc.cancel)
	}()
	go func() {
		defer streamWG.Done()
		streamRenderOutput(proc.stderr, "stderr", req, activity, outputLog, &failureDiagnostics, &streamFailed, proc.cancel)
	}()
	// Stall watchdog: abort when output stops for longer than the configured
	// timeout. The sampler lives in stall_watch.go so it is unit-tested.
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	watchTicker := time.NewTicker(stallWatchInterval)
	defer watchTicker.Stop()
	watch := stallWatch{
		timeout:  stallTimeout,
		activity: activity,
		onStall: func(idle time.Duration) {
			log.Printf("[chronon WARN] stall detected: no output for %v; aborting render", idle.Round(time.Second))
			proc.cancel()
		},
	}
	go func() { watch.run(watchdogDone, watchTicker.C) }()
	streamWG.Wait()
	err = proc.cmd.Wait()
	return renderResult(ctx, proc.ctx, err, streamFailed.Load(), failureDiagnostics.snapshot(), stallTimeout, time.Since(renderStart))
}

func streamRenderOutput(r io.Reader, prefix string, req RenderRequest, activity *stallActivity, outputLog *renderOutputSampler, failureDiagnostics *renderFailureDiagnostics, streamFailed *atomic.Bool, cancel context.CancelFunc) {
	if err := scanRenderOutput(r, func(line string) {
		activity.touch(time.Now())
		if prefix == "stderr" {
			failureDiagnostics.remember(line)
		}
		if progress, ok := parseProgressLine(line, req.TotalFrames); ok {
			if outputLog.allowProgress(prefix) {
				log.Printf("[chronon %s] %s", prefix, line)
			} else {
				outputLog.remember(prefix, line)
			}
			if req.Progress != nil {
				req.Progress(progress)
			}
			return
		}
		// Anything that is not a frame milestone is a diagnostic and is always
		// forwarded.
		log.Printf("[chronon %s] %s", prefix, line)
	}); err != nil {
		// An output-stream error must abort loudly: silently stopping the
		// scanner would freeze progress and the stall heartbeat.
		streamFailed.Store(true)
		log.Printf("[chronon WARN] output stream %s failed: %v; aborting render", prefix, err)
		cancel()
	}
	if line, ok := outputLog.flush(prefix); ok {
		log.Printf("[chronon %s] %s (final)", prefix, line)
	}
}

func renderResult(ctx context.Context, renderCtx context.Context, err error, streamFailed bool, diagnostics string, stallTimeout, duration time.Duration) error {
	if err == nil {
		log.Printf("[chronon] render finished successfully in %v", duration.Round(time.Millisecond))
		return nil
	}
	if renderCtx.Err() == context.Canceled && ctx.Err() == nil {
		if streamFailed {
			return fmt.Errorf("chronon render aborted: output stream failure (aborted after %v): %w", duration, err)
		}
		return fmt.Errorf("chronon render stalled: no output for %v (aborted after %v)", stallTimeout, duration)
	}
	if diagnostics != "" {
		return fmt.Errorf("chronon execution failed after %v: %w (stderr: %s)", duration, err, diagnostics)
	}
	return fmt.Errorf("chronon execution failed after %v: %w", duration, err)
}

// Output streaming (scanRenderOutput), progress-line parsing and the CLI
// renderArgs translation live in output_scan.go and render_args.go.
