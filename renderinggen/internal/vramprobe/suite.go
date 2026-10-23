// suite.go turns the single-shape two-runtime harness into the calibration
// suite TICKET-DAEMON-JOB-LIFECYCLE §5 asks for.
//
// Why a suite and not "run the probe a few times". The ticket's exit condition
// for the question "can the daemon's RENDER_JOB mutex be removed?" is not one
// number: it is 3-5 CERTIFIED SHAPES, each measured with >=3 repetitions, with
// the number read from the per-process attribution (the device-wide counter
// drifts >3x on a shared host, measured: `vram-two-runtime-pool-2026-09-16.md`
// §4.1). A hand-run loop cannot state that: nobody can prove afterwards that
// every shape was repeated, or that the runs belonged to the same revision.
//
// So this file owns exactly two things and invents nothing:
//
//   - the SHAPE MATRIX (a manifest of labels + concrete plans + repeat count),
//     validated up front so a suite cannot be run with 1 repetition per shape
//     and then reported as a calibration;
//   - the AGGREGATION of the runs each shape produced, keeping the per-run
//     reports as the documents of record and reducing them to min/p50/max plus
//     the invariants (artifact identity across repetitions).
//
// The aggregation is pure so it is testable without a GPU: the measurement
// itself remains `RunTwoRuntimePool`, unchanged, one call per repetition.
package vramprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SuiteManifestSchema is the versioned shape-matrix document.
const SuiteManifestSchema = "renderinggen.vram-calibration-suite.v1"

// MinRunsPerShape is the ticket's own floor: a single sample is not a
// calibration, and two cannot show spread. A manifest asking for fewer is
// REJECTED rather than silently raised, so the caller learns the contract
// instead of getting a number the ticket does not accept.
const MinRunsPerShape = 3

// ShapeSpec is one row of the calibration matrix.
type ShapeSpec struct {
	// Label is the human name of the shape ("overlay-only", "video-source-fullgraph",
	// "direct-yuv", "4k-overlay", ...). Unique per suite: two rows with the same
	// label would make the report unable to say which plan a number belongs to.
	Label string `json:"label"`
	// Plan is a CONCRETE chronon.render-plan.v2 (the harness refuses a semantic
	// plan, which no daemon can render).
	Plan string `json:"plan"`
	// Repeat is how many times the two-runtime measurement is repeated for this
	// shape. Must be >= MinRunsPerShape.
	Repeat int `json:"repeat"`
}

// SuiteManifest is the shape matrix.
type SuiteManifest struct {
	Schema string      `json:"schema"`
	Shapes []ShapeSpec `json:"shapes"`
}

// LoadSuiteManifest reads and validates the matrix.
//
// Validation is deliberately strict, because every rule here is a way a
// "calibration" could otherwise be reported without being one: an unknown
// schema, an empty or duplicated label, a missing plan, a repeat below the
// floor.
//
// A plan path that is not absolute is resolved against the DIRECTORY OF THE
// MANIFEST, not the process working directory. The matrix is then a
// self-contained document: the same file means the same shapes whether it is
// invoked from the repository root, from `renderinggen/`, or from a service
// unit. (Same rule the pipeline applies to lexicon paths against the config's
// own directory; a CWD-relative matrix silently measures a different plan.)
func LoadSuiteManifest(path string) (SuiteManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SuiteManifest{}, fmt.Errorf("vramprobe: read suite manifest: %w", err)
	}
	var manifest SuiteManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return SuiteManifest{}, fmt.Errorf("vramprobe: decode suite manifest %s: %w", path, err)
	}
	if manifest.Schema != SuiteManifestSchema {
		return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s declares schema %q, want %q", path, manifest.Schema, SuiteManifestSchema)
	}
	if len(manifest.Shapes) == 0 {
		return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s declares no shapes", path)
	}
	manifestBase := filepath.Dir(path)
	seen := make(map[string]bool, len(manifest.Shapes))
	for index, shape := range manifest.Shapes {
		if shape.Label == "" {
			return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s shape #%d has an empty label", path, index)
		}
		if seen[shape.Label] {
			return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s declares the label %q twice", path, shape.Label)
		}
		seen[shape.Label] = true
		if shape.Plan == "" {
			return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s shape %q has no plan", path, shape.Label)
		}
		if !filepath.IsAbs(shape.Plan) {
			manifest.Shapes[index].Plan = filepath.Join(manifestBase, shape.Plan)
		}
		if shape.Repeat < MinRunsPerShape {
			return SuiteManifest{}, fmt.Errorf("vramprobe: suite manifest %s shape %q asks for %d run(s); a calibration needs at least %d",
				path, shape.Label, shape.Repeat, MinRunsPerShape)
		}
	}
	return manifest, nil
}

