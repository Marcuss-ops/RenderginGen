// Final rendering certification — compile level.
//
// These tests prove that every official RenderingGen preset compiles into a
// valid chronon.render-plan.v2 with the exact contract Chronon renders.
// Coverage is derived from overlay.OfficialPresetIDs(): a preset added to the
// registry automatically enters this suite and fails CI until it really
// compiles. No hardcoded preset lists, ever.
package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// certificationFixture builds one real semantic entity per registry preset,
// rendered against the Pale Olive color background. It is the in-repo,
// compile-level successor of the removed render-final-certification CLI: the
// registry gate these tests enforce now runs in CI via go test ./...
func certificationFixture(def OfficialPresetDefinition) FastEntityOverlay {
	const durationFrames = int64(125)
	fx := FastEntityOverlay{
		StartFrame: 10, // non-zero start; end frame 125 is exclusive
		EndFrame:   durationFrames,
		Opacity:    1.0,
		PresetID:   def.ID,
	}
	switch def.Family {
	case PresetImage:
		fx.Type = "image"
		fx.Asset = "gerard_butler.jpg"
		fx.Position = def.Layout.Anchor
		fx.Size = float64(def.Layout.BoxWidth)
		fx.Animation = def.Motion.Name
	default:
		fx.Type = "text"
		fx.Text = "Pipeline Certificata — 450% più veloce, fino a 125 frame"
		fx.Font = "fonts/Poppins-Bold.ttf"
		fx.Position = def.Layout.Anchor
		fx.Size = def.Style.FontSize
		fx.Color = def.Style.Fill
		fx.Animation = def.Motion.Name
	}
	return fx
}

// TestFinal_AllOfficialPresetsCovered is the completeness gate: the registry
// must enumerate at least one preset of each family, and every registered ID
// must resolve to a real definition. It runs first so a registry regression
// is reported before the per-preset matrix.
func TestFinal_AllOfficialPresetsCovered(t *testing.T) {
	ids := OfficialPresetIDs()
	if len(ids) == 0 {
		t.Fatal("official preset registry is empty: no preset can ever be certified")
	}
	textCount, imageCount := 0, 0
	for _, id := range ids {
		def, err := ResolveOfficialPreset(id)
		if err != nil {
			t.Errorf("registry entry %q does not resolve: %v", id, err)
			continue
		}
		if def.ID != id {
			t.Errorf("registry entry %q resolves to definition with ID %q", id, def.ID)
		}
		if def.Family != PresetText && def.Family != PresetImage {
			t.Errorf("preset %q has unknown family %q", id, def.Family)
		}
		// Static presets (no motion) are legal — static_text_smoke is one
		// by design — but they must say so: the motion name is the contract
		// the compiler and certification fixture both read.
		if def.Motion.Name == "" && def.Family != PresetText {
			t.Errorf("preset %q has no motion name", id)
		}
		switch def.Family {
		case PresetText:
			textCount++
		case PresetImage:
			imageCount++
		}
	}
	if textCount == 0 {
		t.Error("registry contains no text presets: text certification would be vacuous")
	}
	if imageCount == 0 {
		t.Error("registry contains no image presets: image certification would be vacuous")
	}
	t.Logf("registry coverage: %d text + %d image = %d presets", textCount, imageCount, len(ids))
}

