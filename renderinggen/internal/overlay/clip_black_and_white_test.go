package overlay

import (
	"strings"
	"testing"
)

// clipBlackAndWhitePlan builds a minimal clip render plan carrying one
// black_and_white effect in the global adjustment stack.
func clipBlackAndWhitePlan(effect string) []byte {
	return []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"bw","video_id":"clip","width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":3000,"source":{"asset_id":"clip","sha256":"` + clipTestSHA + `"},"clip_effects":[` + effect + `],"items":[]}`)
}

// TestBlackAndWhiteClipEffectLowersToSaturation pins the pixel meaning of the
// opening-clip treatment: the public effect is not a new renderer primitive, it
// is Chronon's native color.saturation catalog effect with value 1 - amount.
// Rendering an archival opening clip must go through the same primitive the
// verified archive image look already uses.
func TestBlackAndWhiteClipEffectLowersToSaturation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effect string
		want   float64
	}{
		{"full desaturation", `{"type":"black_and_white","amount":1.0}`, 0},
		{"partial", `{"type":"black_and_white","amount":0.25}`, 0.75},
		{"no-op", `{"type":"black_and_white","amount":0.0}`, 1},
	} {
		result, err := CompileSemantic(clipBlackAndWhitePlan(tc.effect))
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.name, err)
		}
		var source *Layer
		for index := range result.Plan.Layers {
			if result.Plan.Layers[index].ID == "source" {
				source = &result.Plan.Layers[index]
			}
		}
		if source == nil {
			t.Fatalf("%s: source layer missing", tc.name)
		}
		if len(source.Effects) != 1 {
			t.Fatalf("%s: effects = %+v, want one lowered effect", tc.name, source.Effects)
		}
		effect := source.Effects[0]
		if effect.Type != "catalog" || effect.Params["effect_id"] != "color.saturation" {
			t.Fatalf("%s: lowered effect = %+v, want catalog/color.saturation", tc.name, effect)
		}
		params, ok := effect.Params["params"].([]map[string]any)
		if !ok || len(params) != 1 || params[0]["param"] != "value" {
			t.Fatalf("%s: catalog params = %#v", tc.name, effect.Params["params"])
		}
		value, ok := params[0]["value"].(float64)
		if !ok || value != tc.want {
			t.Fatalf("%s: saturation value = %#v, want %v", tc.name, params[0]["value"], tc.want)
		}
	}
}

// TestBlackAndWhiteClipEffectFailsClosed pins the closed clip schema: the
// required amount keeps a parameterless request from rendering an ambiguous
// treatment, and the bound keeps a negative/full-colour request out.
func TestBlackAndWhiteClipEffectFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effect string
		want   string
	}{
		{"missing amount", `{"type":"black_and_white"}`, "requires amount"},
		{"above range", `{"type":"black_and_white","amount":1.5}`, "must be in [0, 1]"},
		{"negative", `{"type":"black_and_white","amount":-0.2}`, "must be in [0, 1]"},
	} {
		if _, err := CompileSemantic(clipBlackAndWhitePlan(tc.effect)); err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %q, want %q", tc.name, err, tc.want)
		}
	}
}

// TestBlackAndWhiteLayerEffectMatchesClip proves the image surface lowers the
// same type to the same primitive, so an image overlay carrying the treatment
// replays what a clip carrying it renders.
func TestBlackAndWhiteLayerEffectMatchesClip(t *testing.T) {
	result, err := CompileSemantic(imageEffectsPlan(`,"params":{"effects":[{"type":"black_and_white","amount":0.4}]}`))
	if err != nil {
		t.Fatalf("compile image effect: %v", err)
	}
	var image *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "fx-image:image" {
			image = &result.Plan.Layers[index]
		}
	}
	if image == nil {
		t.Fatal("image layer missing")
	}
	if len(image.Effects) != 1 {
		t.Fatalf("image effects = %+v", image.Effects)
	}
	effect := image.Effects[0]
	if effect.Type != "catalog" || effect.Params["effect_id"] != "color.saturation" {
		t.Fatalf("image lowering = %+v", effect)
	}
	params, ok := effect.Params["params"].([]map[string]any)
	if !ok || len(params) != 1 {
		t.Fatalf("image catalog params = %#v", effect.Params["params"])
	}
	if value, _ := params[0]["value"].(float64); value != 0.6 {
		t.Fatalf("image saturation value = %#v, want 0.6", params[0]["value"])
	}
}
