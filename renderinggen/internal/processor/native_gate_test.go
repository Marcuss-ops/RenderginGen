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
		{"native", `{"job":{"gpu":{"effective_backend":"vulkan","software_fallback_nodes":0}}}`, true},
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
			got := requireNativeVulkan(path) == nil
			if got != tc.want {
				t.Fatalf("requireNativeVulkan() = %v, want %v", got, tc.want)
			}
		})
	}
}
