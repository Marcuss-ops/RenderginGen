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
	"os"
	"path/filepath"
	"strings"
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