// SuiteShapePlan is one shape resolved against its plan, without rendering it.
// It is what `-dry-run` reports, so the matrix can be checked (labels, plans,
// schema, canvas, frames) before spending GPU time on it.
type SuiteShapePlan struct {
	Label      string    `json:"label"`
	Plan       string    `json:"plan"`
	PlanSHA256 string    `json:"plan_sha256"`
	Shape      PoolShape `json:"shape"`
	Repeat     int       `json:"repeat"`
}

// PlanSuite resolves every shape's plan. No daemon, no GPU, no sampling.
func PlanSuite(manifest SuiteManifest) ([]SuiteShapePlan, error) {
	plans := make([]SuiteShapePlan, 0, len(manifest.Shapes))
	for _, spec := range manifest.Shapes {
		shape, digest, err := readPoolPlan(spec.Plan)
		if err != nil {
			return nil, fmt.Errorf("vramprobe: shape %q: %w", spec.Label, err)
		}
		plans = append(plans, SuiteShapePlan{
			Label:      spec.Label,
			Plan:       spec.Plan,
			PlanSHA256: digest,
			Shape:      shape,
			Repeat:     spec.Repeat,
		})
	}
	return plans, nil
}

// Stat is a min/p50/max reduction of one measurement across repetitions.
// P50 is the upper median, so a value is always a measured one.
type Stat struct {
	Min int `json:"min"`
	P50 int `json:"p50"`
	Max int `json:"max"`
}

// summarizeInts is the ONE statistics helper: sorted input copy, upper median.
// It never invents a value, and it returns zeros for an empty slice so a shape
// with no completed run reports nothing rather than a fabricated number.
func summarizeInts(values []int) Stat {
	if len(values) == 0 {
		return Stat{}
	}
	ordered := append([]int(nil), values...)
	sort.Ints(ordered)
	return Stat{
		Min: ordered[0],
		P50: ordered[len(ordered)/2],
		Max: ordered[len(ordered)-1],
	}
}

// SuiteRunSummary is the compact projection of one repetition. The full
// PoolReport keeps its own file (ReportPath); this is what the suite report
// holds so a reader sees the spread without opening N documents.
type SuiteRunSummary struct {
	Run        int    `json:"run"`
	ReportPath string `json:"report_path"`
	// Device is the hardware THIS repetition observed. It is recorded per run
	// because a suite is long enough for the host to change under it (a driver
	// update, a fallback to another GPU), and a shape whose spread spans two
	// devices is not a measurement of one shape. It is also what
	// DeviceConsistentAcrossRuns is read from.
	Device                 PoolDevice `json:"device"`
	Samples                int        `json:"samples"`
	IdleDeviceMiB          int        `json:"idle_device_mib"`
	RenderingPeakDeviceMiB int        `json:"rendering_peak_device_mib"`
	RenderingDeltaMiB      int        `json:"rendering_delta_mib"`
	// DaemonAttributedMiB is the rendering-phase peak ATTRIBUTED to the
	// chronon3d_cli process(es): the number this suite calibrates on, because
	// the device-wide counter is not stable on a shared host.
	DaemonAttributedMiB   int  `json:"daemon_attributed_mib"`
	ResidentHoldDeviceMiB int  `json:"resident_hold_device_mib"`
	ArtifactsIdentical    bool `json:"artifacts_identical"`
	Jobs                  int  `json:"jobs"`
}

func summarizeRun(index int, report *PoolReport, reportPath string) SuiteRunSummary {
	return SuiteRunSummary{
		Run:                    index,
		ReportPath:             reportPath,
		Device:                 report.Device,
		Samples:                report.Samples,
		IdleDeviceMiB:          report.IdleDeviceMiB,
		RenderingPeakDeviceMiB: report.RenderingPeakDeviceMiB,
		RenderingDeltaMiB:      report.RenderingDeltaMiB,
		DaemonAttributedMiB:    report.Phases[string(phaseRendering)].PeakDaemonMiB,
		ResidentHoldDeviceMiB:  report.ResidentHoldDeviceMiB,
		ArtifactsIdentical:     report.ArtifactsIdentical,
		Jobs:                   len(report.Jobs),
	}
}