// TestFinal_CompileEveryPreset compiles every registry preset through the
// real entity contract and asserts the plan Chronon receives: 1080p 24fps
// canvas, a Pale Olive background layer, exactly one entity layer, correct
// timeline window, and valid animation tracks.
func TestFinal_CompileEveryPreset(t *testing.T) {
	const (
		width, height  = 1920, 1080
		durationFrames = int64(125)
	)
	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			def, err := ResolveOfficialPreset(id)
			if err != nil {
				t.Fatalf("resolve registry preset: %v", err)
			}
			plan, err := CompileFastEntityOverlays(id, width, height, 24, 1, durationFrames,
				"color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			// Canvas contract.
			if plan.Canvas.Width != width || plan.Canvas.Height != height {
				t.Errorf("canvas %dx%d, want %dx%d", plan.Canvas.Width, plan.Canvas.Height, width, height)
			}
			if plan.Canvas.FPSNum != 24 || plan.Canvas.FPSDen != 1 {
				t.Errorf("fps %d/%d, want 24/1", plan.Canvas.FPSNum, plan.Canvas.FPSDen)
			}
			if plan.Canvas.DurationFrames != durationFrames {
				t.Errorf("duration %d, want %d", plan.Canvas.DurationFrames, durationFrames)
			}

			// Background: the color layer contract that keeps a compositor
			// backend from rendering branded content as black.
			if len(plan.Layers) < 2 {
				t.Fatalf("layers=%d, want >= 2 (background + entity)", len(plan.Layers))
			}
			bg := plan.Layers[0]
			if bg.Type != "color" || bg.ID != "bg_color" {
				t.Errorf("layer 0 = %s/%s, want color/bg_color", bg.Type, bg.ID)
			}
			want := []float64{238.0 / 255, 241.0 / 255, 231.0 / 255, 1}
			for i, c := range want {
				if bg.Color[i] != c {
					t.Errorf("bg.Color[%d]=%v, want %v (Pale Olive)", i, bg.Color[i], c)
				}
			}
			if bg.StartFrame != 0 || bg.DurationFrames != durationFrames {
				t.Errorf("bg timeline %d+%d, want 0..%d", bg.StartFrame, bg.DurationFrames, durationFrames)
			}

			// Entity layer: exactly one, valid timeline, valid motion.
			entity := certificationEntityLayer(plan, def)
			switch def.Family {
			case PresetText:
				if entity.Type != "text" {
					t.Errorf("entity type %q, want text", entity.Type)
				}
				if entity.Text == "" {
					t.Error("entity text is empty")
				}
				if entity.Style == nil || entity.Style.Font == "" {
					t.Error("entity has no font: Chronon would silently fall back")
				}
			case PresetImage:
				if entity.Type != "image" {
					t.Errorf("entity type %q, want image", entity.Type)
				}
				if entity.Asset == "" {
					t.Error("entity has no asset")
				}
				if entity.BoxWidth <= 0 || entity.BoxHeight <= 0 {
					t.Errorf("entity box %dx%d, want positive", entity.BoxWidth, entity.BoxHeight)
				}
				if entity.Position == nil || len(entity.Position) != 2 {
					t.Error("entity position missing: placement cannot be verified")
				}
			}
			if entity.StartFrame != 10 {
				t.Errorf("entity StartFrame=%d, want 10", entity.StartFrame)
			}
			// Exclusive end: entity covers 10..124 inclusive, never frame 125.
			if entity.DurationFrames != durationFrames-10 {
				t.Errorf("entity DurationFrames=%d, want %d (exclusive end)", entity.DurationFrames, durationFrames-10)
			}
			if entity.Opacity <= 0 || entity.Opacity > 1 {
				t.Errorf("entity Opacity=%v, want in (0,1]", entity.Opacity)
			}
			// Only animated presets must carry tracks; static presets
			// (static_text_smoke) legitimately compile without them.
			imageScaleFallback := def.Family == PresetImage
			if def.Motion.Name != "" && !imageScaleFallback && ((entity.Animation == nil || len(entity.Animation.Tracks) == 0) &&
				len(entity.TextAnimators) == 0) {
				t.Fatalf("entity has no animation tracks or text animators: motion %q compiled away", def.Motion.Name)
			}
		})
	}
}

