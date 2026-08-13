package gpu

import "testing"

func TestDetectWithNvidiaSMI(t *testing.T) {
	has := func(name string) bool { return name == "nvidia-smi" }
	info := detect(0, has)

	if !info.Present {
		t.Fatal("expected GPU present")
	}
	if info.Backend != "vulkan" {
		t.Fatalf("backend = %q", info.Backend)
	}
	if info.Driver != "nvidia" {
		t.Fatalf("driver = %q", info.Driver)
	}
	if info.Device != 0 {
		t.Fatalf("device = %d", info.Device)
	}
}

func TestDetectWithVulkaninfo(t *testing.T) {
	has := func(name string) bool { return name == "vulkaninfo" }
	info := detect(2, has)

	if !info.Present {
		t.Fatal("expected GPU present")
	}
	if info.Driver != "vulkan" {
		t.Fatalf("driver = %q", info.Driver)
	}
	if info.Device != 2 {
		t.Fatalf("device = %d", info.Device)
	}
}

func TestDetectNoGPU(t *testing.T) {
	has := func(string) bool { return false }
	info := detect(0, has)

	if info.Present {
		t.Fatal("expected no GPU")
	}
	if info.Backend != "" {
		t.Fatalf("backend = %q", info.Backend)
	}
	if info.Driver != "" {
		t.Fatalf("driver = %q", info.Driver)
	}
}
