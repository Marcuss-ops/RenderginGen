package chronon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cliAvailable returns a Client wired to the real chronon3d_cli binary, or
// skips the test when it is not installed. Set CHRONON_HOME to point at an
// alternate install prefix (default /opt/chronon3d, matching the runtime
// image).
func cliAvailable(t *testing.T) *Client {
	t.Helper()
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}
	return cli
}

// TestRenderIntegrationCLI renders the asset-free color smoke plan through the
// real chronon3d_cli binary and asserts a non-empty result.mp4 is produced.
// This proves the CLI contract used by the worker (render --plan --assets-root
// --backend -o) matches the real Chronon3d executable.
func TestRenderIntegrationCLI(t *testing.T) {
	cli := cliAvailable(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(dir, "plan.json")
	outputPath := filepath.Join(dir, "output", "result.mp4")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(ExampleColorSmokePlan), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := cli.Render(ctx, RenderRequest{
		PlanPath:   planPath,
		AssetsRoot: filepath.Join(dir, "assets"),
		OutputPath: outputPath,
		Backend:    "software",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	st, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("output %s: %v", outputPath, err)
	}
	if st.Size() == 0 {
		t.Fatalf("output %s is empty", outputPath)
	}
}
