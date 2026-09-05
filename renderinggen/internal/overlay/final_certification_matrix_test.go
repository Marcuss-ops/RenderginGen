package overlay

import "testing"

func TestFinal_ImageAndTextTogether(t *testing.T) {
	plan, err := CompileFastEntityOverlays("image-plus-phrase", 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{
		certificationFixture(mustPreset(t, "image_scale_in")), certificationFixture(mustPreset(t, "phrase_fade_in")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 3 {
		t.Fatalf("layers=%d, want background + image + text", len(plan.Layers))
	}
}

func TestFinal_AllImageAndTextPresetsCompile(t *testing.T) {
	for _, def := range OfficialPresets.All() {
		t.Run(def.ID, func(t *testing.T) {
			if _, err := CompileFastEntityOverlays(def.ID, 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFinal_EntityOpacity(t *testing.T) {
	for _, want := range []float64{0, .25, .5, 1} {
		want := want
		t.Run(string(rune('0'+int(want*4))), func(t *testing.T) {
			plan, err := CompileFastEntityOverlays("opacity", 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{{Type: "text", Text: "opacity", Font: "fonts/Poppins-Bold.ttf", StartFrame: 0, EndFrame: 125, Opacity: want, OpacityExplicit: &want}})
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.Layers[1].Opacity; got != want {
				t.Errorf("opacity %.2f compiled as %.2f", want, got)
			}
		})
	}
}

func TestFinal_AssetMatrix(t *testing.T) {
	fixtures := []FastEntityOverlay{
		{Type: "image", Asset: "gerard_butler.jpg", Position: "image_right", Size: 260, StartFrame: 0, EndFrame: 125, Animation: "static"},
		{Type: "image", Asset: "logo_pulse.png", Position: "center", Size: 260, StartFrame: 0, EndFrame: 125, Animation: "static"},
		{Type: "text", Text: "Nome breve", Font: "fonts/Poppins-Bold.ttf", Position: "lower_third", Size: 58, StartFrame: 0, EndFrame: 125, Animation: "static"},
		{Type: "text", Text: "Frase lunga — àéìòù ✓", Font: "fonts/Poppins-Bold.ttf", Position: "safe_area", Size: 58, StartFrame: 0, EndFrame: 125, Animation: "static"},
	}
	plan, err := CompileFastEntityOverlays("asset-matrix", 1920, 1080, 24, 1, 125, "background.mp4", fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != len(fixtures)+1 {
		t.Fatalf("layers=%d, want %d", len(plan.Layers), len(fixtures)+1)
	}
}

func mustPreset(t *testing.T, id string) OfficialPresetDefinition {
	t.Helper()
	d, err := ResolveOfficialPreset(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
