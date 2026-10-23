package vramprobe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSuiteManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func shapeSpec(label, plan string, repeat int) SuiteShapePlan {
	return SuiteShapePlan{
		Label:      label,
		Plan:       plan,
		PlanSHA256: "digest-" + label,
		Shape:      PoolShape{Width: 1920, Height: 1080, Frames: 120, LayerTypes: []string{"color", "text"}},
		Repeat:     repeat,
	}
}

// rtxA4000 is the host every run reports unless a test is about the host
// changing under the suite.
var rtxA4000 = PoolDevice{Name: "NVIDIA RTX A4000", DriverVersion: "595.91.07", TotalMiB: 16376}

// poolReport builds a run result with the four numbers the suite calibrates on.
func poolReport(attributed, devicePeak, deviceDelta, residentHold int, identical bool) *PoolReport {
	return poolReportOnDevice(rtxA4000, attributed, devicePeak, deviceDelta, residentHold, identical)
}

// poolReportOnDevice is poolReport on an explicit host, for the cases where the
// suite has to notice that a repetition did not run on the same device.
func poolReportOnDevice(device PoolDevice, attributed, devicePeak, deviceDelta, residentHold int, identical bool) *PoolReport {
	report := &PoolReport{
		Samples:                100,
		IdleDeviceMiB:          300,
		RenderingPeakDeviceMiB: devicePeak,
		RenderingDeltaMiB:      deviceDelta,
		ResidentHoldDeviceMiB:  residentHold,
		ArtifactsIdentical:     identical,
		Device:                 device,
		Execution:              PoolRuntime{Binary: "chronon3d_cli", Backend: "vulkan", HardwareEncoder: "nvenc", EncodePreset: "p1"},
		Phases: map[string]PoolPhaseFacts{
			string(phaseRendering): {PeakDaemonMiB: attributed},
		},
		Jobs: []PoolJobFacts{{SHA256: "aa"}, {SHA256: "aa"}},
	}
	return report
}