// SuiteShapeResult is one shape's aggregated evidence.
type SuiteShapeResult struct {
	Label      string    `json:"label"`
	Plan       string    `json:"plan"`
	PlanSHA256 string    `json:"plan_sha256"`
	Shape      PoolShape `json:"shape"`
	// Device and Execution come from the runs, so the number carries the
	// hardware and the transport it was measured on.
	Device    PoolDevice  `json:"device"`
	Execution PoolRuntime `json:"execution"`

	RunsRequested int `json:"runs_requested"`
	RunsCompleted int `json:"runs_completed"`

	// DaemonAttributedMiB is the calibrated quantity (per-process attribution).
	DaemonAttributedMiB Stat `json:"daemon_attributed_mib"`
	// DevicePeakMiB / DeviceDeltaMiB are recorded because the raw series is
	// evidence, and flagged as unstable in the report's caveat.
	DevicePeakMiB         Stat `json:"device_peak_mib"`
	DeviceDeltaMiB        Stat `json:"device_delta_mib"`
	ResidentHoldDeviceMiB Stat `json:"resident_hold_device_mib"`

	// ArtifactsIdenticalAcrossRuns is true only when EVERY run reported
	// byte-identical artifacts for its two runtimes: a shape whose repetitions
	// rendered different bytes is not one shape, and its numbers are not
	// comparable across runs.
	ArtifactsIdenticalAcrossRuns bool `json:"artifacts_identical_across_runs"`
	// DeviceConsistentAcrossRuns is true only when EVERY completed run observed
	// the same device name and driver version: a shape measured across a driver
	// update is two shapes, and its spread is the update rather than the render.
	DeviceConsistentAcrossRuns bool `json:"device_consistent_across_runs"`

	Runs []SuiteRunSummary `json:"runs"`
}

// SuiteReport is the document of record for one calibration suite run.
type SuiteReport struct {
	Schema string `json:"schema"`

	ManifestPath string `json:"manifest_path"`
	// Matrix is the plan-resolved matrix actually executed, so the report names
	// the plans and their SHAs even when a run failed part-way.
	Matrix []SuiteShapePlan `json:"matrix"`

	Binary           string  `json:"binary"`
	Backend          string  `json:"backend"`
	HardwareEncoder  string  `json:"hardware_encoder"`
	EncodePreset     string  `json:"encode_preset"`
	DaemonCount      int     `json:"daemon_count"`
	LanesPerDaemon   int     `json:"lanes_per_daemon"`
	SampleIntervalMS int     `json:"sample_interval_ms"`
	HoldSeconds      float64 `json:"hold_seconds"`

	Shapes []SuiteShapeResult `json:"shapes"`

	// AuthorizesMutexRemoval mirrors the single-shape report and stays false:
	// this suite produces the calibration INPUT. Feeding
	// derive_vram_working_set is a separate, deliberate step.
	AuthorizesMutexRemoval bool   `json:"authorizes_mutex_removal"`
	Caveat                 string `json:"caveat"`

	// Complete is true only when EVERY shape is a calibration datapoint: every
	// requested repetition completed and all of them are one shape (same bytes,
	// same device). The document is written either way — it is the evidence of
	// what these runs did — but `complete` is what stops a reader from quoting
	// an incomplete run's numbers as a calibration.
	Complete bool `json:"complete"`
	// IncompleteShapes names every shape that is NOT a calibration datapoint,
	// with the reason, so the re-run list is in the document rather than in the
	// operator's head.
	IncompleteShapes []SuiteIncompleteShape `json:"incomplete_shapes,omitempty"`
}

// SuiteIncompleteShape is one shape that is not a usable calibration datapoint.
type SuiteIncompleteShape struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

// SuiteReportSchema is the versioned suite document.
const SuiteReportSchema = "renderinggen.vram-calibration-report.v1"

// SuiteCaveat states the limits of the document, in the document.
const SuiteCaveat = "The per-shape daemon-attributed numbers are the calibration input; the device-wide series is reported but is not stable on a host shared with other tenants. A shape is a calibration datapoint only when every requested repetition completed as the SAME shape (artifacts_identical_across_runs and device_consistent_across_runs both true): incomplete_shapes names the shapes that are not, and `complete` stays false until they are re-run."

