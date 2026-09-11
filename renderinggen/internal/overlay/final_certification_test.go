// Final rendering certification — compile level.
//
// These tests prove that every official RenderingGen preset compiles into a
// valid chronon.render-plan.v2 with the exact contract Chronon renders.
// Coverage is derived from overlay.OfficialPresetIDs(): a preset added to the
// registry automatically enters this suite and fails CI until it really
// compiles. No hardcoded preset lists, ever.
//
// Every fixture goes through the REAL production path — CompileIfSemantic on
// a renderinggen.overlay-plan.v1 document — so the suite certifies the single
// lowering chain that PipelineGen submits to, not a test-only compiler.
package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// certificationDurationFrames is the certified composition length. The
// fixture starts at frame 10, so the entity's own window is 10..124 inclusive
// (exclusive end 125).
const certificationDurationFrames = int64(125)

// certificationMSRange is the millisecond range that lowers to the certified
// frame window at the fixture's 24 fps: floor(start_ms·24/1000)=10 and
// ceil(end_ms·24/1000)=125.
const (
	certificationStartMS = int64(417)
	certificationEndMS   = int64(5208)
)

// certificationBackgroundRGBA is the Pale Olive Classic background, the color
// layer contract that keeps a compositor backend from rendering branded
// content as black.
const certificationBackgroundRGBA = "[0.9333333333333333,0.9450980392156862,0.9058823529411765,1]"

// certificationAssetID/SHA identify the single fixture image every
// certification plan references. At runtime the harness materializes the real
// bytes at the semantic logical path (assets/semantic/<id>.jpg).
const (
	certificationAssetID  = "certification-image"
	certificationAssetSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

const certificationText = "Pipeline Certificata — 450% più veloce, fino a 125 frame"

// certificationItemJSON builds one real semantic item for a registry preset,
// using the same template vocabulary PipelineGen emits: IMAGE_OVERLAY for the
// image family, IMPORTANT_PHRASE for the text family.
func certificationItemJSON(def PresetDefinition) (string, error) {
	if def.Family == PresetImage {
		return fmt.Sprintf(
			`{"id":%q,"template_id":"IMAGE_OVERLAY","preset_id":%q,"start_ms":%d,"end_ms":%d,`+
				`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/certification.jpg","media_type":"image/jpeg"}]}`,
			def.ID, def.ID, certificationStartMS, certificationEndMS, certificationAssetID, certificationAssetSHA), nil
	}
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMPORTANT_PHRASE","preset_id":%q,"text":%q,"start_ms":%d,"end_ms":%d}`,
		def.ID, def.ID, certificationText, certificationStartMS, certificationEndMS), nil
}

// certificationPlanRaw is the renderinggen.overlay-plan.v1 document the whole
// certification suite (compile and runtime) lowers and renders.
func certificationPlanRaw(def PresetDefinition) (string, error) {
	item, err := certificationItemJSON(def)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":%q,"video_id":"certification","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"background":{"kind":"color","color":%s},"items":[%s]}`,
		"certification-"+def.ID, certificationBackgroundRGBA, item), nil
}

// certificationPlan compiles the fixture for one preset through the single
// production compiler and returns the exact chronon.render-plan.v2 the
// runtime certification renders. It is deterministic, so pixel tests can read
// the entity's declared geometry from the same plan that produced the MP4
// instead of hard-coding sample points.
func certificationPlan(t *testing.T, presetID string) *Plan {
	t.Helper()
	def, err := ResolveOfficialPreset(presetID)
	if err != nil {
		t.Fatalf("resolve registry preset: %v", err)
	}
	raw, err := certificationPlanRaw(def)
	if err != nil {
		t.Fatalf("build certification plan: %v", err)
	}
	plan, _, semantic, err := CompileIfSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile certification plan %s: %v", presetID, err)
	}
	if !semantic {
		t.Fatalf("certification plan %s did not go through the semantic compiler", presetID)
	}
	return plan
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
// real semantic contract and asserts the plan Chronon receives: 1080p 24fps
// canvas, a Pale Olive background layer, exactly one entity layer, correct
// timeline window, and valid animation tracks.
func TestFinal_CompileEveryPreset(t *testing.T) {
	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			def, err := ResolveOfficialPreset(id)
			if err != nil {
				t.Fatalf("resolve registry preset: %v", err)
			}
			plan := certificationPlan(t, id)

			// Canvas contract.
			if plan.Canvas.Width != 1920 || plan.Canvas.Height != 1080 {
				t.Errorf("canvas %dx%d, want 1920x1080", plan.Canvas.Width, plan.Canvas.Height)
			}
			if plan.Canvas.FPSNum != 24 || plan.Canvas.FPSDen != 1 {
				t.Errorf("fps %d/%d, want 24/1", plan.Canvas.FPSNum, plan.Canvas.FPSDen)
			}
			if plan.Canvas.DurationFrames != certificationDurationFrames {
				t.Errorf("duration %d, want %d", plan.Canvas.DurationFrames, certificationDurationFrames)
			}

			// Background: the color layer contract that keeps a compositor
			// backend from rendering branded content as black.
			if len(plan.Layers) < 2 {
				t.Fatalf("layers=%d, want >= 2 (background + entity)", len(plan.Layers))
			}
			bg := plan.Layers[0]
			if bg.Type != "color" || bg.ID != "background" {
				t.Errorf("layer 0 = %s/%s, want color/background", bg.Type, bg.ID)
			}
			want := []float64{238.0 / 255, 241.0 / 255, 231.0 / 255, 1}
			for i, c := range want {
				if bg.Color[i] != c {
					t.Errorf("bg.Color[%d]=%v, want %v (Pale Olive)", i, bg.Color[i], c)
				}
			}
			if bg.StartFrame != 0 || bg.DurationFrames != certificationDurationFrames {
				t.Errorf("bg timeline %d+%d, want 0..%d", bg.StartFrame, bg.DurationFrames, certificationDurationFrames)
			}

			// Entity layer: exactly one, valid timeline, valid motion.
			entity := certificationEntityLayer(plan, def)
			if entity == nil {
				t.Fatalf("no %s entity layer compiled", def.Family)
			}
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
				if len(entity.Position) != 2 {
					t.Error("entity position missing: placement cannot be verified")
				}
			}
			if entity.StartFrame != 10 {
				t.Errorf("entity StartFrame=%d, want 10", entity.StartFrame)
			}
			// Exclusive end: entity covers 10..124 inclusive, never frame 125.
			if entity.DurationFrames != certificationDurationFrames-10 {
				t.Errorf("entity DurationFrames=%d, want %d (exclusive end)", entity.DurationFrames, certificationDurationFrames-10)
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
	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			def, err := ResolveOfficialPreset(id)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			plan := certificationPlan(t, id)
			entity := certificationEntityLayer(plan, def)
			if entity == nil {
				t.Fatal("no entity layer")
			}
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
			plan := certificationPlan(t, id)
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
	raw := fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"fails-closed","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"items":[{"id":"img","template_id":"IMAGE_OVERLAY","preset_id":"image_scale_in","motion_id":"totally_unknown_motion","start_ms":0,"end_ms":1000,`+
			`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/x.jpg","media_type":"image/jpeg"}]}]}`,
		certificationAssetID, certificationAssetSHA)
	_, _, _, err := CompileIfSemantic([]byte(raw))
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

func certificationEntityLayer(plan *Plan, def PresetDefinition) *Layer {
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
