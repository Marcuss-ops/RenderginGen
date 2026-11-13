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

func TestStandaloneImageBackdropUsesSameImageAndCapsZoom(t *testing.T) {
	extra := `,"params":{"image_backdrop":{"zoom":1.25,"blur_px":20}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile standalone image backdrop: %v", err)
	}
	if len(result.Plan.Layers) != 2 {
		t.Fatalf("layers = %+v; want backdrop then foreground image", result.Plan.Layers)
	}
	backdrop, foreground := result.Plan.Layers[0], result.Plan.Layers[1]
	if backdrop.ID != "fx-image:image-backdrop" || backdrop.Asset != foreground.Asset {
		t.Fatalf("backdrop=%+v foreground=%+v; backdrop must reuse the source image", backdrop, foreground)
	}
	if backdrop.Size[0] != 1280 || backdrop.Size[1] != 720 || backdrop.Fit != FitCover || backdrop.Scale[0] != 1.25 || backdrop.Scale[1] != 1.25 {
		t.Fatalf("backdrop geometry = %+v", backdrop)
	}
	if len(backdrop.Effects) != 1 || backdrop.Effects[0].Type != "gaussian_blur" || backdrop.Effects[0].Params["radius"] != float64(20) {
		t.Fatalf("backdrop effects = %+v", backdrop.Effects)
	}
	if len(foreground.Effects) != 0 {
		t.Fatalf("backdrop processing must not blur the foreground: %+v", foreground.Effects)
	}
	if _, err := CompileSemantic(imageEffectsPlan(`,"params":{"image_backdrop":{"zoom":10.01}}`)); err == nil {
		t.Fatal("backdrop zoom above 10x must fail closed")
	}
	composite := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-backdrop-composite","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"duo","kind":"image","template_id":"image_popup","start_ms":0,"end_ms":5000,"asset_refs":[%s,%s],"image_layers":[{"id":"left","asset_id":"effect-photo","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"},{"id":"right","asset_id":"effect-photo-2","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"}],"params":{"image_backdrop":{}}}]}`,
		imageEffectsAssetRef, imageEffectsSecondAssetRef))
	if _, err := CompileSemantic(composite); err == nil || !strings.Contains(err.Error(), "standalone image") {
		t.Fatalf("composite image backdrop error = %v, want standalone-only validation", err)
	}
}

