package processor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// failFastJob is a plan whose single item names a template_id that exists in no
// registry row and no legacy alias table.
func failFastJob() *queue.Job {
	return &queue.Job{
		ID:      "failfast-1",
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
		  "schema_version":"renderinggen.overlay-plan.v1",
		  "plan_id":"failfast-1","video_id":"failfast-1",
		  "width":1280,"height":720,"fps_num":30,"fps_den":1,
		  "items":[{"id":"n","kind":"important_phrase","template_id":"DOES_NOT_EXIST_XYZ",
		    "preset_id":"phrase_default","text":"Hello","start_ms":0,"end_ms":1000}]
		}`),
	}
}

// TestPrepareJobRejectsUnknownTemplateBeforeAnyWorkspace pins the fail-fast end
// to end: an unknown template_id is rejected at the prepare boundary, and the
// rejection happens BEFORE the per-job workspace exists. No scratch directory,
// no asset download, no font stage and no GPU lane is spent on a plan the
// compiler could not place — historically the same plan rendered as a
// preset-less text primitive with pixels as the only evidence.
func TestPrepareJobRejectsUnknownTemplateBeforeAnyWorkspace(t *testing.T) {
	jobsRoot := t.TempDir()
	proc := NewWithOptions(jobsRoot, "software", "0.1.0", "http://store:9000",
		storage.New(storage.NewMemory(), storage.Options{}), &fakeRenderer{}, Options{})
	_, err := proc.PrepareJob(context.Background(), failFastJob())
	if err == nil {
		t.Fatal("a template_id that resolves to no registry row must be rejected at prepare")
	}
	if !strings.Contains(err.Error(), "DOES_NOT_EXIST_XYZ") {
		t.Fatalf("the rejection must name the offending template_id, got %v", err)
	}
	entries, readErr := os.ReadDir(jobsRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("a rejected job must not have created a workspace, found %v", names)
	}
}

// The plan-vs-asset gate itself is pinned by materialized_plan_gate_test.go
// (missing/unproven, proven-skip and unsafe-even-proven); the test below only
// adds the font half of the same prepare-boundary contract.

// TestMaterializeBuiltinFontsRejectsUnbundledFont pins the font fail-fast: a
// plan referencing a preset font that exists in no editorial_v1 bundle fails
// the prepare stage instead of reaching Chronon with a dangling font path.
func TestMaterializeBuiltinFontsRejectsUnbundledFont(t *testing.T) {
	plan := &overlay.Plan{Layers: []overlay.Layer{{
		ID: "title", Type: "text",
		Style: &overlay.LayerStyle{Font: "assets/fonts/definitely-not-in-the-bundle-9f3a.otf"},
	}}}
	err := materializeBuiltinFonts(t.TempDir(), plan)
	if err == nil || !strings.Contains(err.Error(), "missing from the editorial_v1 bundle") {
		t.Fatalf("an unbundled preset font must fail the prepare stage, got %v", err)
	}
}
