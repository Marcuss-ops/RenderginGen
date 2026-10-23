package main

// main_test.go pins the flag plumbing. It exists because this command had no
// tests at all, while it owned the one thing the three modes share: the
// translation from flags to the harness options. Two identical copies of that
// mapping is how a mode ends up measuring something the operator did not ask
// for, so the mapping is asserted field by field.

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/vramprobe"
)

// runtimeFlagsForTest is every field set to a value distinct from its
// neighbour, so a field mapped to the wrong slot cannot pass by coincidence.
func runtimeFlagsForTest() runtimeFlags {
	return runtimeFlags{
		plan:         "plan.json",
		outputDir:    "out-dir",
		samples:      "samples.log",
		binary:       "chronon3d_cli",
		assetsRoot:   "/assets",
		backend:      "vulkan",
		hardware:     "nvenc",
		encodePreset: "p1",
		gpuDevice:    3,
		daemons:      2,
		lanes:        1,
		interval:     20 * time.Millisecond,
		hold:         15 * time.Second,
		timeout:      time.Minute,
		out:          "report.json",
	}
}

func TestRuntimeFlagsTranslateEveryFieldOnce(t *testing.T) {
	got := runtimeFlagsForTest().options()
	want := vramprobe.PoolOptions{
		PlanPath:        "plan.json",
		OutputDir:       "out-dir",
		SamplesPath:     "samples.log",
		Binary:          "chronon3d_cli",
		AssetsRoot:      "/assets",
		Backend:         "vulkan",
		HardwareEncoder: "nvenc",
		EncodePreset:    "p1",
		GPUDevice:       3,
		Daemons:         2,
		LanesPerDaemon:  1,
		Interval:        20 * time.Millisecond,
		Hold:            15 * time.Second,
		Timeout:         time.Minute,
		ReportPath:      "report.json",
	}
	if got != want {
		t.Errorf("options() = %+v\nwant        %+v", got, want)
	}
	// The logger is deliberately NOT part of the mapping: each mode installs
	// its own, and a mapping that set it would silently overwrite the caller's.
	if got.Logger != nil {
		t.Errorf("options() must leave the logger to the caller, got %v", got.Logger)
	}
}

// The suite embeds the runtime flags, so the shared builder is the same one the
// harness uses. If this ever diverges, a suite flag would stop reaching the
// harness that measures it.
func TestSuiteFlagsShareTheRuntimeOptionsBuilder(t *testing.T) {
	flags := suiteFlags{runtimeFlags: runtimeFlagsForTest(), manifest: "matrix.json", dryRun: true}
	if flags.options() != runtimeFlagsForTest().options() {
		t.Error("suiteFlags must translate through the same runtimeFlags mapping")
	}
	if flags.manifest != "matrix.json" || !flags.dryRun {
		t.Errorf("the matrix fields must stay the suite's own: %+v", flags)
	}
}
