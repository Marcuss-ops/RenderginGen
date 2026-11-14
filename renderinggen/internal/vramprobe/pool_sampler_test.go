package vramprobe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPhaseFactsDeltaIsMeasuredAgainstEachPhasesFirstSample pins the reduction
// rule the report states: a device shared with other tenants drifts, so each
// phase's delta must be computed against that phase's own first sample, never
// against a global baseline.
func TestPhaseFactsDeltaIsMeasuredAgainstEachPhasesFirstSample(t *testing.T) {
	sampler := &poolSampler{}
	sampler.samples = []poolSample{
		{phase: phaseIdle, device: 1000, daemon: 100},
		{phase: phaseIdle, device: 1010, daemon: 110},
		{phase: phaseWarmPools, device: 1400, daemon: 300},
		{phase: phaseWarmPools, device: 1500, daemon: 350},
		{phase: phaseRendering, device: 1400, daemon: 300},
		{phase: phaseRendering, device: 2400, daemon: 900, utilization: 97},
	}
	facts := sampler.phaseFacts()

	idle := facts[string(phaseIdle)]
	if idle.Samples != 2 || idle.IdleDeviceMiB != 1000 || idle.PeakDeviceMiB != 1010 || idle.DeviceDeltaMiB != 10 {
		t.Fatalf("idle facts = %+v, want 2 samples and a delta from its own first sample", idle)
	}
	warm := facts[string(phaseWarmPools)]
	if warm.IdleDeviceMiB != 1400 || warm.DeviceDeltaMiB != 100 {
		t.Fatalf("warm_pools facts = %+v, want delta from 1400 (its own first sample), not the global 1000", warm)
	}
	render := facts[string(phaseRendering)]
	if render.IdleDeviceMiB != 1400 || render.PeakDeviceMiB != 2400 || render.DeviceDeltaMiB != 1000 {
		t.Fatalf("rendering facts = %+v", render)
	}
	if render.DaemonDeltaMiB != 600 || render.PeakUtilization != 97 {
		t.Fatalf("rendering daemon facts = %+v, want peak daemon delta 600 and utilization 97", render)
	}
}

// TestPhaseFactsOfUnmeasuredPhaseIsAbsent pins that a phase with no sample is
// absent from the map instead of being published as a zero-cost phase.
func TestPhaseFactsOfUnmeasuredPhaseIsAbsent(t *testing.T) {
	sampler := &poolSampler{}
	if got := sampler.phaseFacts(); len(got) != 0 {
		t.Fatalf("facts = %+v, want no phases", got)
	}
}

// TestAllReturnsACopy pins that a reader cannot mutate the series the report
// will summarize.
func TestAllReturnsACopy(t *testing.T) {
	sampler := &poolSampler{}
	sampler.samples = []poolSample{{phase: phaseProbe, device: 10}}
	returned := sampler.all()
	returned[0].device = 999
	if sampler.samples[0].device != 10 {
		t.Fatal("all() exposed the sampler's backing array")
	}
}

// TestWriteSamplesKeepsEveryColumn pins the raw-series contract: a reader must
// be able to re-derive any statistic without trusting the summary.
func TestWriteSamplesKeepsEveryColumn(t *testing.T) {
	sampler := &poolSampler{}
	sampler.samples = []poolSample{
		{phase: phaseIdle, at: 1500 * time.Millisecond, device: 1000, daemon: 100},
		{phase: phaseRendering, at: 2 * time.Second, device: 2400, daemon: 900, utilization: 97},
	}
	path := filepath.Join(t.TempDir(), "samples.tsv")
	if err := sampler.writeSamples(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want header + 2 samples:\n%s", len(lines), raw)
	}
	if !strings.HasPrefix(lines[0], "# phase elapsed_s device_used_mib") {
		t.Fatalf("header = %q", lines[0])
	}
	if lines[1] != "idle 1.50 1000 100 0" {
		t.Fatalf("idle row = %q", lines[1])
	}
	if lines[2] != "rendering 2.00 2400 900 97" {
		t.Fatalf("rendering row = %q", lines[2])
	}
}

// TestAbsolutizePoolPathsResolvesEveryInput pins that the evidence cannot cite
// a relative path whose meaning depends on the caller's working directory.
func TestAbsolutizePoolPathsResolvesEveryInput(t *testing.T) {
	opts := PoolOptions{PlanPath: "plan.json", OutputDir: "out"}
	if err := absolutizePoolPaths(&opts); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(opts.PlanPath) || !filepath.IsAbs(opts.OutputDir) {
		t.Fatalf("paths were not resolved: %+v", opts)
	}
	if opts.SamplesPath != "" {
		t.Fatalf("an unset path must stay unset, got %q", opts.SamplesPath)
	}
}
