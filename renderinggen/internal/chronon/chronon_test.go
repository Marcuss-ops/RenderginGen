package chronon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
			CompositionRequired: true, VideoSourceRequired: false, PacketCopyAllowed: true,
		},
	})
	want := []string{
		"render",
		"--plan", "/jobs/1/plan.json",
		"--assets-root", "/jobs/1/assets",
		"--backend", "vulkan",
		"-o", "/jobs/1/output/result.mp4",
		"--hardware", "nvenc",
		"--encoder-backend", "pipe",
		"--gpu-hot-path-mode", "auto",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renderArgs (GPU) = %#v, want %#v", got, want)
	}
}

func TestValidateEncodePresetAcceptance(t *testing.T) {
	// Empty preserves the engine default; every NVENC tier preset is valid.
	valid := append([]string{""}, ValidEncodePresets...)
	for _, preset := range valid {
		if err := ValidateEncodePreset(preset); err != nil {
			t.Fatalf("ValidateEncodePreset(%q) = %v, want nil", preset, err)
		}
	}
}

func TestValidateEncodePresetRejectsUnknownVocabulary(t *testing.T) {
	// The worker exposes only the p1..p7 NVENC tier; x264 names, legacy NVENC
	// aliases and typos must fail closed at config load.
	for _, preset := range []string{"banana", "fast", "slow", "medium", "hq", "llhq", "p0", "p8", "P2", "p2 ", " default"} {
		if err := ValidateEncodePreset(preset); err == nil {
			t.Fatalf("ValidateEncodePreset(%q) = nil, want error", preset)
		}
	}
}

func TestRenderArgsForwardsEncodePresetOnNativeVideoPath(t *testing.T) {
	got := renderArgs(RenderRequest{
		PlanPath:     "/jobs/1/plan.json",
		AssetsRoot:   "/jobs/1/assets",
		OutputPath:   "/jobs/1/output/result.mp4",
		EncodePreset: "p2",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, VideoSourceRequired: true, PacketCopyAllowed: true,
		},
	})
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"--hardware nvenc",
		"--encoder-backend native",
		"--gpu-hot-path-mode require_gpu_native",
		"--encode-preset p2",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("renderArgs=%q, want to contain %q", joined, want)
		}
	}
}

func TestRenderArgsOmitsEncodePresetOnPipePath(t *testing.T) {
	// The pipe branch targets x264 vocabulary; a pN NVENC preset must never
	// be forwarded there.
	got := renderArgs(RenderRequest{
		PlanPath:     "/jobs/1/plan.json",
		AssetsRoot:   "/jobs/1/assets",
		OutputPath:   "/jobs/1/output/result.mp4",
		EncodePreset: "p2",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, VideoSourceRequired: false, PacketCopyAllowed: true,
		},
	})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "--encode-preset") {
		t.Fatalf("renderArgs=%q must not forward encode preset on the pipe path", joined)
	}
}

func TestRenderArgsHonorsSoftwareBackendForComposition(t *testing.T) {
	args := renderArgs(RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1/assets", OutputPath: "/jobs/1/output/result.mp4",
		Requirements: ExecutionRequirements{Backend: "software", CompositionRequired: true},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--backend software") {
		t.Fatalf("args=%v, expected software backend", args)
	}
	if strings.Contains(joined, "--backend vulkan") {
		t.Fatalf("args=%v, software composition was forced to Vulkan", args)
	}
}

func TestBinaryPath(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "bin", "chronon3d_cli")
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{Home: home}
	if got := c.Binary(); got != path {
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
