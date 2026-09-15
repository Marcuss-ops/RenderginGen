package gpu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner answers the probe commands from a fixture table. A command that is
// not in the table fails the way a missing binary does.
type fakeRunner struct {
	outputs map[string]string
	calls   []string
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if out, ok := f.outputs[name]; ok {
		return []byte(out), nil
	}
	return nil, errors.New("executable file not found in $PATH")
}

const nvidiaSmiTwoGPUs = `NVIDIA GeForce RTX A4000, 550.54.14, 16376
NVIDIA GeForce RTX 4090, 550.54.14, 24564
`

const vulkaninfoOneDevice = `==========
VULKANINFO
==========

Devices:
========
GPU0:
	deviceName        = NVIDIA GeForce RTX A4000
	driverName        = NVIDIA
`

// TestDetectUsesNVidiaDeviceTable pins that detection reads a real device
// table: the name, driver and VRAM of the REQUESTED index, not merely the
// presence of a driver utility.
func TestDetectUsesNVidiaDeviceTable(t *testing.T) {
	fake := &fakeRunner{outputs: map[string]string{"nvidia-smi": nvidiaSmiTwoGPUs}}
	info := detect(context.Background(), 1, fake.run)

	if !info.Present {
		t.Fatalf("expected the second device to be detected: %+v", info)
	}
	if info.Backend != "vulkan" {
		t.Errorf("backend = %q, want vulkan", info.Backend)
	}
	if info.Driver != "nvidia" {
		t.Errorf("driver = %q, want nvidia", info.Driver)
	}
	if info.Device != 1 {
		t.Errorf("device = %d, want 1", info.Device)
	}
	if !strings.Contains(info.Name, "RTX 4090") {
		t.Errorf("name = %q, want the second device's name", info.Name)
	}
	if info.MemoryMiB != 24564 {
		t.Errorf("memory = %d MiB, want the second device's 24564", info.MemoryMiB)
	}
	if info.Reason != "" {
		t.Errorf("reason = %q, want empty for a detected device", info.Reason)
	}
}

// TestDetectRejectsMissingDeviceIndex is the regression this probe exists for: a
// host that has the driver utilities but not the requested device must NOT be
// reported as a usable GPU.
func TestDetectRejectsMissingDeviceIndex(t *testing.T) {
	fake := &fakeRunner{outputs: map[string]string{"nvidia-smi": nvidiaSmiTwoGPUs}}
	info := detect(context.Background(), 5, fake.run)

	if info.Present {
		t.Fatalf("device index 5 does not exist in a two-device table, got %+v", info)
	}
	if info.Backend != "" || info.Driver != "" {
		t.Errorf("a missing device must not name a backend/driver: %+v", info)
	}
	if info.Reason == "" {
		t.Error("reason must explain why nothing was detected")
	}
	if info.Device != 5 {
		t.Errorf("device = %d, want the requested 5", info.Device)
	}
}

// TestDetectFallsBackToVulkan pins the non-NVIDIA path: when the vendor query is
// unavailable, the Vulkan summary decides — and its device list must contain the
// requested index.
func TestDetectFallsBackToVulkan(t *testing.T) {
	fake := &fakeRunner{outputs: map[string]string{"vulkaninfo": vulkaninfoOneDevice}}
	info := detect(context.Background(), 0, fake.run)

	if !info.Present {
		t.Fatalf("expected the Vulkan device to be detected: %+v", info)
	}
	if info.Driver != "vulkan" {
		t.Errorf("driver = %q, want vulkan", info.Driver)
	}
	if info.Device != 0 {
		t.Errorf("device = %d, want 0", info.Device)
	}
	if !strings.Contains(info.Name, "RTX A4000") {
		t.Errorf("name = %q, want the summary's device name", info.Name)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected the nvidia-smi attempt to precede the Vulkan probe, calls = %v", fake.calls)
	}
}

// TestDetectNoGPU pins the fail-closed case: no tools at all, and a driver
// utility that answers with an empty table, both report no GPU.
func TestDetectNoGPU(t *testing.T) {
	for name, fake := range map[string]*fakeRunner{
		"no tools":      {outputs: nil},
		"empty table":   {outputs: map[string]string{"nvidia-smi": "\n"}},
		"unparsable":    {outputs: map[string]string{"nvidia-smi": "not,a,gpu,row,at,all"}},
		"blank device ": {outputs: map[string]string{"nvidia-smi": ", 550.54.14, 16376\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			info := detect(context.Background(), 0, fake.run)
			if info.Present {
				t.Fatalf("expected no GPU, got %+v", info)
			}
			if info.Backend != "" || info.Driver != "" {
				t.Errorf("backend/driver must stay empty without a device: %+v", info)
			}
			if info.Reason == "" {
				t.Error("reason must explain the absent device")
			}
		})
	}
}

// TestParseCSVRowsSkipsBlankLines pins the CSV handling: the blank trailing line
// nvidia-smi emits must not become a phantom device row.
func TestParseCSVRowsSkipsBlankLines(t *testing.T) {
	rows := parseCSVRows(nvidiaSmiTwoGPUs)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (the trailing blank line must be dropped)", len(rows))
	}
	if strings.TrimSpace(rows[0][0]) != "NVIDIA GeForce RTX A4000" {
		t.Errorf("first row name = %q", rows[0][0])
	}
}
