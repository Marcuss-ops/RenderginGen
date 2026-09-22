package processor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRequireNativeVulkanCertifiesFromV2TimingSidecar pins the current-engine
// contract: Chronon3d emits the v2 frame-timing sidecar and (by its own
// architecture rule) never the legacy summary artifact, so a native render must
// still certify — from the sidecar's bounded summary/job sections.
func TestRequireNativeVulkanCertifiesFromV2TimingSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.mp4")
	sidecar := `{"schema":"chronon3d.frame-timing.v2","version":2,"summary":{"render_loop_fps":29.2},"job":{"execution_path":"full_graph_native","surface_handoff_path":"direct","gpu":{"effective_backend":"vulkan","encoder_backend":"nvenc","software_fallback_nodes":0,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"vulkan_frames":1,"gpu_native_surface_frames":0},"text":{"shaping_ms":1.5}},"frame_times_ms":[{"frame":0}]}`
	if err := os.WriteFile(path+".timing.json", []byte(sidecar), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".receipt.json", []byte(`{"copy_eligible":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireNativeVulkan(path, 1); err != nil {
		t.Fatalf("requireNativeVulkan() with a v2 sidecar: %v", err)
	}

	// The same sidecar reporting a software fallback must still fail closed.
	degraded := `{"schema":"chronon3d.frame-timing.v2","job":{"surface_handoff_path":"direct","gpu":{"effective_backend":"vulkan","encoder_backend":"nvenc","software_fallback_nodes":3,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"vulkan_frames":1}}}`
	if err := os.WriteFile(path+".timing.json", []byte(degraded), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireNativeVulkan(path, 1); err == nil {
		t.Fatal("a v2 sidecar reporting software fallback must not certify native")
	}

	// A v1 deep-profile document is not the current sidecar: no telemetry, no
	// certification.
	v1 := `{"schema":"chronon3d.frame-timing.v1","job":{"gpu":{"effective_backend":"vulkan"}}}`
	if err := os.WriteFile(path+".timing.json", []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireNativeVulkan(path, 1); err == nil {
		t.Fatal("a v1 timing document must not satisfy the native gate")
	}
}

func TestRequireNativeVulkan(t *testing.T) {
	const summarySchema = "chronon3d.render-telemetry-summary.v1"
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"native", `{"schema":"` + summarySchema + `","job":{"surface_handoff_path":"vulkan_copy","gpu":{"effective_backend":"vulkan","encoder_backend":"nvenc","software_fallback_nodes":0,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"vulkan_frames":1}}}`, true},
		{"direct_yuv", `{"schema":"` + summarySchema + `","job":{"execution_path":"direct_yuv","surface_handoff_path":"direct","gpu":{"effective_backend":"unknown","encoder_backend":"nvenc","software_fallback_nodes":0,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"gpu_native_surface_frames":1,"vulkan_frames":0}}}`, true},
		{"hybrid", `{"schema":"` + summarySchema + `","job":{"gpu":{"effective_backend":"hybrid","software_fallback_nodes":2}}}`, false},
		{"missing receipt field", `{"schema":"` + summarySchema + `","job":{"gpu":{"software_fallback_nodes":0}}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "result.mp4")
			if err := os.WriteFile(path+".telemetry-summary.json", []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			receipt := `{"copy_eligible":true}`
			if tc.name != "native" {
				receipt = `{"copy_eligible":false}`
			}
			if err := os.WriteFile(path+".receipt.json", []byte(receipt), 0o600); err != nil {
				t.Fatal(err)
			}
			got := requireNativeVulkan(path, 1) == nil
			if got != tc.want {
				t.Fatalf("requireNativeVulkan() = %v, want %v", got, tc.want)
			}
		})
	}
}
