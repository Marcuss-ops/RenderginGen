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
	"os/exec"
	"path/filepath"
	"strings"
)

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
	return filepath.Join(c.Home, "bin", "chronon3d_cli")
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
// output path. It implements Renderer.
func (c *Client) Render(ctx context.Context, req RenderRequest) error {
	cmd := exec.CommandContext(ctx, c.Binary(), renderArgs(req)...)
	// Keep Chronon's execution report next to the plan. The worker invokes the
	// CLI from its own process directory otherwise, which makes the report
	// disappear with an ephemeral container and prevents E2E profiling.
	cmd.Dir = filepath.Dir(req.PlanPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
