package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOfficialPresetCatalog(t *testing.T) {
	if got := len(officialPresets); got != 51 {
		t.Fatalf("official preset count = %d, want 51", got)
	}
	for id, d := range officialPresets {
		if id == "" || d.ID != id {
			t.Errorf("invalid catalog identity: key=%q definition=%+v", id, d)
		}
		if d.Family != PresetText && d.Family != PresetImage {
			t.Errorf("%s has invalid family %q", id, d.Family)
		}
		staticSmoke := id == "static_text_smoke"
		if d.Layout.Anchor == "" || (!staticSmoke && (d.Motion.ID == "" || d.Motion.Unit == "" || d.Motion.Enter <= 0 || d.Motion.Exit <= 0)) {
			t.Errorf("%s has incomplete materialization: %+v", id, d)
		}
		if d.Family == PresetText && (d.Style.FontFamily == "" || d.Style.FontSize <= 0 || len(d.Style.Fill) != 4) {
			t.Errorf("%s has incomplete text definition: %+v", id, d)
		}
		if d.Family == PresetImage && (d.Layout.BoxWidth <= 0 || d.Layout.BoxHeight <= 0 || d.Layout.Fit == "") {
			t.Errorf("%s has incomplete image definition: %+v", id, d)
		}
	}
}

func TestOfficialPresetFamilyValidation(t *testing.T) {
	if _, err := resolveOfficialPreset("caption_card", "image"); err == nil {
		t.Fatal("text preset accepted as image")
	}
	if _, err := resolveOfficialPreset("image_focus_in", "text"); err == nil {
		t.Fatal("image preset accepted as text")
	}
	if _, err := resolveOfficialPreset("does_not_exist", "text"); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestEveryOfficialPresetCompilesAndMaterializes(t *testing.T) {
	for _, id := range officialPresetIDs() {
		family := string(PresetText)
		item := `{"id":"canary","template_id":"IMPORTANT_PHRASE","preset_id":"` + id + `","text":"PRESET TEST","start_ms":0,"end_ms":3000}`
		if officialPresets[id].Family == PresetImage {
			family = string(PresetImage)
			item = `{"id":"canary","template_id":"IMAGE_OVERLAY","preset_id":"` + id + `","start_ms":0,"end_ms":3000,"asset_refs":[{"asset_id":"image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/image.png","media_type":"image/png"}]}`
		}
		resolved, err := resolveOfficialPreset(id, family)
		if err != nil {
			t.Fatalf("%s resolve: %v", id, err)
		}
		staticSmoke := id == "static_text_smoke"
		if resolved.ID != id || (!staticSmoke && resolved.Motion.ID == "") {
			t.Fatalf("%s resolved empty: %+v", id, resolved)
		}
		raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"canary-` + id + `","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` + item + `]}`)
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("%s compile: %v", id, err)
		}
		compiled := result.Plan
		if len(compiled.Layers) != 1 {
			t.Fatalf("%s was not materially lowered: %+v", id, compiled.Layers)
		}
		// Motion is materialized either as layer tracks (fades, slides,
		// scales) or as text animators (word_reveal/character_cascade carry
		// their per-glyph/word sweep there) — an animated preset that ships
		// neither is the "silently static" regression.
		if !staticSmoke {
			layer := compiled.Layers[0]
			hasTracks := layer.Animation != nil && len(layer.Animation.Tracks) > 0
			if !hasTracks && len(layer.TextAnimators) == 0 {
				t.Fatalf("%s was not materially lowered: %+v", id, layer)
			}
		}
	}
}

func TestNoFixtureReferencesUnknownOfficialPreset(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	stylesDir := filepath.Join(filepath.Dir(source), "../../../testdata/styles")
	entries, err := os.ReadDir(stylesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(stylesDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var job struct {
			RenderPlan struct {
				Items []struct {
					PresetID string `json:"preset_id"`
				} `json:"items"`
			} `json:"render_plan"`
		}
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		for _, item := range job.RenderPlan.Items {
			if item.PresetID != "" {
				if _, ok := officialPresets[item.PresetID]; !ok {
					t.Errorf("%s references unknown official preset %q", entry.Name(), item.PresetID)
				}
			}
		}
	}
}

func TestAppleStyleFixturesAreDistinctAndComplete(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	stylesDir := filepath.Join(filepath.Dir(source), "../../../testdata/styles")
	entries, err := os.ReadDir(stylesDir)
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(stylesDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var job struct {
			RenderPlan struct {
				SchemaVersion string `json:"schema_version"`
				StyleProfile  string `json:"style_profile"`
				Items         []struct {
					Kind     string `json:"kind"`
					Template string `json:"template_id"`
					PresetID string `json:"preset_id"`
				} `json:"items"`
			} `json:"render_plan"`
			Assets []struct {
				LogicalPath string `json:"logical_path"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if job.RenderPlan.SchemaVersion != "renderinggen.overlay-plan.v1" {
			t.Errorf("%s: render_plan must be the semantic overlay-plan.v1 contract, got %q", entry.Name(), job.RenderPlan.SchemaVersion)
		}
		if job.RenderPlan.StyleProfile == "" {
			t.Errorf("%s: missing style_profile", entry.Name())
		}
		var signature strings.Builder
		for _, item := range job.RenderPlan.Items {
			if item.PresetID != "" {
				signature.WriteString(item.Kind + ":" + item.Template + ":" + item.PresetID + "|")
			}
		}
		if signature.Len() == 0 {
			t.Errorf("%s: no visual layers", entry.Name())
		}
		profiles[job.RenderPlan.StyleProfile] = signature.String()
	}
	if len(profiles) < 3 {
		t.Fatalf("expected three Apple style profiles, got %d", len(profiles))
	}
	seen := map[string]string{}
	for profile, signature := range profiles {
		if previous, exists := seen[signature]; exists {
			t.Fatalf("Apple style profiles %q and %q are visually identical", previous, profile)
		}
		seen[signature] = profile
	}
}
