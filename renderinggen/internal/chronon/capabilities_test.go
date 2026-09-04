package chronon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCLI writes an executable shell script that emits a canned doctor --json
// document, so the handshake is testable without a real Chronon build. It
// relies on CHRONON_BINARY being honored by Client.Binary().
func fakeCLI(t *testing.T, doctorOutput string) *Client {
	t.Helper()
	if doctorOutput == "" {
		// A binary whose doctor invocation fails (not executable).
		return &Client{Home: t.TempDir()}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'EOF'\n"+doctorOutput+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHRONON_BINARY", script)
	return &Client{Home: dir}
}

const doctorReadyDoc = `{
  "ready": true,
  "capabilities": {
    "vulkan": true,
    "cuda_interop": true,
    "direct_yuv": true,
    "native_ffmpeg": true,
    "nvdec": true,
    "nvenc": true,
    "ipc": false,
    "build_sha": "abc1234"
  },
  "checks": [
    {"id": "encoder.nvenc", "status": "pass", "message": "h264_nvenc hardware encoder available"}
  ]
}`

func TestCapabilitiesParsesDoctorJSON(t *testing.T) {
	caps, err := fakeCLI(t, doctorReadyDoc).Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Vulkan || !caps.CUDAInterop || !caps.DirectYUV || !caps.NativeFFmpeg || !caps.NVDEC || !caps.NVENC {
		t.Fatalf("capabilities not fully parsed: %+v", caps)
	}
	if caps.IPC {
		t.Fatalf("ipc should be false for a CLI build")
	}
	if caps.BuildSHA != "abc1234" {
		t.Fatalf("build_sha = %q", caps.BuildSHA)
	}
}

func TestCapabilitiesMissingCapabilitiesBlock(t *testing.T) {
	// An old binary that prints checks but no capabilities block.
	doc := `{"ready": true, "checks": []}`
	if _, err := fakeCLI(t, doc).Capabilities(context.Background()); err == nil {
		t.Fatal("expected an error for a doctor document without capabilities")
	}
}

func TestCapabilitiesDoctorFailure(t *testing.T) {
	if _, err := fakeCLI(t, "").Capabilities(context.Background()); err == nil {
		t.Fatal("expected an error when the doctor invocation fails")
	}
}

func TestValidateGPUHotPathSatisfied(t *testing.T) {
	caps, err := fakeCLI(t, doctorReadyDoc).Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(GPUHotPathRequirements()); err != nil {
		t.Fatalf("GPU hot path requirements should be satisfied: %v", err)
	}
}

func TestValidateFailsOnNotReadyEnvironment(t *testing.T) {
	doc := strings.Replace(doctorReadyDoc, `"ready": true`, `"ready": false`, 1)
	caps, err := fakeCLI(t, doc).Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = caps.Validate(Requirements{})
	if err == nil || !strings.Contains(err.Error(), "NOT ready") {
		t.Fatalf("expected NOT-ready error, got %v", err)
	}
}

func TestValidateNVENCRequiresRuntimeProbe(t *testing.T) {
	// Declared nvenc=true (build-time flag) but the runtime probe failed.
	doc := `{
	  "ready": true,
	  "capabilities": {"vulkan": true, "cuda_interop": true, "direct_yuv": true, "native_ffmpeg": true, "nvdec": true, "nvenc": true, "ipc": false, "build_sha": "x"},
	  "checks": [
	    {"id": "encoder.nvenc", "status": "fail", "message": "h264_nvenc encoder unavailable"}
	  ]
	}`
	caps, err := fakeCLI(t, doc).Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = caps.Validate(GPUHotPathRequirements())
	if err == nil || !strings.Contains(err.Error(), "encoder.nvenc") {
		t.Fatalf("expected nvenc runtime-probe failure, got %v", err)
	}
	// The same declaration satisfies a requirement set that does not need NVENC.
	if err := caps.Validate(VulkanOnlyRequirements()); err != nil {
		t.Fatalf("vulkan-only requirements should ignore the nvenc probe: %v", err)
	}
}

func TestValidateListsAllMissingCapabilities(t *testing.T) {
	doc := `{
	  "ready": true,
	  "capabilities": {"vulkan": false, "cuda_interop": false, "direct_yuv": false, "native_ffmpeg": false, "nvdec": false, "nvenc": false, "ipc": false, "build_sha": "x"},
	  "checks": [{"id": "encoder.nvenc", "status": "skip", "message": "no ffmpeg"}]
	}`
	caps, err := fakeCLI(t, doc).Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = caps.Validate(GPUHotPathRequirements())
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"vulkan", "cuda_interop", "direct_yuv", "native_ffmpeg", "nvdec", "nvenc"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %q: %v", want, err)
		}
	}
}

func TestVerifyFailsFastOnIncompatibleBinary(t *testing.T) {
	// The regression this whole handshake exists for: a binary compiled
	// without CUDA interop must fail Verify() at startup, never mid-queue.
	doc := `{
	  "ready": true,
	  "capabilities": {"vulkan": true, "cuda_interop": false, "direct_yuv": false, "native_ffmpeg": true, "nvdec": false, "nvenc": true, "ipc": false, "build_sha": "x"},
	  "checks": [{"id": "encoder.nvenc", "status": "pass", "message": "ok"}]
	}`
	cli := fakeCLI(t, doc)
	cli.Backend = "vulkan"
	cli.StrictNativeBackend = true
	cli.HardwareEncoder = "nvenc"
	err := cli.Verify()
	if err == nil {
		t.Fatal("expected Verify to reject a binary without cuda_interop")
	}
	if !strings.Contains(err.Error(), "cuda_interop") {
		t.Fatalf("error should name cuda_interop: %v", err)
	}
}

func TestVerifyPassesOnCompatibleBinary(t *testing.T) {
	cli := fakeCLI(t, doctorReadyDoc)
	cli.Backend = "vulkan"
	cli.StrictNativeBackend = true
	cli.HardwareEncoder = "nvenc"
	if err := cli.Verify(); err != nil {
		t.Fatalf("Verify should pass: %v", err)
	}
}

func TestVerifyPresenceOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "chronon3d_cli"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No CHRONON_BINARY override: Verify falls through to the capability
	// handshake and fails on the missing doctor output — but the presence
	// error path is what TestVerifyFailsFastOnIncompatibleBinary's sibling
	// guards, so here we only assert a directory is rejected.
	t.Setenv("CHRONON_BINARY", dir)
	cli := &Client{Home: dir}
	if err := cli.Verify(); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory rejection, got %v", err)
	}
}

func TestDoctorJSONDecodeShape(t *testing.T) {
	var doc doctorJSON
	if err := json.Unmarshal([]byte(doctorReadyDoc), &doc); err != nil {
		t.Fatalf("doctorJSON shape drifted: %v", err)
	}
	if len(doc.Checks) == 0 || doc.Checks[0].ID != "encoder.nvenc" {
		t.Fatalf("checks decode drift: %+v", doc.Checks)
	}
}
