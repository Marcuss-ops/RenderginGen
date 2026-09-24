package overlay

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestOverlayV3CatalogLowersEveryModernMotion exercises the same lowering used
// by semantic_compile. It keeps the catalog gate meaningful: an id may exist
// in JSON and still be unusable if it cannot produce a concrete layer plan.
func TestOverlayV3CatalogLowersEveryModernMotion(t *testing.T) {
	for _, id := range motion.Registry.AppleV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "A MODERN OVERLAY", 120, 6)
		if err != nil {
			t.Fatalf("text motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) == 0 {
			t.Fatalf("text motion %s lowered incompletely: %+v", id, animation)
		}
	}
	for _, id := range motion.Registry.ImageV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "", 120, 6)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 {
			t.Fatalf("image motion %s lowered without layer tracks: %+v", id, animation)
		}
	}
}

func TestImage25DCleanV1CatalogLowersLayerOnlyWithCatalogExit(t *testing.T) {
	ids := motion.Registry.Image25DCleanV1MotionIDs()
	if len(ids) != 8 {
		t.Fatalf("clean 2.5D image catalog has %d motions, want 8", len(ids))
	}
	for _, id := range ids {
		animation, err := animationForMotion(id, nil, "", 120, 0)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) != 0 {
			t.Fatalf("image motion %s did not lower to layer tracks only: %+v", id, animation)
		}
		if !layerUses3D(animation) {
			t.Fatalf("image motion %s did not activate 3D", id)
		}
		for _, track := range animation.Tracks {
			if track.Property == "opacity" {
				last := track.Keyframes[len(track.Keyframes)-1]
				if last.Frame != 119 || last.Value != 0.0 {
					t.Fatalf("image motion %s exit = frame %d value %v, want frame 119 opacity 0", id, last.Frame, last.Value)
				}
			}
		}
	}
}

func TestImageMotionInventoryHasAllCatalogImageAnimations(t *testing.T) {
	inventory := ImageMotionInventory()
	want := map[string][]string{
		"overlay_v3_image": {
			"image_fade_reveal", "image_focus_reveal", "image_scale_reveal",
			"image_slide_left_reveal", "image_slide_right_reveal", "image_parallax_depth_reveal",
			"image_tilt_settle", "image_card_push", "image_diagonal_sweep", "image_soft_focus_reveal",
		},
		"image_25d_clean_v1": {
			"image_25d_depth_float_in", "image_25d_yaw_flip_in", "image_25d_pitch_lift",
			"image_25d_pop_z_bounce", "image_25d_swipe_3d", "image_25d_card_swing",
			"image_25d_blur_focus_in", "image_25d_blur_scale_in",
		},
	}
	if len(inventory) != len(want) {
		t.Fatalf("image inventory groups = %v, want exactly %v", inventory, want)
	}
	all := make(map[string]bool)
	for family, expected := range want {
		got := inventory[family]
		if len(got) != len(expected) {
			t.Fatalf("%s inventory = %v, want %d IDs", family, got, len(expected))
		}
		seen := make(map[string]bool, len(got))
		for _, id := range got {
			if seen[id] {
				t.Errorf("%s inventory repeats %q", family, id)
			}
			seen[id], all[id] = true, true
		}
		for _, id := range expected {
			if !seen[id] {
				t.Errorf("%s inventory is missing catalog motion %q", family, id)
			}
		}
	}
	if len(all) != 18 || len(motion.Registry.ImageOverlayMotionIDs()) != len(all) {
		t.Fatalf("combined image motion inventory = %d, want 18 unique IDs", len(all))
	}
}

