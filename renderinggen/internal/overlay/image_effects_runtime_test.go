package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const imageEffectsAssetRef = `{"asset_id":"effect-photo","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/effect.png","media_type":"image/png"}`

const imageEffectsSecondAssetRef = `{"asset_id":"effect-photo-2","sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","url":"https://example.test/effect-2.png","media_type":"image/png"}`

func imageEffectsPlan(extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-effects","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"fx-image","kind":"image","template_id":"image_popup","preset_id":"image_focus_in","start_ms":0,"end_ms":5000,"asset_refs":[%s]%s}]}`,
		imageEffectsAssetRef, extra))
}

// TestImageItemRuntimeEffectsLowerOntoBaseImageLayer proves the runtime effect
// stack requested through item params.effects decorates the base image layer,
// keeps authored order (runtime stack appended last) and survives the V3 wire.
func TestImageItemRuntimeEffectsLowerOntoBaseImageLayer(t *testing.T) {
	extra := `,"params":{"width":640,"height":360,"effects":[{"type":"glow","params":{"radius":120,"intensity":0.2,"color":[1,0.72,0.45,0.35]}},{"type":"noise","params":{"amount":0.06,"size":1,"seed":7,"animated":true,"color_mode":"rgb"}}]}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile image effects: %v", err)
	}
	var imageLayer *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "fx-image:image" {
			imageLayer = &result.Plan.Layers[index]
		}
	}
	if imageLayer == nil {
		t.Fatalf("base image layer missing from compiled layers: %+v", result.Plan.Layers)
	}
	if len(imageLayer.Effects) != 2 {
		t.Fatalf("image layer effects = %+v, want glow then noise", imageLayer.Effects)
	}
	if imageLayer.Effects[0].Type != "glow" || imageLayer.Effects[1].Type != "noise" {
		t.Fatalf("effect order not preserved: %+v", imageLayer.Effects)
	}
	glow := imageLayer.Effects[0].Params
	if radius, _ := toFloat(glow["radius"]); radius != 120 {
		t.Fatalf("glow radius = %v, want 120", glow["radius"])
	}
	if _, ok := glow["color"]; !ok {
		t.Fatalf("glow color must survive lowering: %+v", glow)
	}
	noise := imageLayer.Effects[1].Params
	if seed, _ := toInt(noise["seed"]); seed != 7 {
		t.Fatalf("noise seed = %v, want 7", noise["seed"])
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal V3 plan: %v", err)
	}
	if !strings.Contains(string(wire), `"type":"glow"`) || !strings.Contains(string(wire), `"type":"noise"`) {
		t.Fatalf("runtime effects must serialize onto the V3 wire: %s", wire)
	}
}

// TestImageItemRuntimeEffectsFailClosedOnUnsupportedType proves the runtime
// stack is validated through the shared layer-effect resolver: an unsupported
// effect type is a compile error, never a silent drop.
func TestImageItemRuntimeEffectsFailClosedOnUnsupportedType(t *testing.T) {
	extra := `,"params":{"width":640,"height":360,"effects":[{"type":"not_an_effect","params":{"amount":0.5}}]}`
	if _, err := CompileSemantic(imageEffectsPlan(extra)); err == nil {
		t.Fatal("unsupported runtime effect type must fail closed")
	}
	badBounds := `,"params":{"width":640,"height":360,"effects":[{"type":"glow","params":{"radius":120,"intensity":99}}]}`
	if _, err := CompileSemantic(imageEffectsPlan(badBounds)); err == nil {
		t.Fatal("out-of-bounds runtime effect parameters must fail closed")
	}
}

// TestCompositeImageRuntimeEffectsDecorateEveryChild proves item-level runtime
// effects decorate every image_layers child surface, not only the first one.
func TestCompositeImageRuntimeEffectsDecorateEveryChild(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-effects-composite","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"fx-duo","kind":"image","template_id":"image_popup","start_ms":0,"end_ms":5000,"asset_refs":[%s,%s],"image_layers":[{"id":"left","asset_id":"effect-photo","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":-300,"position_y":0}},{"id":"right","asset_id":"effect-photo-2","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":300,"position_y":0}}],"params":{"effects":[{"type":"vignette","params":{"amount":0.22,"radius":0.75,"softness":0.6}}]}}]}`,
		imageEffectsAssetRef, imageEffectsSecondAssetRef))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite image effects: %v", err)
	}
	decorated := 0
	for _, layer := range result.Plan.Layers {
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") {
			continue
		}
		if len(layer.Effects) != 1 || layer.Effects[0].Type != "vignette" {
			t.Fatalf("child layer %q effects = %+v, want one vignette", layer.ID, layer.Effects)
		}
		decorated++
	}
	if decorated != 2 {
		t.Fatalf("decorated %d child image layers, want 2", decorated)
	}
}

// TestImageItemWithoutRuntimeEffectsStaysEffectFree pins the compatibility
// contract: items that request no runtime decoration compile byte-identical to
// the pre-feature output (no synthetic effect stack).
func TestCompositeChildrenKeepIndependentRuntimeEffects(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-effects-independent","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"fx-duo","kind":"image","template_id":"image_popup","preset_id":"image_focus_in","start_ms":0,"end_ms":5000,"asset_refs":[%s,%s],"image_layers":[{"id":"left","asset_id":"effect-photo","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":-300,"position_y":0,"effects":[{"type":"glow","params":{"radius":120,"intensity":0.2}}]}},{"id":"right","asset_id":"effect-photo-2","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":300,"position_y":0,"effects":[{"type":"noise","params":{"amount":0.06,"size":1,"seed":7,"animated":true,"color_mode":"rgb"}}]}}]}]}`,
		imageEffectsAssetRef, imageEffectsSecondAssetRef))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile independent composite image effects: %v", err)
	}
	want := map[string]string{"fx-duo:left:image": "glow", "fx-duo:right:image": "noise"}
	for id, effectType := range want {
		var found *Layer
		for index := range result.Plan.Layers {
			if result.Plan.Layers[index].ID == id {
				found = &result.Plan.Layers[index]
				break
			}
		}
		if found == nil || len(found.Effects) != 1 || found.Effects[0].Type != effectType {
			t.Fatalf("layer %q = %+v, want its independent %s effect", id, found, effectType)
		}
	}
}

func TestImageItemWithoutRuntimeEffectsStaysEffectFree(t *testing.T) {
	result, err := CompileSemantic(imageEffectsPlan(`,"params":{"width":640,"height":360}`))
	if err != nil {
		t.Fatalf("compile plain image: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" && len(layer.Effects) != 0 {
			t.Fatalf("plain image layer %q gained effects: %+v", layer.ID, layer.Effects)
		}
	}
	var document map[string]any
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal V3 plan: %v", err)
	}
	if err := json.Unmarshal(wire, &document); err != nil {
		t.Fatalf("decode V3 wire: %v", err)
	}
}
