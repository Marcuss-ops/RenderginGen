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
	got := renderArgs(RenderRequest{PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/output.mp4", RangeEnabled: true, FirstFrame: 240, LastFrame: 359})
	if !reflect.DeepEqual(got[len(got)-4:], []string{"--start-frame", "240", "--end-frame", "359"}) {
		t.Fatalf("chunk args = %#v", got)
	}
}

// TestRenderArgsFrameZeroRange pins the sentinel-collision fix: a chunk
// covering exactly frame 0 (Start=0, End=1 -> inclusive last frame 0) must
// reach the CLI as an explicit --start-frame 0 --end-frame 0 range — never be
// dropped and silently render the whole plan, and never be treated as a range
// when RangeEnabled is false (whole-plan renders stay default).
func TestRenderArgsFrameZeroRange(t *testing.T) {
	got := renderArgs(RenderRequest{PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/output.mp4", RangeEnabled: true, FirstFrame: 0, LastFrame: 0})
	if !reflect.DeepEqual(got[len(got)-4:], []string{"--start-frame", "0", "--end-frame", "0"}) {
		t.Fatalf("frame-zero chunk args = %#v", got)
	}
	// RangeEnabled=false with the same coordinates must omit the range
	// entirely (whole-plan render), so default-constructed requests never
	// accidentally render a single frame.
	whole := renderArgs(RenderRequest{PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/output.mp4"})
	for _, arg := range whole {
		if arg == "--start-frame" {
			t.Fatalf("whole-plan render must not emit a frame range: %#v", whole)
		}
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
	// CPUFallbackAllowed=false is the strict-native contract. It must produce
	// require_gpu_native, not "auto": with "auto" an image/text-only
	// composition (no source video) resolves to the host-frame FFmpeg pipe lane
	// and encodes with libx264, silently degrading the strict profile (and then
	// failing on the NVENC-only --encode-preset).
	want := []string{
		"render",
		"--plan", "/jobs/1/plan.json",
		"--assets-root", "/jobs/1/assets",
		"--backend", "vulkan",
		"-o", "/jobs/1/output/result.mp4",
		"--hardware", "nvenc",
		"--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renderArgs (GPU) = %#v, want %#v", got, want)
	}
}

// TestRenderArgsKeepsAutoWhenCPUFallbackIsAllowed pins the other half of the
// contract: a caller that PERMITS CPU fallback keeps Chronon's own "auto"
// classification, so the strict requirement is opt-in via the semantic
// requirement and never hardcoded onto every GPU request.
func TestRenderArgsKeepsAutoWhenCPUFallbackIsAllowed(t *testing.T) {
	got := renderArgs(RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: true,
			CompositionRequired: true, PacketCopyAllowed: true,
		},
	})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--gpu-hot-path-mode auto") {
		t.Fatalf("renderArgs=%q, want --gpu-hot-path-mode auto when CPU fallback is allowed", joined)
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

// TestRenderArgsForwardsConfiguredHardwareEncoder pins the boundary fix: the
// encoder selected by the worker configuration is what the CLI is asked to
// run with. It used to be hardcoded to nvenc, which made chronon.
// hardware_encoder a config value with no effect. An empty value must still
// resolve to the native default so the GPU contract is unchanged for workers
// that do not select one.
func TestRenderArgsForwardsConfiguredHardwareEncoder(t *testing.T) {
	gpu := ExecutionRequirements{
		GPURequired: true, CPUFallbackAllowed: false,
		CompositionRequired: true, VideoSourceRequired: true, PacketCopyAllowed: true,
	}

	configured := renderArgs(RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1/assets",
		OutputPath:      "/jobs/1/output/result.mp4",
		HardwareEncoder: "nvenc_custom", Requirements: gpu,
	})
	if joined := strings.Join(configured, " "); !strings.Contains(joined, "--hardware nvenc_custom") {
		t.Fatalf("renderArgs=%q, want the configured encoder forwarded", joined)
	}

	defaulted := renderArgs(RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4", Requirements: gpu,
	})
	if joined := strings.Join(defaulted, " "); !strings.Contains(joined, "--hardware nvenc") {
		t.Fatalf("renderArgs=%q, want the native default when no encoder is configured", joined)
	}
}

func TestRenderArgsForwardsPipePixelFormat(t *testing.T) {
	args := renderArgs(RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/output.mp4",
		Output:       OutputSpec{PipePixFmt: "p010"},
		Requirements: ExecutionRequirements{CompositionRequired: true},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--pipe-pixfmt p010") {
		t.Fatalf("renderArgs=%q, want explicit pipe format", joined)
	}
}

func TestRenderArgsForwardsEncodePresetOnNativeImageComposition(t *testing.T) {
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
	// This is the exact GoldenSemanticOverlayJobV1 shape: image + text, no
	// source video, strict native. It previously expected "auto", which let
	// Chronon resolve the host-frame FFmpeg pipe lane and hand the NVENC-only
	// preset to libx264 ("preset p2 is not valid for the software (libx264)
	// encoder"), failing the render. Strict-native must ask for the native lane
	// explicitly.
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

func TestBinaryPathExplicit(t *testing.T) {
	c := &Client{
		Home:       "/opt/chronon3d",
		BinaryPath: "/usr/local/bin/chronon3d_cli",
	}
	if got := c.Binary(); got != c.BinaryPath {
		t.Fatalf("Binary() with explicit path = %q, want %q", got, c.BinaryPath)
	}
}

func TestClientImplementsRenderer(t *testing.T) {
	var r Renderer = &Client{Home: "/opt/chronon3d"}
	if r == nil {
		t.Fatal("Client should satisfy Renderer")
	}
}
