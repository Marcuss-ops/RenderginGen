package editoriallayers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// editorialCorpusManifest is the shared authority of the editorial composition
// corpus. This boundary only reads the lowered fixtures: the composition
// fixtures are the producer's and the Rust planner's business.
type editorialCorpusManifest struct {
	SchemaVersion string                 `json:"schema_version"`
	Capabilities  string                 `json:"capabilities"`
	Fixtures      []editorialCorpusEntry `json:"fixtures"`
}

type editorialCorpusEntry struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Expect  string `json:"expect"`
	Lowered string `json:"lowered,omitempty"`
	Layers  int    `json:"layers,omitempty"`
	Code    string `json:"code,omitempty"`
}

// renderinggenRoot is the RenderingGen repository root, which holds the
// vendored corpus so a standalone checkout stays hermetic.
func renderinggenRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadEditorialCorpus(t *testing.T) (string, editorialCorpusManifest) {
	t.Helper()
	root := renderinggenRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "manifest.json"))
	if err != nil {
		t.Fatalf("read editorial corpus manifest: %v", err)
	}
	var manifest editorialCorpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode editorial corpus manifest: %v", err)
	}
	if manifest.SchemaVersion != "editorial-composition-corpus.v1" {
		t.Fatalf("unexpected corpus schema %q", manifest.SchemaVersion)
	}
	return root, manifest
}

func TestEditorialLayersCorpus(t *testing.T) {
	root, manifest := loadEditorialCorpus(t)
	if len(manifest.Fixtures) < 12 {
		t.Fatalf("corpus has %d fixtures, need at least 12", len(manifest.Fixtures))
	}
	seen := make(map[string]struct{}, len(manifest.Fixtures))
	compiled := 0
	for _, fixture := range manifest.Fixtures {
		if _, duplicate := seen[fixture.ID]; duplicate {
			t.Fatalf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		if fixture.Expect != "compiled" {
			continue
		}
		compiled++
		t.Run(fixture.ID, func(t *testing.T) {
			// The manifest path is workspace-relative; the vendored copy keeps
			// the same file name under the local `lowered/` directory.
			raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "lowered", filepath.Base(fixture.Lowered)))
			if err != nil {
				t.Fatalf("read lowered fixture: %v", err)
			}
			plan, err := Admit(raw)
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			if len(plan.Document.Layers) != fixture.Layers {
				t.Fatalf("admitted layers = %d, want %d", len(plan.Document.Layers), fixture.Layers)
			}
			if plan.RoleCounts[RoleBackground] > 1 {
				t.Fatalf("admitted %d backgrounds", plan.RoleCounts[RoleBackground])
			}
		})
	}
	if compiled < 8 {
		t.Fatalf("corpus has %d compiled fixtures, need at least 8", compiled)
	}
}

func TestEditorialLayersCalloutIsAnchored(t *testing.T) {
	root, _ := loadEditorialCorpus(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "lowered", "map_photo_callout.layers.json"))
	if err != nil {
		t.Fatalf("read map fixture: %v", err)
	}
	plan, err := Admit(raw)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if got := plan.RoleCounts[RoleMapCallout]; got != 1 {
		t.Fatalf("callouts = %d, want 1", got)
	}
	if _, ok := plan.Callouts["milano"]; !ok {
		t.Fatalf("callout is not anchored on pin milano: %v", plan.Callouts)
	}
}

func TestEditorialLayersAttachmentsHangFromTheirParent(t *testing.T) {
	root, _ := loadEditorialCorpus(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "lowered", "entity_date_attachment.layers.json"))
	if err != nil {
		t.Fatalf("read attachment fixture: %v", err)
	}
	plan, err := Admit(raw)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	attachments := plan.Attachments["entity_trump"]
	if len(attachments) != 3 {
		t.Fatalf("attachments = %v, want three under entity_trump", attachments)
	}
}

