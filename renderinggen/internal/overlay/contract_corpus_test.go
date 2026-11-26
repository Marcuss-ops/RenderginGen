package overlay

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contractCorpusManifest is the cross-repo corpus authority. The Rust planner
// fixtures prove the request/plan wire; these fixtures prove that the plans
// PipelineGen actually seals compile in RenderingGen and lower to a pinned
// Chronon layer document. Keep the manifest at least ten entries: fewer than
// that stops covering every map path plus the boundary cases.
type contractCorpusManifest struct {
	SchemaVersion string                  `json:"schema_version"`
	Fixtures      []contractCorpusFixture `json:"fixtures"`
}

type contractCorpusFixture struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Kind          string `json:"kind"`
	Expect        string `json:"expect"`
	Layers        int    `json:"layers,omitempty"`
	LayersSHA256  string `json:"layers_sha256,omitempty"`
	ErrorContains string `json:"error_contains,omitempty"`
}

func TestOverlayContractCorpus(t *testing.T) {
	// The corpus is vendored under the RenderingGen repository root so this
	// test is hermetic: a standalone RenderingGen CI checkout must not need a
	// sibling PipelineGen tree or a parent workspace manifest. The workspace
	// copy is checked separately by TestContractCorpusMatchesWorkspaceSources.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "overlay-v2", "manifest.json"))
	if err != nil {
		t.Fatalf("read contract corpus manifest: %v", err)
	}
	var manifest contractCorpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode contract corpus manifest: %v", err)
	}
	if manifest.SchemaVersion != "overlay-contract-corpus.v1" {
		t.Fatalf("unexpected corpus schema %q", manifest.SchemaVersion)
	}
	if len(manifest.Fixtures) < 10 {
		t.Fatalf("contract corpus has %d fixtures, need at least 10", len(manifest.Fixtures))
	}
	seen := make(map[string]struct{}, len(manifest.Fixtures))
	for _, fixture := range manifest.Fixtures {
		if _, duplicate := seen[fixture.ID]; duplicate {
			t.Fatalf("duplicate corpus fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
	}

	for _, fixture := range manifest.Fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			plan, err := os.ReadFile(filepath.Join(root, fixture.Source))
			if err != nil {
				t.Fatalf("read %s: %v", fixture.Source, err)
			}
			if fixture.Kind == "pipelinegen_plan" {
				plan, err = projectPipelineGenPlan(plan)
				if err != nil {
					t.Fatalf("project %s: %v", fixture.ID, err)
				}
			}
			result, compileErr := CompileSemantic(plan)
			switch fixture.Expect {
			case "rejected":
				if compileErr == nil {
					t.Fatalf("plan compiled, want rejection containing %q", fixture.ErrorContains)
				}
				if fixture.ErrorContains != "" && !strings.Contains(compileErr.Error(), fixture.ErrorContains) {
					t.Fatalf("rejection %q does not contain %q", compileErr, fixture.ErrorContains)
				}
			case "compiled":
				if compileErr != nil {
					t.Fatalf("compile: %v", compileErr)
				}
				if got := len(result.Plan.Layers); got != fixture.Layers {
					t.Fatalf("layers = %d, want %d", got, fixture.Layers)
				}
				layers, err := json.Marshal(result.Plan.Layers)
				if err != nil {
					t.Fatal(err)
				}
				got := fmt.Sprintf("%x", sha256.Sum256(layers))
				if got != fixture.LayersSHA256 {
					t.Fatalf("layers sha256 = %s, want %s", got, fixture.LayersSHA256)
				}
			default:
				t.Fatalf("unknown expectation %q", fixture.Expect)
			}
		})
	}
}

// projectPipelineGenPlan applies the two lossy boundary decisions the
// production wire mapper already makes: planner-only sections never enter the
// RenderingGen document, and map glow/pin scope are producer-side selection
// metadata that the map compiler does not consume. Keeping this projection in
// the corpus test (instead of weakening the strict decoder) means a new
// producer-only field fails here until it is either mapped or explicitly
// stripped.
func projectPipelineGenPlan(raw []byte) ([]byte, error) {
	var plan map[string]json.RawMessage
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"chart_cards", "chart_placements", "recap_cards", "recap_placements",
		"timed_scenes", "intake",
	} {
		delete(plan, key)
	}
	plan["schema_version"], _ = json.Marshal(SemanticSchema)
	if itemsRaw := plan["items"]; len(itemsRaw) > 0 {
		projected, err := projectPipelineGenItems(itemsRaw)
		if err != nil {
			return nil, err
		}
		plan["items"] = projected
	}
	return json.Marshal(plan)
}

func projectPipelineGenItems(raw json.RawMessage) (json.RawMessage, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		encoded := item["map"]
		if len(encoded) == 0 {
			continue
		}
		var mapWire map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &mapWire); err != nil {
			return nil, err
		}
		delete(mapWire, "area_glow_radius_km")
		if pinsRaw := mapWire["pins"]; len(pinsRaw) > 0 {
			var pins []map[string]json.RawMessage
			if err := json.Unmarshal(pinsRaw, &pins); err != nil {
				return nil, err
			}
			for _, pin := range pins {
				delete(pin, "scope")
			}
			projectedPins, err := json.Marshal(pins)
			if err != nil {
				return nil, err
			}
			mapWire["pins"] = projectedPins
		}
		projectedMap, err := json.Marshal(mapWire)
		if err != nil {
			return nil, err
		}
		item["map"] = projectedMap
	}
	return json.Marshal(items)
}