// A "calibration" that repeats a shape once is not a calibration: the manifest
// must refuse it rather than let the report claim spread it never measured.
func TestLoadSuiteManifestRefusesAMatrixThatIsNotACalibration(t *testing.T) {
	valid := `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"overlay-only","plan":"a.json","repeat":3}]}`

	cases := map[string]string{
		"unknown schema":     `{"schema":"something.else.v1","shapes":[{"label":"a","plan":"a.json","repeat":3}]}`,
		"no shapes":          `{"schema":"` + SuiteManifestSchema + `","shapes":[]}`,
		"empty label":        `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"","plan":"a.json","repeat":3}]}`,
		"duplicate label":    `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"a","plan":"a.json","repeat":3},{"label":"a","plan":"b.json","repeat":3}]}`,
		"missing plan":       `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"a","plan":"","repeat":3}]}`,
		"repeat below floor": `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"a","plan":"a.json","repeat":2}]}`,
		"repeat unset":       `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"a","plan":"a.json"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadSuiteManifest(writeSuiteManifest(t, body)); err == nil {
				t.Fatalf("manifest accepted, want an error")
			}
		})
	}

	// The same document with a valid row must load, or the rejections above
	// would be vacuous.
	manifest, err := LoadSuiteManifest(writeSuiteManifest(t, valid))
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if len(manifest.Shapes) != 1 || manifest.Shapes[0].Repeat != MinRunsPerShape {
		t.Fatalf("manifest = %+v, want one shape repeated %d times", manifest, MinRunsPerShape)
	}

	if _, err := LoadSuiteManifest(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing manifest must be an error")
	}
}

func TestSummarizeIntsIsAMinP50MaxAndInventsNothing(t *testing.T) {
	empty := summarizeInts(nil)
	if empty != (Stat{}) {
		t.Fatalf("empty = %+v, want the zero Stat (no fabricated sample)", empty)
	}
	// The input must not be reordered in place: callers pass slices they reuse.
	values := []int{1814, 1692, 1700}
	got := summarizeInts(values)
	if got.Min != 1692 || got.P50 != 1700 || got.Max != 1814 {
		t.Fatalf("stat = %+v, want min=1692 p50=1700 max=1814", got)
	}
	if values[0] != 1814 {
		t.Fatalf("summarizeInts mutated its input: %v", values)
	}
	single := summarizeInts([]int{7})
	if single.Min != 7 || single.P50 != 7 || single.Max != 7 {
		t.Fatalf("single = %+v", single)
	}
}

func TestSummarizeShapeAggregatesRepetitionsAndKeepsFailuresMissing(t *testing.T) {
	plan := shapeSpec("overlay-only", "plan.json", 3)

	// Three completed runs: the spread is the calibration evidence, and the
	// device/transport identity is recorded from the runs.
	complete := SummarizeShape(plan,
		[]*PoolReport{
			poolReport(1692, 1815, 1515, 700, true),
			poolReport(1814, 1945, 1645, 768, true),
			poolReport(1700, 1900, 1600, 740, true),
		},
		[]string{"r0.json", "r1.json", "r2.json"})
	if complete.RunsCompleted != 3 || complete.RunsRequested != 3 {
		t.Fatalf("runs = %d/%d, want 3/3", complete.RunsCompleted, complete.RunsRequested)
	}
	if complete.DaemonAttributedMiB != (Stat{Min: 1692, P50: 1700, Max: 1814}) {
		t.Fatalf("attribution stat = %+v", complete.DaemonAttributedMiB)
	}
	if complete.DevicePeakMiB.Max != 1945 || complete.DeviceDeltaMiB.Min != 1515 {
		t.Fatalf("device stats = %+v / %+v", complete.DevicePeakMiB, complete.DeviceDeltaMiB)
	}
	if !complete.ArtifactsIdenticalAcrossRuns {
		t.Error("every run reported identical artifacts, so the shape must say so")
	}
	if complete.Device.Name != "NVIDIA RTX A4000" || complete.Execution.HardwareEncoder != "nvenc" {
		t.Fatalf("device/execution identity missing: %+v %+v", complete.Device, complete.Execution)
	}
	if !complete.DeviceConsistentAcrossRuns {
		t.Error("every run reported the same device and driver, so the shape must say so")
	}
	if len(complete.Runs) != 3 || complete.Runs[1].ReportPath != "r1.json" {
		t.Fatalf("per-run summaries = %+v", complete.Runs)
	}
	// The per-run device is recorded, not just compared: the document has to
	// show WHAT each repetition ran on, otherwise a mismatch is unarguable.
	if complete.Runs[0].Device.Name != "NVIDIA RTX A4000" || complete.Runs[0].Device.DriverVersion != "595.91.07" {
		t.Fatalf("per-run device missing: %+v", complete.Runs[0])
	}

	// A failed repetition is missing evidence, never a zero.
	partial := SummarizeShape(plan,
		[]*PoolReport{poolReport(1692, 1815, 1515, 700, true), nil, poolReport(1700, 1900, 1600, 740, true)},
		[]string{"r0.json", "", "r2.json"})
	if partial.RunsCompleted != 2 {
		t.Fatalf("runs_completed = %d, want 2", partial.RunsCompleted)
	}
	if partial.DaemonAttributedMiB != (Stat{Min: 1692, P50: 1700, Max: 1700}) {
		t.Fatalf("attribution stat = %+v, want the two completed runs only", partial.DaemonAttributedMiB)
	}

	// Different bytes across runtimes is not one shape.
	differing := SummarizeShape(plan,
		[]*PoolReport{poolReport(1692, 1815, 1515, 700, true), poolReport(1700, 1900, 1600, 740, false), poolReport(1701, 1901, 1601, 741, true)},
		nil)
	if differing.ArtifactsIdenticalAcrossRuns {
		t.Error("a run whose two runtimes rendered different bytes must break the shape's identity")
	}

	// A host that changes mid-suite is not one shape either: the spread would be
	// the driver update rather than the render.
	movedHost := SummarizeShape(plan,
		[]*PoolReport{
			poolReport(1692, 1815, 1515, 700, true),
			poolReportOnDevice(PoolDevice{Name: "NVIDIA RTX A5000", DriverVersion: "596.10.02", TotalMiB: 16376}, 1700, 1900, 1600, 740, true),
			poolReport(1701, 1901, 1601, 741, true),
		},
		nil)
	if movedHost.DeviceConsistentAcrossRuns {
		t.Error("a repetition on a different device or driver must break the shape's identity")
	}
	if !movedHost.ArtifactsIdenticalAcrossRuns {
		t.Error("the moved-host case is about the device, so the artifacts must still read as identical")
	}

	noRuns := SummarizeShape(plan, []*PoolReport{nil, nil, nil}, nil)
	if noRuns.RunsCompleted != 0 || noRuns.ArtifactsIdenticalAcrossRuns || noRuns.DeviceConsistentAcrossRuns {
		t.Fatalf("no completed run must report nothing: %+v", noRuns)
	}
}

func TestSummarizeSuiteIsDeterministicAndNamesEveryShapePlan(t *testing.T) {
	plans := []SuiteShapePlan{
		shapeSpec("overlay-only", "overlay.json", 3),
		shapeSpec("4k-overlay", "uhd.json", 3),
	}
	runs := [][]*PoolReport{
		{poolReport(1692, 1815, 1515, 700, true), poolReport(1814, 1945, 1645, 768, true), poolReport(1700, 1900, 1600, 740, true)},
		{poolReport(4200, 4500, 4200, 2100, true), poolReport(4300, 4600, 4300, 2200, true), poolReport(4250, 4550, 4250, 2150, true)},
	}
	paths := [][]string{{"a0", "a1", "a2"}, {"b0", "b1", "b2"}}

	first := SummarizeSuite("suite.json", plans, runs, paths)
	second := SummarizeSuite("suite.json", plans, runs, paths)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("the same inputs must produce the same document")
	}
	if len(first.Shapes) != 2 {
		t.Fatalf("shapes = %d, want 2", len(first.Shapes))
	}
	if first.Shapes[1].Label != "4k-overlay" || first.Shapes[1].DaemonAttributedMiB.P50 != 4250 {
		t.Fatalf("second shape = %+v", first.Shapes[1])
	}
	if first.AuthorizesMutexRemoval {
		t.Error("a suite is the calibration INPUT: it never authorizes removing the mutex")
	}
	if first.Caveat != SuiteCaveat {
		t.Fatalf("caveat = %q", first.Caveat)
	}
	// Every shape completed every repetition here, so the document must say it
	// is usable. "Complete" is the field a consumer quotes, so a false negative
	// is as damaging as a false positive.
	if !first.Complete || len(first.IncompleteShapes) != 0 {
		t.Fatalf("complete=%v incomplete=%+v, want a complete suite", first.Complete, first.IncompleteShapes)
	}
}

// A suite that could not measure a shape must say so IN THE DOCUMENT, with the
// reason: this is the document half of RunSuite's fail-closed contract (the
// error half is not testable without a GPU, the reason list is).
func TestSummarizeSuiteNamesEveryShapeThatIsNotACalibration(t *testing.T) {
	plans := []SuiteShapePlan{
		shapeSpec("unfinished", "a.json", 3),
		shapeSpec("empty", "b.json", 3),
		shapeSpec("different-bytes", "c.json", 3),
		shapeSpec("moved-host", "d.json", 3),
	}
	good := func() *PoolReport { return poolReport(1692, 1815, 1515, 700, true) }
	runs := [][]*PoolReport{
		{good(), nil, good()},
		{nil, nil, nil},
		{good(), poolReport(1692, 1815, 1515, 700, false), good()},
		{good(), poolReportOnDevice(PoolDevice{Name: "NVIDIA RTX A5000", DriverVersion: "596.10.02"}, 1700, 1900, 1600, 740, true), good()},
	}
	report := SummarizeSuite("suite.json", plans, runs, nil)

	if report.Complete {
		t.Fatal("a suite whose four shapes are all unusable reported itself complete")
	}
	// Order is part of the contract: the list is read as the re-run list, so it
	// follows the matrix rather than a map iteration.
	want := []SuiteIncompleteShape{
		{Label: "unfinished", Reason: "2 of 3 repetitions completed"},
		{Label: "empty", Reason: "no repetition completed (3 requested)"},
		{Label: "different-bytes", Reason: "repetitions rendered different bytes"},
		{Label: "moved-host", Reason: "repetitions ran on a different device or driver"},
	}
	if len(report.IncompleteShapes) != len(want) {
		t.Fatalf("incomplete = %+v, want %d entries", report.IncompleteShapes, len(want))
	}
	for index, expected := range want {
		if report.IncompleteShapes[index] != expected {
			t.Errorf("incomplete[%d] = %+v, want %+v", index, report.IncompleteShapes[index], expected)
		}
	}

	// The same matrix with every repetition completed is complete: the list is
	// not "shapes that ran", it is "shapes that did not".
	completeRuns := [][]*PoolReport{
		{good(), good(), good()},
		{good(), good(), good()},
		{good(), good(), good()},
		{good(), good(), good()},
	}
	complete := SummarizeSuite("suite.json", plans, completeRuns, nil)
	if !complete.Complete || len(complete.IncompleteShapes) != 0 {
		t.Fatalf("complete=%v incomplete=%+v, want a complete suite", complete.Complete, complete.IncompleteShapes)
	}
}

// PlanSuite is the dry-run: it must resolve every shape from its plan without a
// daemon, and refuse a plan the harness cannot render.
func TestPlanSuiteResolvesEveryShapeWithoutAGPU(t *testing.T) {
	planPath := writePlan(t, concretePlan)
	manifest := SuiteManifest{Schema: SuiteManifestSchema, Shapes: []ShapeSpec{
		{Label: "overlay-only", Plan: planPath, Repeat: 3},
	}}

	plans, err := PlanSuite(manifest)
	if err != nil {
		t.Fatalf("PlanSuite: %v", err)
	}
	if len(plans) != 1 || plans[0].Shape.Width != 1920 || plans[0].Shape.Frames != 120 {
		t.Fatalf("plans = %+v, want the concrete shape read from the plan", plans)
	}
	if len(plans[0].PlanSHA256) != 64 || plans[0].Repeat != 3 {
		t.Fatalf("plan identity = %+v", plans[0])
	}

	// The dry-run is the gate before GPU time: an unusable plan must fail here.
	semantic := writeSuiteManifest(t, `{"schema":"renderinggen.overlay-plan.v1","canvas":{"width":1920}}`)
	bad := SuiteManifest{Schema: SuiteManifestSchema, Shapes: []ShapeSpec{
		{Label: "semantic", Plan: semantic, Repeat: 3},
	}}
	if _, err := PlanSuite(bad); err == nil {
		t.Error("a semantic plan must be rejected by the dry-run")
	}
}

// A relative plan path is resolved against the MANIFEST's directory, not the
// process working directory, so the matrix means the same shapes wherever it is
// invoked from. The assertion is that the resolved plan both matches the
// expected absolute path AND opens, because a path that resolves but does not
// exist is the bug this rule exists to prevent.
func TestSuiteManifestResolvesPlansAgainstItsOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(planDir, "concrete.json")
	if err := os.WriteFile(planPath, []byte(concretePlan), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "suite.json")
	body := `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"overlay-only","plan":"plans/concrete.json","repeat":3}]}`
	if err := os.WriteFile(manifestPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadSuiteManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadSuiteManifest: %v", err)
	}
	if manifest.Shapes[0].Plan != planPath {
		t.Fatalf("plan = %q, want %q (resolved against the manifest directory)", manifest.Shapes[0].Plan, planPath)
	}
	if _, err := PlanSuite(manifest); err != nil {
		t.Fatalf("the resolved plan must be readable: %v", err)
	}

	// An absolute plan path is left untouched.
	absoluteBody := `{"schema":"` + SuiteManifestSchema + `","shapes":[{"label":"abs","plan":"` + planPath + `","repeat":3}]}`
	if err := os.WriteFile(manifestPath, []byte(absoluteBody), 0o644); err != nil {
		t.Fatal(err)
	}
	absolute, err := LoadSuiteManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadSuiteManifest(absolute): %v", err)
	}
	if absolute.Shapes[0].Plan != planPath {
		t.Fatalf("absolute plan = %q, want %q", absolute.Shapes[0].Plan, planPath)
	}
}