// TestEditorialLayersAdmissionIsFailClosed doctors one admitted document at a
// time: each mutation must be rejected with its own code.
func TestEditorialLayersAdmissionIsFailClosed(t *testing.T) {
	root, _ := loadEditorialCorpus(t)
	base := func(name string) *Document {
		raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "lowered", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		document, err := Decode(raw)
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return document
	}

	t.Run("unknown role", func(t *testing.T) {
		document := base("chapter_explicit.layers.json")
		document.Layers[0].Role = "hologram"
		if _, err := document.Admit(); codeOf(err) != CodeUnknownRole {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeUnknownRole, err)
		}
	})
	t.Run("unordered", func(t *testing.T) {
		document := base("persistent_background.layers.json")
		document.Layers[0], document.Layers[3] = document.Layers[3], document.Layers[0]
		if _, err := document.Admit(); codeOf(err) != CodeUnordered {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeUnordered, err)
		}
	})
	t.Run("orphan attachment", func(t *testing.T) {
		document := base("entity_date_attachment.layers.json")
		document.Layers = document.Layers[1:]
		if _, err := document.Admit(); codeOf(err) != CodeOrphanAttachment {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeOrphanAttachment, err)
		}
	})
	t.Run("callout without anchor", func(t *testing.T) {
		document := base("map_photo_callout.layers.json")
		document.Layers[1].AnchorPinID = ""
		if _, err := document.Admit(); codeOf(err) != CodeCalloutUnanchored {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeCalloutUnanchored, err)
		}
	})
	t.Run("callout with unverified asset", func(t *testing.T) {
		document := base("map_photo_callout.layers.json")
		document.Layers[1].Asset.SHA256 = ""
		if _, err := document.Admit(); codeOf(err) != CodeCalloutAssetUnverified {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeCalloutAssetUnverified, err)
		}
	})
	t.Run("duplicate date", func(t *testing.T) {
		document := base("entity_date_attachment.layers.json")
		document.Layers = append(document.Layers, Layer{
			LayerID:    "date_standalone",
			Role:       RoleDate,
			StartUS:    document.Window.StartUS,
			DurationUS: 1000000,
			Z:          20,
			DateValue:  "2025",
		})
		if _, err := document.Admit(); codeOf(err) != CodeDuplicateDate {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeDuplicateDate, err)
		}
	})
	t.Run("map inside a chain", func(t *testing.T) {
		document := base("map_photo_callout.layers.json")
		document.Layers[0].LinkGroup = "g"
		if _, err := document.Admit(); codeOf(err) != CodeMapChainForbidden {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeMapChainForbidden, err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		raw := []byte(`{
          "schema_version": "editorial_layers.v1",
          "composition_id": "comp", "scene_id": "scene",
          "window": {"start_us": 0, "duration_us": 1000000},
          "layers": [],
          "future_layer_field": true
        }`)
		if _, err := Admit(raw); codeOf(err) != CodeUnknownField {
			t.Fatalf("code = %q, want %q (%v)", codeOf(err), CodeUnknownField, err)
		}
	})
}

func codeOf(err error) string {
	if contractErr, ok := err.(*Error); ok {
		return contractErr.Code
	}
	return ""
}

// TestEditorialLayersCorpusMatchesWorkspaceSources keeps the vendored copy
// honest when the full workspace is checked out.
func TestEditorialLayersCorpusMatchesWorkspaceSources(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Dir(repoRoot)
	if info, err := os.Stat(filepath.Join(workspace, "refactored")); err != nil || !info.IsDir() {
		t.Skip("PipelineGen workspace checkout is not available; vendored corpus cannot be compared")
	}
	workspaceCorpus := filepath.Join(workspace, "contracts", "editorial-composition-v1")
	if _, err := os.Stat(workspaceCorpus); err != nil {
		t.Skip("workspace editorial corpus is not available; vendored corpus cannot be compared")
	}

	compare := func(relative string) {
		t.Helper()
		want, err := os.ReadFile(filepath.Join(workspaceCorpus, relative))
		if err != nil {
			t.Fatalf("read workspace copy of %s: %v", relative, err)
		}
		got, err := os.ReadFile(filepath.Join(repoRoot, "contracts", "editorial-composition-v1", relative))
		if err != nil {
			t.Fatalf("read vendored copy of %s: %v", relative, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("vendored %s is stale: copy %s to the RenderingGen corpus", relative, filepath.Join(workspaceCorpus, relative))
		}
	}

	compare("manifest.json")
	entries, err := os.ReadDir(filepath.Join(workspaceCorpus, "lowered"))
	if err != nil {
		t.Fatalf("read workspace lowered corpus: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		compare(filepath.Join("lowered", entry.Name()))
	}
}