// TestEveryImageMotionReachesTheChrononRenderPlan compiles each registered
// image motion through the public semantic compiler, then checks the serialized
// Chronon plan. This covers the full producer motion_id -> registry -> layer
// tracks -> wire path, including the derived enable_3d routing flag.
func TestEveryImageMotionReachesTheChrononRenderPlan(t *testing.T) {
	ids := motion.Registry.ImageOverlayMotionIDs()
	if len(ids) != 18 {
		t.Fatalf("registered image motions = %d, want 18: %v", len(ids), ids)
	}

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-motion-%[1]s","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"image-%[1]s","kind":"image","template_id":"PRODUCT","motion_id":%[2]q,"start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"test-image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/test-image.png","media_type":"image/png"}]}]}`, id, id))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile semantic plan for image motion %q: %v", id, err)
			}
			if len(result.Plan.Layers) != 1 {
				t.Fatalf("compiled %q to %d layers, want one image layer", id, len(result.Plan.Layers))
			}

			// Exercise the exact serialized contract handed to Chronon rather
			// than only inspecting the compiler's in-memory layer.
			wire, err := result.Plan.Marshal()
			if err != nil {
				t.Fatalf("marshal Chronon plan for %q: %v", id, err)
			}
			var concrete struct {
				Schema string `json:"schema"`
				Layers []struct {
					ID            string          `json:"id"`
					Type          string          `json:"type"`
					Asset         string          `json:"asset"`
					Enable3D      bool            `json:"enable_3d"`
					Animation     *LayerAnimation `json:"animation"`
					TextAnimators []TextAnimator  `json:"text_animators"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(wire, &concrete); err != nil {
				t.Fatalf("decode Chronon plan for %q: %v", id, err)
			}
			if concrete.Schema != "chronon.render-plan.v2" || len(concrete.Layers) != 1 {
				t.Fatalf("serialized plan schema/layers = %q/%d, want chronon.render-plan.v2/1", concrete.Schema, len(concrete.Layers))
			}
			layer := concrete.Layers[0]
			if layer.ID != imageLayerID("image-"+id) || layer.Type != "image" || layer.Asset != "assets/semantic/test-image.png" {
				t.Fatalf("serialized image layer = %+v", layer)
			}
			if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
				t.Fatalf("image motion %q did not reach the render plan as layer tracks", id)
			}
			if len(layer.TextAnimators) != 0 {
				t.Fatalf("image motion %q unexpectedly lowered to text animators: %+v", id, layer.TextAnimators)
			}

			want, err := animationForMotion(id, nil, "", layerDurationFrames(t, result.Plan.Layers[0]), 0)
			if err != nil {
				t.Fatalf("independently lower image motion %q: %v", id, err)
			}
			if !reflect.DeepEqual(layer.Animation.Tracks, want.Tracks) {
				t.Fatalf("serialized %q tracks differ from registry lowering\n got: %+v\nwant: %+v", id, layer.Animation.Tracks, want.Tracks)
			}
			if got, want3D := layer.Enable3D, layerUses3D(want); got != want3D {
				t.Fatalf("serialized %q enable_3d = %v, want %v from its tracks", id, got, want3D)
			}
		})
	}
}

func layerDurationFrames(t *testing.T, layer Layer) int64 {
	t.Helper()
	if layer.DurationFrames <= 0 {
		t.Fatalf("compiled image layer has invalid duration: %d", layer.DurationFrames)
	}
	return layer.DurationFrames
}

func TestTextPresetCatalogContainsOnlyTheTwoSupportedIDs(t *testing.T) {
	got := motion.OverlayPresetIDs(string(PresetText))
	want := []string{PhraseDefaultPresetID, StaticTextSmokePresetID}
	if len(got) != len(want) {
		t.Fatalf("canonical text presets = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical text presets = %v, want exactly %v", got, want)
		}
	}
}

func TestPhraseDefaultAndMotionFamilyRemainIndependent(t *testing.T) {
	if _, err := ResolveOfficialPreset(PhraseDefaultPresetID); err != nil {
		t.Fatal(err)
	}
	if got := len(motion.Registry.FamilyMotionIDs("typewriter")); got != 5 {
		t.Fatalf("typewriter family has %d motions, want 5", got)
	}
	if got := len(motion.Registry.FamilyMotionIDs("web")); got != 0 {
		t.Fatalf("web family has %d placeholder motions, want 0", got)
	}
	if got := len(motion.Registry.FamilyMotionIDs("3d")); got == 0 {
		t.Fatal("3d family should expose catalog motions independently of phrase_default")
	}
}

func TestRuntimeStyleOverrideSchemaContract(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	for _, pointer := range []string{
		"#/properties/items/items/properties/params",
		"#/properties/items/items/properties/style",
	} {
		t.Run(pointer, func(t *testing.T) {
			properties := contractschema.At(t, schema, pointer)["properties"].(map[string]any)
			font := properties["font_family"].(map[string]any)["enum"].([]any)
			if len(font) != 3 || font[0] != "poppins" || font[1] != "inter" || font[2] != "dejavu_sans" {
				t.Errorf("font_family enum = %v, want [poppins inter dejavu_sans]", font)
			}
			// The schema's documented control set and the validator's key set are
			// the same set, in both spellings. Pinning only the keys that happened
			// to exist when this test was written is how font_size_px and the four
			// shadow controls ended up documented but unenforced.
			documented := make(map[string]bool, len(properties))
			for name := range properties {
				documented[name] = true
			}
			for _, key := range runtimeTextStyleKeys {
				if !documented[key] {
					t.Errorf("the runtime validator enforces %q but the schema does not document it", key)
				}
				delete(documented, key)
			}
			for extra := range documented {
				t.Errorf("the schema documents %q, which the runtime validator does not enforce", extra)
			}
			for _, bound := range []struct {
				key              string
				minimum          float64
				maximum          float64
				exclusiveMinimum bool
			}{
				{"glow_size", 0, maxRuntimeGlowSize, false},
				{"stroke_size", 0, maxRuntimeStrokeSize, false},
				{"font_size_px", 0, maxRuntimeFontSize, true},
				{"shadow_blur_px", 0, maxRuntimeShadowSize, false},
				{"shadow_opacity", 0, 1, false},
				{"shadow_offset_x_px", -maxRuntimeShadowSize, maxRuntimeShadowSize, false},
				{"shadow_offset_y_px", -maxRuntimeShadowSize, maxRuntimeShadowSize, false},
			} {
				field, ok := properties[bound.key].(map[string]any)
				if !ok {
					t.Errorf("%s is not documented", bound.key)
					continue
				}
				if field["type"] != "number" || field["maximum"] != bound.maximum {
					t.Errorf("%s schema = %v, want number with maximum %g", bound.key, field, bound.maximum)
					continue
				}
				if bound.exclusiveMinimum {
					if field["exclusiveMinimum"] != float64(0) || field["minimum"] != nil {
						t.Errorf("%s schema = %v, want exclusiveMinimum 0", bound.key, field)
					}
					continue
				}
				if field["minimum"] != bound.minimum {
					t.Errorf("%s schema = %v, want minimum %g", bound.key, field, bound.minimum)
				}
			}
		})
	}
}

func TestRuntimeTextStyleOverridesAcceptInclusiveBounds(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"glow_size":256,"stroke_size":64,"font_size_px":512,`+
			`"shadow_blur_px":256,"shadow_opacity":1,`+
			`"shadow_offset_x_px":-256,"shadow_offset_y_px":256}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style.Glow == nil || layer.Style.Glow.Radius != maxRuntimeGlowSize {
		t.Fatalf("maximum glow override = %+v", layer.Style.Glow)
	}
	if layer.Style.Stroke == nil || layer.Style.Stroke.Width != maxRuntimeStrokeSize {
		t.Fatalf("maximum stroke override = %+v", layer.Style.Stroke)
	}
	if layer.Style.FontSize != maxRuntimeFontSize {
		t.Fatalf("maximum font size override = %v", layer.Style.FontSize)
	}
	shadow := layer.Style.Shadow
	if shadow == nil || shadow.Blur != maxRuntimeShadowSize || shadow.Opacity != 1 {
		t.Fatalf("maximum shadow blur/opacity override = %+v", shadow)
	}
	if len(shadow.Offset) != 2 || shadow.Offset[0] != -maxRuntimeShadowSize || shadow.Offset[1] != maxRuntimeShadowSize {
		t.Fatalf("maximum shadow offset override = %v", shadow.Offset)
	}
}

// TestRuntimeShadowOpacityZeroDisablesTheShadow pins the documented semantics:
// "shadow opacity zero disables the shadow", the same zero-disables rule glow
// and stroke already follow. A shadow at zero opacity is invisible, so keeping
// the block in the plan would only mislead the next reader of it.
func TestRuntimeShadowOpacityZeroDisablesTheShadow(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"shadow_blur_px":12,"shadow_opacity":0}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style.Shadow != nil {
		t.Fatalf("shadow opacity zero left a shadow in the plan: %+v", layer.Style.Shadow)
	}
}

func TestPhraseDefaultPresetIsTheSingleModernTextPreset(t *testing.T) {
	definition, err := ResolveOfficialPreset(PhraseDefaultPresetID)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Family != PresetText || definition.Motion.ID == "" {
		t.Fatalf("phrase default preset is incomplete: %+v", definition)
	}
	if definition.Style.Stroke == nil || definition.Style.Shadow == nil {
		t.Fatalf("phrase default preset lost legibility styling: %+v", definition.Style)
	}
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-default","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"depth_parallax_reveal","text":"A MODERN 2.5D TITLE","start_ms":0,"end_ms":5000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Animation == nil {
		t.Fatalf("phrase default did not lower to an animated layer: %+v", result.Plan.Layers)
	}
}
