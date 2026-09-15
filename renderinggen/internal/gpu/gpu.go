// Package gpu detects the available GPU and selects a render backend.
//
// Detection is a real query, not a PATH lookup. The previous implementation
// reported "GPU present, backend=vulkan" whenever `vulkaninfo` OR `nvidia-smi`
// happened to be installed — an installed driver utility is not a usable
// device, so a host with the tools but no device (a container without the
// runtime, a CPU-only VM with the packages pulled in as a dependency) was
// registered as a Vulkan worker, took GPU work, and failed it at render time.
// The probe now has to read an actual device table and find the REQUESTED
// index in it.
package gpu

import (
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds each detection command. A hung driver utility must not
// hold up worker startup: an unreadable device table is reported as "not
// detected", which is the same fail-closed outcome as an absent device.
const probeTimeout = 5 * time.Second

// Info describes the detected GPU.
type Info struct {
	Present   bool
	Backend   string
	Device    int
	Driver    string
	Name      string
	MemoryMiB int
	// Reason explains why nothing was detected. Empty when Present is true.
	// It is surfaced in the startup log so an operator can tell "no driver" from
	// "the requested device index does not exist".
	Reason string
}

// runner runs a probe command and returns its stdout. It is the seam the tests
// substitute, so detection is exercised without a GPU.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Detect returns GPU info for the requested device index.
func Detect(device int) Info {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return detect(ctx, device, defaultRunner)
}

// detect probes the device, preferring the vendor query (it names the device and
// its VRAM) and falling back to the Vulkan summary.
func detect(ctx context.Context, device int, run runner) Info {
	info := Info{Device: device}
	if vendor, ok := detectNVidia(ctx, device, run); ok {
		return vendor
	}
	if vulkan, ok := detectVulkan(ctx, device, run); ok {
		return vulkan
	}
	info.Reason = fmt.Sprintf("no device at index %d in the nvidia-smi device table or the vulkaninfo summary", device)
	return info
}

// detectNVidia reads the vendor device table and returns the row for the
// requested index. ok is false when the tool is unavailable, its output cannot
// be read, its row does not have the shape this query asked for, or it has no
// such device.
func detectNVidia(ctx context.Context, device int, run runner) (Info, bool) {
	out, err := run(ctx, "nvidia-smi",
		"--query-gpu=name,driver_version,memory.total",
		"--format=csv,noheader,nounits")
	if err != nil {
		return Info{}, false
	}
	rows := parseCSVRows(string(out))
	if device < 0 || device >= len(rows) {
		return Info{}, false
	}
	row := rows[device]
	// The row must actually answer the query (name, driver_version,
	// memory.total). Checking only "the name is non-empty" accepted any
	// comma-separated line as a device, so an unrelated tool that happens to
	// print CSV would have been reported as a GPU; this requires the memory
	// column to be the number the query asked for.
	if len(row) < 3 {
		return Info{}, false
	}
	name := strings.TrimSpace(row[0])
	driver := strings.TrimSpace(row[1])
	// memory.total is reported in MiB because of the nounits modifier.
	mib, convErr := strconv.Atoi(strings.TrimSpace(row[2]))
	if name == "" || driver == "" || convErr != nil {
		return Info{}, false
	}
	return Info{
		Present:   true,
		Backend:   "vulkan",
		Driver:    "nvidia",
		Device:    device,
		Name:      name,
		MemoryMiB: mib,
	}, true
}

// detectVulkan counts the device names in the Vulkan summary and returns the
// requested index. This covers non-NVIDIA Vulkan devices (and NVIDIA hosts whose
// driver utility is named differently), while still refusing to report a device
// that the summary does not actually list.
func detectVulkan(ctx context.Context, device int, run runner) (Info, bool) {
	out, err := run(ctx, "vulkaninfo", "--summary")
	if err != nil {
		return Info{}, false
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "deviceName") {
			continue
		}
		_, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if name := strings.TrimSpace(value); name != "" {
			names = append(names, name)
		}
	}
	if device < 0 || device >= len(names) {
		return Info{}, false
	}
	return Info{
		Present: true,
		Backend: "vulkan",
		Driver:  "vulkan",
		Device:  device,
		Name:    names[device],
	}, true
}

// parseCSVRows parses a noheader CSV document into rows, dropping the blank
// trailing line nvidia-smi emits.
//
// A table that could only be read in part still yields the rows that parsed:
// ReadAll returns the records it managed to read before the error, and the
// caller falls through to the Vulkan probe whenever the requested index is not
// among them — so an unreadable table degrades to "try the other probe", never
// to a fabricated device.
func parseCSVRows(doc string) [][]string {
	reader := csv.NewReader(strings.NewReader(doc))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil && len(rows) == 0 {
		return nil
	}
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if len(row) == 1 && strings.TrimSpace(row[0]) == "" {
			continue
		}
		out = append(out, row)
	}
	return out
}