// TestStandaloneImageBackdropGridLowersLightCanvasGrid proves the payload can
// pair the blurred same-image backdrop with a light canvas grid and that colour,
// spacing, line width and opacity survive lowering onto the screen-space shape.
func TestStandaloneImageBackdropGridLowersLightCanvasGrid(t *testing.T) {
	extra := `,"params":{"image_backdrop":{"zoom":1.2,"blur_px":24,"grid":{"style":"lines","color":"#7FA8C8","spacing_px":64,"line_width_px":1.5,"opacity":0.12}}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile image backdrop grid: %v", err)
	}
	if len(result.Plan.Layers) != 3 {
		t.Fatalf("layers = %+v; want backdrop, foreground and grid", result.Plan.Layers)
	}
	grid := result.Plan.Layers[2]
	if grid.ID != "fx-image:image-grid" || grid.Type != "shape" || grid.Shape == nil || grid.Shape.Type != "grid" {
		t.Fatalf("grid layer = %+v", grid)
	}
	if !grid.ScreenSpace {
		t.Fatalf("backdrop grid must be a screen-space shape: %+v", grid)
	}
	if grid.Shape.GridSpacing != 64 || grid.Shape.Stroke == nil || grid.Shape.Stroke.Color != "#7FA8C8" || grid.Shape.Stroke.Width != 1.5 {
		t.Fatalf("grid geometry = %+v", grid.Shape)
	}
	if grid.Opacity == nil || *grid.Opacity != 0.12 {
		t.Fatalf("grid opacity = %v, want 0.12", grid.Opacity)
	}
	if grid.StartFrame != 0 || grid.DurationFrames == 0 {
		t.Fatalf("grid timing = [%d,%d)", grid.StartFrame, grid.DurationFrames)
	}
}

// TestStandaloneImageBackdropGridSupportsDotsAndDisabling pins the dot style
// and the explicit off switch: a disabled grid lowers no extra layer.
func TestStandaloneImageBackdropGridSupportsDotsAndDisabling(t *testing.T) {
	extra := `,"params":{"image_backdrop":{"grid":{"style":"dots","color":"#FFFFFF","spacing_px":48,"dot_radius_px":3,"opacity":0.2}}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile dot backdrop grid: %v", err)
	}
	if len(result.Plan.Layers) != 3 {
		t.Fatalf("layers = %+v; want backdrop, foreground and dot grid", result.Plan.Layers)
	}
	grid := result.Plan.Layers[2]
	if grid.Shape == nil || grid.Shape.Type != "dot_grid" || grid.Shape.GridSpacing != 48 || grid.Shape.DotRadius != 3 {
		t.Fatalf("dot grid geometry = %+v", grid.Shape)
	}
	if grid.Opacity == nil || *grid.Opacity != 0.2 {
		t.Fatalf("dot grid opacity = %v, want 0.2", grid.Opacity)
	}

	disabled, err := CompileSemantic(imageEffectsPlan(`,"params":{"image_backdrop":{"grid":{"enabled":false}}}`))
	if err != nil {
		t.Fatalf("compile disabled backdrop grid: %v", err)
	}
	if len(disabled.Plan.Layers) != 2 {
		t.Fatalf("disabled grid lowered %d layers, want backdrop and foreground only: %+v", len(disabled.Plan.Layers), disabled.Plan.Layers)
	}
}

// TestStandaloneImageBackdropGridFailsClosed proves malformed grid controls are
// compile errors, never a silently missing visual treatment.
func TestStandaloneImageBackdropGridFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grid  string
		error string
	}{
		{"not an object", `"on"`, "must be an object"},
		{"unknown property", `{"density":3}`, "unsupported property"},
		{"bad style", `{"style":"crosses"}`, "style"},
		{"bad color", `{"color":"red"}`, "color"},
		{"spacing too small", `{"spacing_px":2}`, "spacing_px"},
		{"spacing too large", `{"spacing_px":513}`, "spacing_px"},
		{"opacity out of range", `{"opacity":1.5}`, "opacity"},
		{"line width on dots", `{"style":"dots","line_width_px":1}`, "line_width_px"},
		{"dot radius on lines", `{"style":"lines","dot_radius_px":2}`, "dot_radius_px"},
	} {
		plan := imageEffectsPlan(`,"params":{"image_backdrop":{"grid":` + tc.grid + `}}`)
		if _, err := CompileSemantic(plan); err == nil || !strings.Contains(err.Error(), tc.error) {
			t.Errorf("%s: error = %v, want mention of %q", tc.name, err, tc.error)
		}
	}
}

func TestImageFramePayloadControlsShadowAndBorder(t *testing.T) {
	extra := `,"frame":{"border":{"width_px":5,"color":"#38BDF8","radius_px":24},"shadow":{"color":"#101820","opacity":0.6,"blur_px":12,"offset_x_px":2,"offset_y_px":5}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile image frame payload: %v", err)
	}
	image := result.Plan.Layers[0]
	if image.Style == nil || image.Style.Background == nil || image.Style.Shadow == nil {
		t.Fatalf("image frame treatment did not lower to border and shadow: %+v", image)
	}
	if image.Style.Background.Color != "#38BDF8" || image.Style.Background.Padding[0] != 5 || image.Style.Shadow.Color != "#101820" || image.Style.Shadow.Blur != 12 || image.Style.Shadow.Offset[1] != 5 {
		t.Fatalf("lowered frame = %+v", image.Style)
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