// SummarizeShape reduces the repetitions of ONE shape. `runs` are in
// execution order; a nil entry means the repetition failed, which lowers
// RunsCompleted but never contributes a fabricated sample.
func SummarizeShape(plan SuiteShapePlan, runs []*PoolReport, reportPaths []string) SuiteShapeResult {
	result := SuiteShapeResult{
		Label:         plan.Label,
		Plan:          plan.Plan,
		PlanSHA256:    plan.PlanSHA256,
		Shape:         plan.Shape,
		RunsRequested: plan.Repeat,
	}
	var attributed, devicePeak, deviceDelta, residentHold []int
	allIdentical := true
	sameDevice := true
	for index, report := range runs {
		if report == nil {
			continue
		}
		path := ""
		if index < len(reportPaths) {
			path = reportPaths[index]
		}
		summary := summarizeRun(index, report, path)
		result.Runs = append(result.Runs, summary)
		result.RunsCompleted++
		if result.Device.Name == "" {
			// The device/transport identity is a property of the host, so the
			// first completed run declares it; every later run is compared
			// against it below and a mismatch is stated in the document.
			result.Device = report.Device
			result.Execution = report.Execution
		} else if summary.Device.Name != result.Device.Name || summary.Device.DriverVersion != result.Device.DriverVersion {
			sameDevice = false
		}
		attributed = append(attributed, summary.DaemonAttributedMiB)
		devicePeak = append(devicePeak, summary.RenderingPeakDeviceMiB)
		deviceDelta = append(deviceDelta, summary.RenderingDeltaMiB)
		residentHold = append(residentHold, summary.ResidentHoldDeviceMiB)
		if !summary.ArtifactsIdentical {
			allIdentical = false
		}
	}
	result.DaemonAttributedMiB = summarizeInts(attributed)
	result.DevicePeakMiB = summarizeInts(devicePeak)
	result.DeviceDeltaMiB = summarizeInts(deviceDelta)
	result.ResidentHoldDeviceMiB = summarizeInts(residentHold)
	result.ArtifactsIdenticalAcrossRuns = result.RunsCompleted > 0 && allIdentical
	result.DeviceConsistentAcrossRuns = result.RunsCompleted > 0 && sameDevice
	return result
}

// incompleteReason returns why a shape is not a calibration datapoint, or ""
// when it is. The order is the order of severity: a shape that did not finish
// is reported as unfinished rather than as inconsistent, because the runs that
// are missing are the first thing to fix.
func incompleteReason(result SuiteShapeResult) string {
	switch {
	case result.RunsCompleted == 0:
		return fmt.Sprintf("no repetition completed (%d requested)", result.RunsRequested)
	case result.RunsCompleted < result.RunsRequested:
		return fmt.Sprintf("%d of %d repetitions completed", result.RunsCompleted, result.RunsRequested)
	case !result.ArtifactsIdenticalAcrossRuns:
		return "repetitions rendered different bytes"
	case !result.DeviceConsistentAcrossRuns:
		return "repetitions ran on a different device or driver"
	}
	return ""
}

// SummarizeSuite builds the report from the resolved matrix and the runs it
// produced (shapes[i][run]). It is pure: same inputs, same document.
//
// The matrix is the execution plan, so the runs are paired with the PLANS and
// not with the manifest: a caller that summarized against a different document
// would produce a report that names the wrong shapes, and there is nothing left
// to compare that against once the plans are the input.
func SummarizeSuite(manifestPath string, plans []SuiteShapePlan,
	runs [][]*PoolReport, reportPaths [][]string) SuiteReport {
	report := SuiteReport{
		Schema:                 SuiteReportSchema,
		ManifestPath:           manifestPath,
		Matrix:                 plans,
		AuthorizesMutexRemoval: false,
		Caveat:                 SuiteCaveat,
	}
	for index, plan := range plans {
		var shapeRuns []*PoolReport
		var shapePaths []string
		if index < len(runs) {
			shapeRuns = runs[index]
		}
		if index < len(reportPaths) {
			shapePaths = reportPaths[index]
		}
		report.Shapes = append(report.Shapes, SummarizeShape(plan, shapeRuns, shapePaths))
	}
	// Completeness is computed from the shapes the report actually holds, so it
	// answers the question a reader has ("can I use these numbers?") instead of
	// a bookkeeping question about the inputs. A shape with one failed
	// repetition is named, not silently averaged.
	for _, shape := range report.Shapes {
		if reason := incompleteReason(shape); reason != "" {
			report.IncompleteShapes = append(report.IncompleteShapes, SuiteIncompleteShape{Label: shape.Label, Reason: reason})
		}
	}
	report.Complete = len(report.IncompleteShapes) == 0
	return report
}

