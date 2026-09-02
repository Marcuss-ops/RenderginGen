package chronon

import (
	"reflect"
	"testing"
)

func TestRenderArgs(t *testing.T) {
	got := renderArgs(RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
	})
	want := []string{
		"render",
		"--plan", "/jobs/1/plan.json",
		"--assets-root", "/jobs/1/assets",
		"--backend", "auto",
		"-o", "/jobs/1/output/result.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renderArgs = %#v, want %#v", got, want)
	}
}

func TestRenderArgsChunkRange(t *testing.T) {
	got := renderArgs(RenderRequest{PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/output.mp4", FirstFrame: 240, LastFrame: 359})
	if !reflect.DeepEqual(got[len(got)-4:], []string{"--start-frame", "240", "--end-frame", "359"}) {
		t.Fatalf("chunk args = %#v", got)
	}
}

func TestRenderArgsReport(t *testing.T) {
	got := renderArgs(RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Report:     true,
	})
	want := []string{
		"render",
		"--plan", "/jobs/1/plan.json",
		"--assets-root", "/jobs/1/assets",
		"--backend", "auto",
		"-o", "/jobs/1/output/result.mp4",
		"--report",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renderArgs (report) = %#v, want %#v", got, want)
	}
}

func TestRenderArgsResolvesGPURequirementAtAdapterBoundary(t *testing.T) {
	got := renderArgs(RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, PacketCopyAllowed: true,
		},
	})
	want := []string{
		"render",
		"--plan", "/jobs/1/plan.json",
		"--assets-root", "/jobs/1/assets",
		"--backend", "vulkan",
		"-o", "/jobs/1/output/result.mp4",
		"--hardware", "nvenc",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renderArgs (GPU) = %#v, want %#v", got, want)
	}
}

func TestBinaryPath(t *testing.T) {
	c := &Client{Home: "/opt/chronon3d"}
	if got := c.Binary(); got != "/opt/chronon3d/bin/chronon3d_cli" {
		t.Fatalf("Binary() = %q", got)
	}
}

func TestBinaryPathOverride(t *testing.T) {
	t.Setenv("CHRONON_BINARY", "/tmp/chronon3d_cli")
	c := &Client{Home: "/opt/chronon3d"}
	if got := c.Binary(); got != "/tmp/chronon3d_cli" {
		t.Fatalf("Binary() with override = %q", got)
	}
}

func TestClientImplementsRenderer(t *testing.T) {
	var r Renderer = &Client{Home: "/opt/chronon3d"}
	if r == nil {
		t.Fatal("Client should satisfy Renderer")
	}
}
