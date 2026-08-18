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
	Backend    string // render backend: software | vulkan | auto
	Report     bool   // emit the execution report + telemetry JSONL (--report)
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// renderArgs builds the chronon3d_cli arguments for the render subcommand.
func renderArgs(req RenderRequest) []string {
	args := []string{
		"render",
		"--plan", req.PlanPath,
		"--assets-root", req.AssetsRoot,
		"--backend", req.Backend,
		"-o", req.OutputPath,
	}
	if req.Report {
		// Emit the execution report and telemetry JSONL (render_ms, encode_ms,
		// cache_hits/misses) used by the performance benchmark.
		args = append(args, "--report")
	}
	return args
}
