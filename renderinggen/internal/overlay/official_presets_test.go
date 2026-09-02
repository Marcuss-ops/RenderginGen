package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOfficialPresetCatalog(t *testing.T) {
	if got := len(officialPresets); got != 30 {
		t.Fatalf("official preset count = %d, want 30", got)
	}
	for id, d := range officialPresets {
		if id == "" || d.ID != id {
			t.Errorf("invalid catalog identity: key=%q definition=%+v", id, d)
		}
		if d.Family != PresetText && d.Family != PresetImage {
			t.Errorf("%s has invalid family %q", id, d.Family)
		}
		if d.Layout.Anchor == "" || d.Motion.Name == "" || d.Motion.Unit == "" || d.Motion.Enter <= 0 || d.Motion.Exit <= 0 {
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
		if resolved.ID != id || resolved.Motion.Name == "" {
			t.Fatalf("%s resolved empty: %+v", id, resolved)
		}
		raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"canary-` + id + `","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` + item + `]}`)
		compiled, _, semantic, err := CompileIfSemantic(raw)
		if err != nil || !semantic {
			t.Fatalf("%s compile: semantic=%v err=%v", id, semantic, err)
		}
		var plan concretePlan
		if err := json.Unmarshal(compiled, &plan); err != nil {
			t.Fatalf("%s concrete decode: %v", id, err)
		}
		if len(plan.Layers) != 1 || plan.Layers[0].Animation == nil || len(plan.Layers[0].Animation.Tracks) == 0 {
			t.Fatalf("%s was not materially lowered: %+v", id, plan.Layers)
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
				Layers []struct {
					Preset string `json:"preset"`
				} `json:"layers"`
			} `json:"render_plan"`
		}
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		for _, layer := range job.RenderPlan.Layers {
			if layer.Preset != "" {
				if _, ok := officialPresets[layer.Preset]; !ok {
					t.Errorf("%s references unknown official preset %q", entry.Name(), layer.Preset)
				}
			}
		}
	}
}
