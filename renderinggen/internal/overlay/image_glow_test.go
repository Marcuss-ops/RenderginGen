package overlay

import (
	"math"
	"strings"
	"testing"
)

func TestImageGlowLowersToNativeGlowEffect(t *testing.T) {
	extra := `,"params":{"width":640,"height":360,"image_glow_radius":24,"image_glow_intensity":0.35,"image_glow_color":"#FF3355"}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile image glow: %v", err)
	}
	var imageLayer *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "fx-image:image" {
			imageLayer = &result.Plan.Layers[index]
		}
	}
	if imageLayer == nil {
		t.Fatalf("base image layer missing: %+v", result.Plan.Layers)
	}
	if len(imageLayer.Effects) != 1 || imageLayer.Effects[0].Type != "glow" {
		t.Fatalf("image glow effects = %+v, want one native glow", imageLayer.Effects)
	}
	glow := imageLayer.Effects[0].Params
	if radius, _ := toFloat(glow["radius"]); radius != 24 {
		t.Fatalf("glow radius = %v, want 24", glow["radius"])
	}
	if intensity, _ := toFloat(glow["intensity"]); intensity != 0.35 {
		t.Fatalf("glow intensity = %v, want 0.35", glow["intensity"])
	}
	color, ok := glow["color"].([]float64)
	if !ok || len(color) != 4 {
		t.Fatalf("glow color = %#v, want RGBA", glow["color"])
	}
	if math.Abs(color[0]-1) > 1e-6 || math.Abs(color[1]-0.2) > 1e-6 || math.Abs(color[2]-0.3333333) > 1e-6 || color[3] != 1 {
		t.Fatalf("glow color = %v, want [1,0.2,0.333333,1]", color)
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if !strings.Contains(string(wire), `"type":"glow"`) {
		t.Fatalf("image glow must serialize as a native glow effect: %s", wire)
	}
}

func TestImageGlowZeroDisables(t *testing.T) {
	extra := `,"params":{"width":640,"height":360,"image_glow_radius":0}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile disabled image glow: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.ID == "fx-image:image" {
			if len(layer.Effects) != 0 {
				t.Fatalf("radius 0 must emit no glow effect: %+v", layer.Effects)
			}
			return
		}
	}
	t.Fatal("base image layer missing")
}

func TestImageGlowValidation(t *testing.T) {
	for _, tc := range []struct {
		name, params, want string
	}{
		{"radius", `"image_glow_radius":257`, "image_glow_radius"},
		{"intensity", `"image_glow_radius":24,"image_glow_intensity":5`, "image_glow_intensity"},
		{"color", `"image_glow_radius":24,"image_glow_color":"red"`, "image_glow_color"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := `,"params":{"width":640,"height":360,` + tc.params + `}`
			_, err := CompileSemantic(imageEffectsPlan(extra))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
