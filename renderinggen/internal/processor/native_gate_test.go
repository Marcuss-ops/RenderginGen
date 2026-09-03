package processor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequireNativeVulkan(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"native", `{"job":{"surface_handoff_path":"vulkan_copy","gpu":{"effective_backend":"vulkan","encoder_backend":"nvenc","software_fallback_nodes":0,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"vulkan_frames":1}}}`, true},
		{"direct_yuv", `{"job":{"execution_path":"direct_yuv","surface_handoff_path":"direct","gpu":{"effective_backend":"unknown","encoder_backend":"nvenc","software_fallback_nodes":0,"cpu_readback_frames":0,"software_encode_frames":0,"nvenc_frames":1,"gpu_native_surface_frames":1,"vulkan_frames":0}}}`, true},
		{"hybrid", `{"job":{"gpu":{"effective_backend":"hybrid","software_fallback_nodes":2}}}`, false},
		{"missing receipt field", `{"job":{"gpu":{"software_fallback_nodes":0}}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "result.mp4")
			if err := os.WriteFile(path+".timing.json", []byte(tc.body), 0o600); err != nil {
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
