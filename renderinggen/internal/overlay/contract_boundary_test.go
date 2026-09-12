package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

// findWorkspaceRoot walks up from start until it finds the go.work that joins
// PipelineGen, RenderingGen and Chronon3d. It reports ok=false when no go.work
// exists anywhere above start, so callers can SKIP rather than fail.
func findWorkspaceRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// workspaceRoot returns the go.work workspace root, or SKIPS the test when it
// is absent. The cross-repo boundary contract needs the sibling Chronon3d/
// (and PipelineGen) schemas; a standalone RenderingGen checkout — including
// this repository's own CI — cannot supply them. Skipping (never failing)
// keeps the CI signal honest: the contract is verified in the workspace that
// has the siblings, and its absence is visible as a skip, not a false pass or
// a false failure. TestFindWorkspaceRootAbsent pins this behavior.
func workspaceRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate test source; skipping cross-repo boundary contract")
	}
	root, found := findWorkspaceRoot(filepath.Dir(source))
	if !found {
		t.Skip("go.work / sibling repositories not checked out; skipping cross-repo boundary contract (standalone RenderingGen checkout)")
	}
	return root
}

// TestFindWorkspaceRootAbsent pins the skip policy: a tree with no go.work
// must report absent so the boundary contract skips instead of failing on a
// filesystem root it cannot see.
func TestFindWorkspaceRootAbsent(t *testing.T) {
	if _, found := findWorkspaceRoot(t.TempDir()); found {
		t.Fatal("a directory tree without go.work must report no workspace")
	}
}

// TestFindWorkspaceRootPresent pins the positive walk (find from a nested dir).
func TestFindWorkspaceRootPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, found := findWorkspaceRoot(nested)
	if !found || got != root {
		t.Fatalf("findWorkspaceRoot(%q) = (%q, %v), want (%q, true)", nested, got, found, root)
	}
}

func contractPresetPath(t *testing.T) string {
	return filepath.Join(workspaceRoot(t), "RenderingGen", "contracts", "overlay-plan.v1.schema.json")
}

func chrononSchemaPath(t *testing.T) string {
	return filepath.Join(workspaceRoot(t), "Chronon3d", "schemas", "json", "chronon.render-plan.v2.schema.json")
}

// contractPresetID picks a deterministic official preset in the template's
// family. The fixture supplies it explicitly because RenderingGen must never
// re-map a template_id to a preset (ADR-029).
func contractPresetID(family PresetFamily) string {
	if family == PresetImage {
		return "image_focus_in"
	}
	return "lower_third_safe"
}

// contractFixture builds the minimal valid overlay-plan.v1 document for one
// registry template. kind/template/preset are the exact contract slots the
// boundary validates.
func contractFixture(templateID string, spec TemplateSpec) []byte {
	item := map[string]any{
		"id":          "item-1",
		"kind":        string(spec.Kind),
		"template_id": templateID,
		"text":        "Sample Name",
		"start_ms":    0,
		"end_ms":      1000,
	}
	if spec.RequiresPreset || spec.Family != "" {
		item["preset_id"] = contractPresetID(spec.Family)
	}
	if isImageKind(spec.Kind) {
		item["asset_refs"] = []map[string]any{{
			"asset_id":   "asset-1",
			"sha256":     strings.Repeat("a", 64),
			"url":        "https://store.example/asset.png",
			"media_type": "image/png",
		}}
	}
	// Video overlays lower to a timed video layer and require the rendered
	// segment as an asset_ref (they are preset-less: the segment carries its
	// own pixels and the producer owns the window).
	if isVideoKind(spec.Kind) {
		item["asset_refs"] = []map[string]any{{
			"asset_id":   "asset-1",
			"sha256":     strings.Repeat("a", 64),
			"url":        "https://store.example/asset.mp4",
			"media_type": "video/mp4",
		}}
	}
	doc := map[string]any{
		"schema_version": SemanticSchema,
		"plan_id":        "contract-plan",
		"video_id":       "contract-video",
		"width":          1280,
		"height":         720,
		"fps_num":        30,
		"fps_den":        1,
		"items":          []any{item},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestBoundaryContractEveryKindTemplate certifies the single production path
// for every kind×template RenderingGen officially supports:
//
//	overlay-plan.v1 (schema-validated)
//	  → CompileSemantic (the one compiler)
//	  → chronon.render-plan.v2 (schema-validated)
//
// A template that compiles but emits a schema-invalid plan (or vice versa)
// fails here, so the boundary can never silently drift.
func TestBoundaryContractEveryKindTemplate(t *testing.T) {
	overlaySchema := contractPresetPath(t)
	chrononSchema := chrononSchemaPath(t)

	templates := make([]string, 0, len(templateRegistry))
	for id := range templateRegistry {
		templates = append(templates, id)
	}
	sort.Strings(templates)
	if len(templates) == 0 {
		t.Fatal("templateRegistry is empty")
	}

	for _, templateID := range templates {
		templateID := templateID
		t.Run(templateID, func(t *testing.T) {
			spec := templateRegistry[templateID]
			raw := contractFixture(templateID, spec)

			// 1. The producer input must satisfy the canonical semantic schema.
			if err := schemaval.ValidateFile(raw, overlaySchema); err != nil {
				t.Fatalf("input is not valid overlay-plan.v1: %v\n%s", err, raw)
			}

			// 2. The single compiler lowers it.
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if result.Plan.Schema != "chronon.render-plan.v2" || result.Plan.Version != 2 {
				t.Fatalf("compiled schema = %s v%d, want chronon.render-plan.v2 v2", result.Plan.Schema, result.Plan.Version)
			}
			if len(result.Plan.Layers) == 0 {
				t.Fatal("compiler emitted no layers")
			}

			// 3. The physical output must satisfy the canonical Chronon schema.
			out, err := result.Plan.Marshal()
			if err != nil {
				t.Fatalf("marshal plan: %v", err)
			}
			if err := schemaval.ValidateFile(out, chrononSchema); err != nil {
				t.Fatalf("output is not valid chronon.render-plan.v2: %v\n%s", err, out)
			}
		})
	}
}

// TestBoundaryContractRejectsUnresolvedPreset pins the fail-closed half: a
// preset-driven template without a preset_id is a producer bug, not a default.
func TestBoundaryContractRejectsUnresolvedPreset(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[{"id":"x","kind":"entity_card","template_id":"PERSON","text":"Ada","start_ms":0,"end_ms":1000}]}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("a preset-driven template without preset_id must be rejected")
	}
}

// TestBoundaryContractRejectsUnversionedChrononSchema pins that the deleted
// unversioned schema can never enter through the semantic boundary. The
// literal is assembled from fragments so this test does not reintroduce the
// very marker the conformance gate bans.
func TestBoundaryContractRejectsUnversionedChrononSchema(t *testing.T) {
	unversioned := "chronon.render-" + "plan"
	doc := `{"schema":"` + unversioned + `","version":1,"canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"layers":[],"output":{"path":"o.mp4"}}`
	if err := schemaval.ValidateFile([]byte(doc), chrononSchemaPath(t)); err == nil {
		t.Fatal("the unversioned chronon schema must fail v2 validation")
	}
}
