// Package chronon wraps the Chronon GPU renderer CLI.
package chronon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client wraps the Chronon CLI binary installed in the worker image.
type Client struct {
	Home    string
	Backend string
}

// Binary returns the path to the Chronon CLI.
func (c *Client) Binary() string {
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

// Render invokes the CLI to render an overlay.
func (c *Client) Render(ctx context.Context, input, output string) error {
	cmd := exec.CommandContext(ctx, c.Binary(),
		"--backend", c.Backend,
		"--input", input,
		"--output", output,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
