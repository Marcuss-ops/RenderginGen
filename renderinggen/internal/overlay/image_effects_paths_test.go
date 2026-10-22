package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func effectTypesOf(t *testing.T, result CompileResult, layerID string) []string {
	t.Helper()
	for _, layer := range result.Plan.Layers {
		if layer.ID != layerID {
			continue
		}
		types := make([]string, 0, len(layer.Effects))
		for _, effect := range layer.Effects {
			types = append(types, effect.Type)
		}
		return types
	}
	t.Fatalf("layer %q missing from compiled plan: %+v", layerID, result.Plan.Layers)
	return nil
}

// TestEntityCardRuntimeEffectsDecoratePortrait proves the entity-card path
// applies params.effects even though it does not lower through compileImageItem.
func TestEntityCardRuntimeEffectsDecoratePortrait(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"entity-effects","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,
		"items":[{"id":"person","entity_id":"person:ada","kind":"entity_card","template_id":"PERSON","preset_id":"phrase_default",
		"image_preset_id":"image_focus_in","text":"Ada Lovelace","start_ms":0,"end_ms":3000,"duration_ms":3000,
		"params":{"effects":[{"type":"vignette","params":{"amount":0.2,"radius":0.8,"softness":0.5}}]},
		"asset_refs":[{"asset_id":"ada","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/ada.png","media_type":"image/png"}]}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile entity card effects: %v", err)
	}
	types := effectTypesOf(t, result, "person:image")
	if len(types) != 1 || types[0] != "vignette" {
		t.Fatalf("entity portrait effects = %v, want one vignette", types)
	}
}

// TestEntityGroupRuntimeEffectsDecorateEveryMember proves a group-level stack
// reaches all member images, matching composite item semantics.
func TestEntityGroupRuntimeEffectsDecorateEveryMember(t *testing.T) {
	result := compileGroupPlan(t, groupTestPlan(
		map[string]any{"effects": []any{map[string]any{"type": "glow", "params": map[string]any{"radius": 12.0, "intensity": 0.4}}}},
		groupTestMembers(),
	))
	for _, id := range []string{"duo:a:image", "duo:b:image"} {
		types := effectTypesOf(t, result, id)
		if len(types) != 1 || types[0] != "glow" {
			t.Fatalf("group member %q effects = %v, want one glow", id, types)
		}
	}
}

// childEffectsPlan builds an image item whose two children carry independent
// runtime stacks. The motion decides which recipe lowering runs.
func childEffectsPlan(t *testing.T, motionField, motionID string) []byte {
	t.Helper()
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "child-effects",
		"video_id":       "v",
		"width":          1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "duo", "kind": "image", "template_id": "image_popup",
			motionField: motionID, "start_ms": 0, "end_ms": 5000,
			"asset_refs": []any{
				map[string]any{"asset_id": "left", "sha256": strings.Repeat("a", 64), "url": "https://example.test/left.png", "media_type": "image/png"},
				map[string]any{"asset_id": "right", "sha256": strings.Repeat("b", 64), "url": "https://example.test/right.png", "media_type": "image/png"},
			},
			"image_layers": []any{
				map[string]any{
					"id": "left", "asset_id": "left", "start_ms": 0, "end_ms": 5000, "preset_id": "image_focus_in",
					"params": map[string]any{"effects": []any{map[string]any{"type": "glow", "params": map[string]any{"radius": 10.0, "intensity": 0.1}}}},
				},
				map[string]any{
					"id": "right", "asset_id": "right", "start_ms": 0, "end_ms": 5000, "preset_id": "image_focus_in",
					"params": map[string]any{"effects": []any{map[string]any{"type": "noise", "params": map[string]any{"amount": 0.05, "size": 1.0, "seed": 3.0, "animated": true, "color_mode": "rgb"}}}},
				},
			},
		}},
	}
	if motionField == "motion_id" {
		plan["items"].([]any)[0].(map[string]any)["motion_params"] = map[string]any{"active_layer_id": "left"}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRecipeChildrenKeepTheirRuntimeEffects covers both recipe lowerings that
// build member layers directly instead of through the composite path.
func TestRecipeChildrenKeepTheirRuntimeEffects(t *testing.T) {
	cases := []struct {
		name, field, motion string
	}{
		{"image group recipe", "group_motion_id", "duo_soft_bloom_rise"},
		{"legacy multi-image recipe", "motion_id", "image_stack_focus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := childEffectsPlan(t, tc.field, tc.motion)
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile %s: %v", tc.name, err)
			}
			if types := effectTypesOf(t, result, "duo:left:image"); len(types) != 1 || types[0] != "glow" {
				t.Fatalf("left member effects = %v, want one glow", types)
			}
			if types := effectTypesOf(t, result, "duo:right:image"); len(types) != 1 || types[0] != "noise" {
				t.Fatalf("right member effects = %v, want one noise", types)
			}
		})
	}
}
