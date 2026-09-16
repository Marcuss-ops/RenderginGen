package vramprobe

import (
	"os"
	"path/filepath"
	"testing"
)

// writePlan writes a plan document to a temp file and returns its path.
func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const concretePlan = `{
  "schema": "chronon.render-plan.v2",
  "version": 2,
  "job_id": "job-1",
  "canvas": {"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1, "duration_frames": 120},
  "layers": [{"id": "bg", "type": "color"}, {"id": "t", "type": "text"}, {"id": "t2", "type": "text"}],
  "output": {"path": "result.mp4", "format": "mp4", "codec": "h264"}
}`

// TestReadPoolPlanRecordsTheShapeAsFacts pins that the evidence names the plan it
// rendered and its shape, and that layer types are recorded as facts (deduped and
// sorted) rather than reduced to a guessed lane label.
func TestReadPoolPlanRecordsTheShapeAsFacts(t *testing.T) {
	shape, digest, err := readPoolPlan(writePlan(t, concretePlan))
	if err != nil {
		t.Fatalf("readPoolPlan: %v", err)
	}
	if shape.Width != 1920 || shape.Height != 1080 || shape.Frames != 120 || shape.FPSNum != 24 {
		t.Fatalf("shape = %+v, want 1920x1080 120 frames @ 24fps", shape)
	}
	if shape.Codec != "h264" || shape.OutputFormat != "mp4" {
		t.Fatalf("output facts = %q/%q", shape.Codec, shape.OutputFormat)
	}
	if len(shape.LayerTypes) != 2 || shape.LayerTypes[0] != "color" || shape.LayerTypes[1] != "text" {
		t.Fatalf("layer types = %v, want [color text]", shape.LayerTypes)
	}
	// The digest identifies the exact bytes measured, so two reports can be told
	// apart when they differ only in the plan.
	if len(digest) != 64 {
		t.Fatalf("plan digest = %q, want a sha256 hex digest", digest)
	}
}

// TestReadPoolPlanRefusesWhatTheDaemonCannotRender pins the fail-closed
// behaviour: the harness renders the CONCRETE plan, and a semantic plan, a
// missing file or an unusable canvas must be reported instead of producing a
// number for something nobody rendered.
func TestReadPoolPlanRefusesWhatTheDaemonCannotRender(t *testing.T) {
	semantic := `{"schema":"renderinggen.overlay-plan.v1","canvas":{"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_frames":120}}`
	if _, _, err := readPoolPlan(writePlan(t, semantic)); err == nil {
		t.Error("a semantic plan must be rejected: the daemon cannot render it")
	}
	noFrames := `{"schema":"chronon.render-plan.v2","canvas":{"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_frames":0}}`
	if _, _, err := readPoolPlan(writePlan(t, noFrames)); err == nil {
		t.Error("a plan with no frames must be rejected")
	}
	if _, _, err := readPoolPlan(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing plan must be rejected")
	}
}

// TestJobsIdenticalRequiresEveryArtifact pins the identity contract the report
// publishes: identical bytes mean the two runtimes rendered the same workload;
// a failed job or a differing artifact must NOT be reported as identical.
func TestJobsIdenticalRequiresEveryArtifact(t *testing.T) {
	same := []PoolJobFacts{{SHA256: "aa"}, {SHA256: "aa"}}
	if !jobsIdentical(same) {
		t.Error("two artifacts with the same digest are identical")
	}
	differing := []PoolJobFacts{{SHA256: "aa"}, {SHA256: "bb"}}
	if jobsIdentical(differing) {
		t.Error("different digests are not identical")
	}
	failed := []PoolJobFacts{{SHA256: "aa"}, {SHA256: "", FailedWith: "boom"}}
	if jobsIdentical(failed) {
		t.Error("a failed job must not count as an identical artifact")
	}
	if jobsIdentical(nil) {
		t.Error("no jobs is not an identical pair")
	}
}
