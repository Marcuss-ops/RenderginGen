package overlay

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestOverlayV3CatalogLowersEveryModernMotion exercises the same lowering used
// by semantic_compile. It keeps the catalog gate meaningful: an id may exist
// in JSON and still be unusable if it cannot produce a concrete layer plan.
func TestOverlayV3CatalogLowersEveryModernMotion(t *testing.T) {
	for _, id := range motion.Registry.AppleV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "A MODERN OVERLAY", 120, 6)
		if err != nil {
			t.Fatalf("text motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) == 0 {
			t.Fatalf("text motion %s lowered incompletely: %+v", id, animation)
		}
	}
	for _, id := range motion.Registry.ImageV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "", 120, 6)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 {
			t.Fatalf("image motion %s lowered without layer tracks: %+v", id, animation)
		}
	}
}

func TestImage25DCleanV1CatalogLowersLayerOnlyWithCatalogExit(t *testing.T) {
	ids := motion.Registry.Image25DCleanV1MotionIDs()
	if len(ids) != 8 {
		t.Fatalf("clean 2.5D image catalog has %d motions, want 8", len(ids))
	}
	for _, id := range ids {
		animation, err := animationForMotion(id, nil, "", 120, 0)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) != 0 {
			t.Fatalf("image motion %s did not lower to layer tracks only: %+v", id, animation)
		}
		if !animationUses3D(animation) {
			t.Fatalf("image motion %s did not activate 3D", id)
		}
		for _, track := range animation.Tracks {
			if track.Property == "opacity" {
				last := track.Keyframes[len(track.Keyframes)-1]
				if last.Frame != 119 || last.Value != 0.0 {
					t.Fatalf("image motion %s exit = frame %d value %v, want frame 119 opacity 0", id, last.Frame, last.Value)
				}
			}
		}
	}
}

func TestImageMotionInventoryHasAllCatalogImageAnimations(t *testing.T) {
	inventory := ImageMotionInventory()
	want := map[string][]string{
		"overlay_v3_image": {
			"image_fade_reveal", "image_focus_reveal", "image_scale_reveal",
			"image_slide_left_reveal", "image_slide_right_reveal", "image_parallax_depth_reveal",
			"image_tilt_settle", "image_card_push", "image_diagonal_sweep", "image_soft_focus_reveal",
		},
		"image_25d_clean_v1": {
			"image_25d_depth_float_in", "image_25d_yaw_flip_in", "image_25d_pitch_lift",
			"image_25d_pop_z_bounce", "image_25d_swipe_3d", "image_25d_card_swing",
			"image_25d_blur_focus_in", "image_25d_blur_scale_in",
		},
	}
	if len(inventory) != len(want) {
		t.Fatalf("image inventory groups = %v, want exactly %v", inventory, want)
	}
	all := make(map[string]bool)
	for family, expected := range want {
		got := inventory[family]
		if len(got) != len(expected) {
			t.Fatalf("%s inventory = %v, want %d IDs", family, got, len(expected))
		}
		seen := make(map[string]bool, len(got))
		for _, id := range got {
			if seen[id] {
				t.Errorf("%s inventory repeats %q", family, id)
			}
			seen[id], all[id] = true, true
		}
		for _, id := range expected {
			if !seen[id] {
				t.Errorf("%s inventory is missing catalog motion %q", family, id)
			}
		}
	}
	if len(all) != 18 || len(motion.Registry.ImageOverlayMotionIDs()) != len(all) {
		t.Fatalf("combined image motion inventory = %d, want 18 unique IDs", len(all))
	}
}

func TestTextPresetCatalogContainsOnlyTheTwoSupportedIDs(t *testing.T) {
	got := motion.OverlayPresetIDs(string(PresetText))
	want := []string{PhraseDefaultPresetID, StaticTextSmokePresetID}
	if len(got) != len(want) {
		t.Fatalf("canonical text presets = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical text presets = %v, want exactly %v", got, want)
		}
	}
}

func TestPhraseDefaultAndMotionFamilyRemainIndependent(t *testing.T) {
	if _, err := ResolveOfficialPreset(PhraseDefaultPresetID); err != nil {
		t.Fatal(err)
	}
	if got := len(motion.Registry.FamilyMotionIDs("typewriter")); got != 5 {
		t.Fatalf("typewriter family has %d motions, want 5", got)
	}
	if got := len(motion.Registry.FamilyMotionIDs("web")); got != 0 {
		t.Fatalf("web family has %d placeholder motions, want 0", got)
	}
	if got := len(motion.Registry.FamilyMotionIDs("3d")); got == 0 {
		t.Fatal("3d family should expose catalog motions independently of phrase_default")
	}
}

func TestRuntimeStyleOverrideSchemaContract(t *testing.T) {
	raw, err := os.ReadFile(contractSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, pointer := range []string{
		"#/properties/items/items/properties/params",
		"#/properties/items/items/properties/style",
	} {
		t.Run(pointer, func(t *testing.T) {
			properties := schemaAt(t, schema, pointer)["properties"].(map[string]any)
			font := properties["font_family"].(map[string]any)["enum"].([]any)
			if len(font) != 3 || font[0] != "poppins" || font[1] != "inter" || font[2] != "dejavu_sans" {
				t.Errorf("font_family enum = %v, want [poppins inter dejavu_sans]", font)
			}
			for key, maximum := range map[string]float64{"glow_size": maxRuntimeGlowSize, "stroke_size": maxRuntimeStrokeSize} {
				field := properties[key].(map[string]any)
				if field["type"] != "number" || field["minimum"] != float64(0) || field["maximum"] != maximum {
					t.Errorf("%s schema = %v, want number in [0,%g]", key, field, maximum)
				}
			}
		})
	}
}

func TestRuntimeTextStyleOverridesAcceptInclusiveBounds(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"glow_size":256,"stroke_size":64}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style.Glow == nil || layer.Style.Glow.Radius != maxRuntimeGlowSize {
		t.Fatalf("maximum glow override = %+v", layer.Style.Glow)
	}
	if layer.Style.Stroke == nil || layer.Style.Stroke.Width != maxRuntimeStrokeSize {
		t.Fatalf("maximum stroke override = %+v", layer.Style.Stroke)
	}
}

func TestPhraseDefaultPresetIsTheSingleModernTextPreset(t *testing.T) {
	definition, err := ResolveOfficialPreset(PhraseDefaultPresetID)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Family != PresetText || definition.Motion.ID == "" {
		t.Fatalf("phrase default preset is incomplete: %+v", definition)
	}
	if definition.Style.Stroke == nil || definition.Style.Shadow == nil {
		t.Fatalf("phrase default preset lost legibility styling: %+v", definition.Style)
	}
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-default","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"depth_parallax_reveal","text":"A MODERN 2.5D TITLE","start_ms":0,"end_ms":5000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Animation == nil {
		t.Fatalf("phrase default did not lower to an animated layer: %+v", result.Plan.Layers)
	}
}
