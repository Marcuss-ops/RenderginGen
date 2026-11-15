package overlay

import "testing"

// TestImageItemEffectsDefaultToNone proves every spelling of the default is
// equivalent: an omitted stack, an empty array and the explicit "none" entry
// all lower to zero effects instead of a synthetic no-op layer.
func TestImageItemEffectsDefaultToNone(t *testing.T) {
	cases := map[string]string{
		"omitted":       `,"params":{"width":640,"height":360}`,
		"empty array":   `,"params":{"width":640,"height":360,"effects":[]}`,
		"explicit none": `,"params":{"width":640,"height":360,"effects":[{"type":"none"}]}`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
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
			if len(imageLayer.Effects) != 0 {
				t.Fatalf("image layer effects = %+v, want none", imageLayer.Effects)
			}
		})
	}
}

// TestImageItemEffectNoneIsExclusive proves "none" never silently discards a
// real effect: combining it with another entry, or with parameters, is a
// compile error rather than a last-one-wins stack.
func TestImageItemEffectNoneIsExclusive(t *testing.T) {
	cases := map[string]string{
		"mixed with glow": `,"params":{"effects":[{"type":"none"},{"type":"glow","params":{"radius":10,"intensity":0.2}}]}`,
		"carries params":  `,"params":{"effects":[{"type":"none","params":{"amount":0.5}}]}`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := CompileSemantic(imageEffectsPlan(extra)); err == nil {
				t.Fatal("invalid \"none\" declaration must fail closed")
			}
		})
	}
}