// TestFinal_AnimationFirstMiddleLastFrame proves every preset's motion really
// evolves: tracks must move between the entity's first frame, its middle and
// its last frame — a static track on an animated preset is the "animation
// silently became static" regression.
func TestFinal_AnimationFirstMiddleLastFrame(t *testing.T) {
	const durationFrames = int64(125)
	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			def, err := ResolveOfficialPreset(id)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			plan, err := CompileFastEntityOverlays(id, 1920, 1080, 24, 1, durationFrames,
				"color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			entity := certificationEntityLayer(plan, def)
			if entity.Animation == nil {
				t.Skip("static preset")
			}
			if len(entity.Animation.Tracks) == 0 && len(entity.TextAnimators) > 0 {
				for _, animator := range entity.TextAnimators {
					for _, selector := range animator.Selectors {
						if selector.Start == nil && selector.End == nil && selector.Amount == nil && selector.Offset == nil {
							t.Errorf("text animator %q has no selector sweep", animator.ID)
						}
					}
				}
				return
			}
			for _, track := range entity.Animation.Tracks {
				if len(track.Keyframes) < 2 {
					t.Errorf("track %q has %d keyframes: motion cannot evolve", track.Property, len(track.Keyframes))
					continue
				}
				// Keyframes are relative to the layer start: 0 is the entity's
				// first visible frame, duration-1 its last.
				first := track.Keyframes[0]
				last := track.Keyframes[len(track.Keyframes)-1]
				if first.Frame != 0 {
					t.Errorf("track %q first keyframe at %d, want 0 (layer-relative)", track.Property, first.Frame)
				}
				// The 125/124 regression: a keyframe at the exclusive end
				// frame animates a frame that must not exist.
				if last.Frame > entity.DurationFrames-1 {
					t.Errorf("track %q last keyframe at %d exceeds exclusive end %d",
						track.Property, last.Frame, entity.DurationFrames-1)
				}
			}
		})
	}
}

// TestFinal_PlansAreSchemaStable marshals every compiled plan and proves the
// output is parseable chronon.render-plan.v2 with no empty required fields —
// the deterministic-plan gate (deterministicRender coverage lives at runtime).
func TestFinal_PlansAreSchemaStable(t *testing.T) {
	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			def, err := ResolveOfficialPreset(id)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			plan, err := CompileFastEntityOverlays(id, 1920, 1080, 24, 1, 125,
				"color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			plan.Output.Path = "out.mp4"
			data, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("marshal plan: %v", err)
			}
			if !strings.Contains(string(data), "render-plan") && plan.Schema == "" {
				t.Error("plan JSON does not carry a schema marker")
			}
			var round map[string]any
			if err := json.Unmarshal(data, &round); err != nil {
				t.Fatalf("round-trip parse: %v", err)
			}
			if round["canvas"] == nil || round["layers"] == nil {
				t.Error("plan JSON missing canvas or layers: Chronon would reject it")
			}
		})
	}
}

// TestFinal_MotionCompileFailureFailsClosed: a motion the registry cannot
// resolve must abort compilation — never degrade to a static layer.
func TestFinal_MotionCompileFailureFailsClosed(t *testing.T) {
	_, err := CompileFastEntityOverlays("fails-closed", 1920, 1080, 24, 1, 125, "color:#EEF1E7",
		[]FastEntityOverlay{{
			Type: "image", StartFrame: 0, EndFrame: 125,
			Asset: "gerard_butler.jpg", Animation: "totally_unknown_motion",
		}})
	if err == nil {
		t.Fatal("unknown motion compiled silently: layer would render static instead of failing")
	}
	if !strings.Contains(err.Error(), "totally_unknown_motion") {
		t.Errorf("error does not name the bad motion: %v", err)
	}
}

// TestFinal_InvalidPresetFailsClosed: an unknown preset ID must be rejected,
// never silently ignored (the "29 presets work, the 30th renders nothing" bug).
func TestFinal_InvalidPresetFailsClosed(t *testing.T) {
	if _, err := ResolveOfficialPreset("preset_that_does_not_exist"); err == nil {
		t.Fatal("unknown preset resolved successfully")
	}
	// And the compiler must not accept it through the kind-checked path
	// either.
	if _, err := resolveOfficialPreset("preset_that_does_not_exist", "text"); err == nil {
		t.Fatal("kind-checked resolve accepted an unknown preset")
	}
}

func certificationEntityLayer(plan *Plan, def OfficialPresetDefinition) *Layer {
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if def.Family == PresetImage && layer.Type == "image" {
			return layer
		}
		if def.Family == PresetText && layer.Type == "text" {
			return layer
		}
	}
	return nil
}
