package overlay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	motioncert "github.com/Marcuss-ops/RenderingGen/renderinggen/motion-certification"
)

// The certification payload is only useful if it is parsed from the real
// checked-in reports AND honest about being a snapshot. These tests pin both: the
// recorded outcome of a report is published, a motion no report names is
// unverified, and a corrupt report cannot degrade into a silent "all unverified".

func TestCertificationSnapshotParsesTheCheckedInReports(t *testing.T) {
	snapshot, err := motioncert.Load()
	if err != nil {
		t.Fatalf("parse embedded certification reports: %v", err)
	}
	if len(snapshot.Reports) == 0 {
		t.Fatal("no certification report was parsed; the embedded payload would publish every motion as unverified")
	}
	if snapshot.SnapshotAt == "" {
		t.Error("snapshot has no timestamp, so a consumer could not tell the statuses are dated evidence")
	}
	for _, report := range snapshot.Reports {
		if report.Name == "" || report.Scope == "" || report.Backend == "" || report.GeneratedAt == "" || report.Motions == 0 {
			t.Errorf("parsed report entry is incomplete: %+v", report)
		}
	}

	// A recorded pass travels as passed, with the report that recorded it.
	passed := snapshot.Status("typewriter_clean")
	if passed.Status != motioncert.StatusPassed || passed.Report == "" || passed.GeneratedAt == "" {
		t.Errorf("typewriter_clean status = %+v, want a passed entry naming its report", passed)
	}

	// Newest report wins. image_parallax_frame is passed by the software image
	// report and render_failed by the newer vulkan report, so the published
	// status must be the newer outcome, not the first one read.
	failed := snapshot.Status("image_parallax_frame")
	if failed.Status != motioncert.StatusFailed {
		t.Errorf("image_parallax_frame status = %+v, want failed from the newer report", failed)
	}
	if !strings.Contains(failed.Report, "vulkan") {
		t.Errorf("image_parallax_frame report = %q, want the newer vulkan report to have set it", failed.Report)
	}

	// A motion no report names is unverified, not absent and not passed.
	unrecorded := snapshot.Status("no_report_names_this_motion")
	if unrecorded.Status != motioncert.StatusUnverified {
		t.Errorf("an unrecorded motion status = %q, want %q", unrecorded.Status, motioncert.StatusUnverified)
	}
	if unrecorded.Report != "" {
		t.Errorf("an unrecorded motion names report %q", unrecorded.Report)
	}
}

func TestSelectionModelPublishesCertificationState(t *testing.T) {
	model := runtimeCertificationModel()
	if model.Error != "" {
		t.Fatalf("certification snapshot did not parse: %s", model.Error)
	}
	if model.Source == "" || model.Rule == "" || model.SnapshotAt == "" {
		t.Errorf("certification block is not self-describing: %+v", model)
	}
	if model.IsCurrent {
		t.Error("certification statuses come from dated artifacts and must not be published as current")
	}
	if len(model.Reports) == 0 {
		t.Fatal("certification block names no report")
	}

	ids := motion.Registry.List()
	if model.Passed+model.Failed+model.Unverified != len(ids) {
		t.Errorf("certification counts %d/%d/%d do not partition the %d registered motions",
			model.Passed, model.Failed, model.Unverified, len(ids))
	}
	if model.Failed == 0 {
		t.Error("the checked-in snapshot records non-passing runs, so a zero failed count means the wiring dropped them")
	}
	if model.Unverified == 0 {
		t.Error("most registered motions appear in no report; an all-covered payload means the counts are fabricated")
	}

	// The per-motion status must reach the serialized payload, not just the
	// in-memory model: that is what a picker reads.
	catalog := CompiledRuntimeAnimationCatalog()
	byID := make(map[string]RuntimeMotionOption, len(catalog.Motions))
	for _, option := range catalog.Motions {
		byID[option.ID] = option
	}
	if got := byID["image_parallax_frame"].Certification; got.Status != motioncert.StatusFailed {
		t.Errorf("serialized image_parallax_frame certification = %+v, want failed", got)
	}
	if got := byID["typewriter_clean"].Certification; got.Status != motioncert.StatusPassed {
		t.Errorf("serialized typewriter_clean certification = %+v, want passed", got)
	}
	// At least one registered motion must serialize as unverified: the reports
	// cover a fraction of the registry, and the uncovered rest must not look
	// certified. The ID is read from the payload instead of hardcoded so this
	// assertion survives a report refresh.
	unverifiedID := ""
	for _, option := range catalog.Motions {
		if option.Certification.Status == motioncert.StatusUnverified {
			if option.Certification.Report != "" {
				t.Errorf("unverified motion %q still names report %q", option.ID, option.Certification.Report)
			}
			unverifiedID = option.ID
			break
		}
	}
	if unverifiedID == "" {
		t.Error("every registered motion is covered by a report, so the unverified path is never exercised")
	} else if got := byID[unverifiedID].Certification; got.Status != motioncert.StatusUnverified {
		t.Errorf("serialized %q certification = %+v, want unverified", unverifiedID, got)
	}

	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	for _, key := range []string{`"certification"`, `"is_current"`, `"unverified"`, `"snapshot_at"`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("serialized payload omitted %s", key)
		}
	}
}

// TestCertificationFailsLoudWhenTheSnapshotIsUnavailable pins the failure mode:
// a snapshot error must not be swallowed into a payload that looks certified.
func TestCertificationFailsLoudWhenTheSnapshotIsUnavailable(t *testing.T) {
	model := runtimeCertificationModel()
	if model.Error == "" {
		// The embedded reports parse in the normal checkout; this test only has
		// something to assert in the degraded case.
		t.Skip("embedded certification reports parsed; the degraded path is not reachable here")
	}
	if model.IsCurrent || model.Passed != 0 || model.Failed != 0 {
		t.Errorf("a failed snapshot must not publish passed/failed counts: %+v", model)
	}
	if model.Unverified != len(motion.Registry.List()) {
		t.Errorf("a failed snapshot must mark every motion unverified: %+v", model)
	}
}
