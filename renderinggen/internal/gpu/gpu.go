// Package gpu detects the available GPU and selects a render backend.
package gpu

import "os/exec"

// Info describes the detected GPU.
type Info struct {
	Present bool
	Backend string
	Device  int
	Driver  string
}

// Detect returns GPU info for the requested device index.
func Detect(device int) Info {
	return detect(device, hasCommand)
}

// detect is the testable core, taking a lookup function for PATH commands.
func detect(device int, has func(string) bool) Info {
	info := Info{Device: device}

	if has("vulkaninfo") || has("nvidia-smi") {
		info.Present = true
		info.Backend = "vulkan"
		info.Driver = detectDriver(has)
	}
	return info
}

func detectDriver(has func(string) bool) string {
	if has("nvidia-smi") {
		return "nvidia"
	}
	if has("vulkaninfo") {
		return "vulkan"
	}
	return "unknown"
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
