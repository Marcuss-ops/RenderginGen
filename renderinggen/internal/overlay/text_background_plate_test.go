package overlay

import (
	"math"
	"testing"
)

func TestClassicPhrasePresetKeepsRuntimeCardBackground(t *testing.T) {
	preset, err := resolveOfficialPreset(ClassicPhrasePresetID, string(PresetText))
	if err != nil {
		t.Fatalf("resolve classic preset: %v", err)
	}
	spec := resolvedTextSpec{
		Text:         "CARD",
		CanvasWidth:  1280,
		CanvasHeight: 720,
		BoxWidth:     400,
		BoxHeight:    100,
		Size:         []float64{400, 100},
		Position:     []float64{640, 360},
		Style:        &LayerStyle{Font: officialFontPath},
		PresetID:     ClassicPhrasePresetID,
		PresetDef:    preset,
		StylePolicy:  textRolePhrase,
		StyleParams: map[string]any{
			"style": map[string]any{
				"background": map[string]any{"color": "#112233"},
			},
		},
	}
	layer, err := compileResolvedText("classic", 0, 24, spec)
	if err != nil {
		t.Fatalf("compile classic phrase: %v", err)
	}
	if layer.Style == nil || layer.Style.Background == nil || !layer.Style.Background.LiftToShape {
		t.Fatalf("classic phrase dropped the runtime card_background: %+v", layer.Style)
	}

	plan := &Plan{Canvas: Canvas{Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1}, Layers: []Layer{layer}}
	liftTextBackgroundPlates(plan)
	if len(plan.Layers) != 2 {
		t.Fatalf("got %d layers, want plate + text", len(plan.Layers))
	}
	plate := plan.Layers[0]
	if plate.Type != "shape" || plate.Shape == nil || plate.Shape.Type != "rect" {
		t.Fatalf("runtime classic plate = %+v, want a native rect shape", plate)
	}
	if plan.Layers[1].Style.Background != nil {
		t.Fatalf("text layer still carries the lowered background: %+v", plan.Layers[1].Style.Background)
	}
}

func TestTextPlateMultipliesStaticLayerOpacity(t *testing.T) {
	backgroundOpacity := 0.8
	layerOpacity := 0.5
	layer := Layer{
		ID:             "caption",
		Type:           "text",
		ScreenSpace:    true,
		Size:           []float64{200, 100},
		Position:       []float64{500, 300},
		Opacity:        &layerOpacity,
		StartFrame:     2,
		DurationFrames: 10,
		Style: &LayerStyle{Background: &LayerBackground{
			Color:       "#112233",
			Opacity:     &backgroundOpacity,
			LiftToShape: true,
		}},
	}
	plan := &Plan{Canvas: Canvas{Width: 1280, Height: 720}, Layers: []Layer{layer}}
	liftTextBackgroundPlates(plan)
	if len(plan.Layers) != 2 {
		t.Fatalf("got %d layers, want plate + text", len(plan.Layers))
	}
	plate := plan.Layers[0]
	if plate.Opacity == nil || math.Abs(*plate.Opacity-0.4) > 1e-9 {
		t.Fatalf("plate opacity = %v, want background 0.8 * text 0.5", plate.Opacity)
	}
	if plate.StartFrame != layer.StartFrame || plate.DurationFrames != layer.DurationFrames {
		t.Fatalf("plate lifetime = [%d,%d), text = [%d,%d)", plate.StartFrame, plate.DurationFrames, layer.StartFrame, layer.DurationFrames)
	}
}