// describeIncomplete renders the re-run list for an error message.
func describeIncomplete(shapes []SuiteIncompleteShape) string {
	parts := make([]string, 0, len(shapes))
	for _, shape := range shapes {
		parts = append(parts, fmt.Sprintf("%s (%s)", shape.Label, shape.Reason))
	}
	return strings.Join(parts, "; ")
}

// WriteSuiteReport serializes the suite report.
func WriteSuiteReport(report SuiteReport, path string) error {
	return writeJSONFile(path, report)
}

// RunSuite executes the matrix: every shape, repeated at least
// MinRunsPerShape times, each repetition being one two-runtime measurement.
//
// The runs are SEQUENTIAL on purpose. A suite measures shapes, not scheduler
// contention: overlapping two shapes' harnesses would make every number in the
// report a function of the other shape's allocation.
//
// It FAILS CLOSED on an incomplete suite: the report is written (it is the
// evidence of what the runs did) and an error is returned naming every shape
// that is not a calibration datapoint, so a suite that measured nothing cannot
// exit zero and be quoted as a calibration.
func RunSuite(ctx context.Context, manifestPath string, manifest SuiteManifest, base PoolOptions) (SuiteReport, error) {
	plans, err := PlanSuite(manifest)
	if err != nil {
		return SuiteReport{}, err
	}
	runs := make([][]*PoolReport, len(plans))
	paths := make([][]string, len(plans))
	for shapeIndex, plan := range plans {
		runs[shapeIndex] = make([]*PoolReport, plan.Repeat)
		paths[shapeIndex] = make([]string, plan.Repeat)
		for run := 0; run < plan.Repeat; run++ {
			options := base
			options.PlanPath = plan.Plan
			options.ReportPath = fmt.Sprintf("%s.run%d.json", base.ReportPath, run)
			// A dedicated output directory per run: two runs sharing one would
			// race on the artifact paths.
			options.OutputDir = fmt.Sprintf("%s/%s-run%d", base.OutputDir, plan.Label, run)
			if options.SamplesPath != "" {
				options.SamplesPath = fmt.Sprintf("%s.run%d.samples.log", base.SamplesPath, run)
			}
			paths[shapeIndex][run] = options.ReportPath
			report, err := RunTwoRuntimePool(ctx, options)
			if err != nil {
				// A failed repetition is recorded as missing evidence, not as a
				// zero: the suite continues so one broken shape cannot hide the
				// shapes that did measure.
				if ctx.Err() != nil {
					return SuiteReport{}, fmt.Errorf("vramprobe: suite interrupted during shape %q run %d: %w", plan.Label, run, err)
				}
				if options.Logger != nil {
					options.Logger.Printf("shape %q run %d failed: %v", plan.Label, run, err)
				}
				continue
			}
			runs[shapeIndex][run] = report
		}
	}
	summary := SummarizeSuite(manifestPath, plans, runs, paths)
	applySuiteExecutionMetadata(&summary, base)
	if err := WriteSuiteReport(summary, base.ReportPath); err != nil {
		return SuiteReport{}, err
	}
	if !summary.Complete {
		// The document is on disk ON PURPOSE: it is the evidence of what these
		// runs did, and deleting it would lose the reason to re-run. The error
		// is what stops the caller from quoting a partial suite as a
		// calibration — otherwise "runs 0/3" reaches the exit code as success.
		return summary, fmt.Errorf("vramprobe: calibration suite incomplete: %s (report written to %s)",
			describeIncomplete(summary.IncompleteShapes), base.ReportPath)
	}
	return summary, nil
}

// applySuiteExecutionMetadata records the transport the suite asked for, so the
// document can be compared with another one without opening the matrix.
func applySuiteExecutionMetadata(report *SuiteReport, base PoolOptions) {
	report.Binary = base.Binary
	report.Backend = base.Backend
	report.HardwareEncoder = base.HardwareEncoder
	report.EncodePreset = base.EncodePreset
	report.DaemonCount = base.Daemons
	report.LanesPerDaemon = base.LanesPerDaemon
	report.SampleIntervalMS = int(base.Interval.Milliseconds())
	report.HoldSeconds = base.Hold.Seconds()
}
