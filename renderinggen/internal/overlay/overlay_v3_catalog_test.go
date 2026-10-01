package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

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
		plugin, err := motion.Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve image motion %s: %v", id, err)
		}
		definition := plugin.(motion.DeclarativePlugin).Definition
		animation, err := animationForMotion(id, nil, "", 240, 0)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) != 0 {
			t.Fatalf("image motion %s did not lower to layer tracks only: %+v", id, animation)
		}
		if !layerUses3D(animation) {
			t.Fatalf("image motion %s did not activate 3D", id)
		}
		catalogPose := make(map[string]any)
		for _, track := range definition.Tracks {
			for _, keyframe := range track.Keyframes {
				if keyframe.Frame > int64(definition.Enter) {
					break
				}
				catalogPose[track.Property] = keyframe.Value
			}
		}
		if len(catalogPose) == 0 {
			t.Fatalf("image motion %s has no authored entrance pose to certify", id)
		}
		for _, track := range animation.Tracks {
			if track.Property == "opacity" {
				last := track.Keyframes[len(track.Keyframes)-1]
				if last.Frame != 239 || last.Value != 0.0 {
					t.Fatalf("image motion %s exit = frame %d value %v, want frame 239 opacity 0", id, last.Frame, last.Value)
				}
			}
			if resting, exists := catalogPose[track.Property]; exists {
				settled, ok := resting.(float64)
				if !ok {
					t.Fatalf("image motion %s resting %s value has type %T", id, track.Property, resting)
				}
				settledFrame := int64(definition.Enter)
				if track.Property == "rotation_y" || track.Property == "rotation_x" || track.Property == "position_z" {
					if settled != 0 {
						t.Fatalf("image motion %s 3D property %s rests at %v, want neutral 0", id, track.Property, settled)
					}
				}
				if got := sampleNumericTrack(track, settledFrame); got != settled {
					t.Fatalf("image motion %s %s at resting frame %d = %v, want %v", id, track.Property, settledFrame, got, settled)
				}
			}
		}
		if animationUses3D(animation) != motion.IsCameraBacked3DProperty(firstCameraBackedProperty(definition)) {
			t.Fatalf("image motion %s no longer routes camera-backed transforms", id)
		}
	}
}

func sampleNumericTrack(track AnimationTrack, frame int64) float64 {
	var latest float64
	for _, keyframe := range track.Keyframes {
		if keyframe.Frame > frame {
			break
		}
		value, ok := keyframe.Value.(float64)
		if !ok {
			return math.NaN()
		}
		latest = value
	}
	return latest
}

func firstCameraBackedProperty(definition motion.MotionDefinition) string {
	for _, track := range definition.Tracks {
		if motion.IsCameraBacked3DProperty(track.Property) {
			return track.Property
		}
	}
	return ""
}

func TestImageMotionInventoryHasAllCatalogImageAnimations(t *testing.T) {
	inventory := ImageMotionInventory()
	want := map[string][]string{
		"editorial_image_v1": motion.Registry.EditorialImageV1MotionIDs(),
		"image_premium_v1":   motion.Registry.ImagePremiumV1MotionIDs(),
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
		"brush_v1":      motion.Registry.VisualAccentsV1MotionIDs("brush_v1"),
		"web_rect_v1":   motion.Registry.VisualAccentsV1MotionIDs("web_rect_v1"),
		"paint_v1":      motion.Registry.VisualAccentsV1MotionIDs("paint_v1"),
		"light_leak_v1": motion.Registry.VisualAccentsV1MotionIDs("light_leak_v1"),
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
	if len(all) != 100 || len(motion.Registry.ImageOverlayMotionIDs()) != 18 || len(motion.Registry.ImagePremiumV1MotionIDs()) != 20 {
		t.Fatalf("combined image motion inventory = %d, want 100 catalog IDs (52 legacy + 48 visual accents), 18 legacy IDs, and 20 premium IDs", len(all))
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
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-motion-%[1]s","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"image-%[1]s","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in","motion_id":%[2]q,"start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"test-image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/test-image.png","media_type":"image/png"}]}]}`, id, id))
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
			// Blur motions lower to layer effects, and a plan containing
			// effects marshals as chronon.render-plan.v3 by contract; every
			// other image motion stays on the v2 baseline.
			wantSchema := "chronon.render-plan.v2"
			if strings.Contains(id, "blur") {
				wantSchema = "chronon.render-plan.v3"
			}
			if concrete.Schema != wantSchema || len(concrete.Layers) != 1 {
				t.Fatalf("serialized plan schema/layers = %q/%d, want %s/1", concrete.Schema, len(concrete.Layers), wantSchema)
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

			preset, err := ResolveOfficialPreset("image_focus_in")
			if err != nil {
				t.Fatalf("resolve image preset: %v", err)
			}
			want, err := animationForMotion(id, nil, "", layerDurationFrames(t, result.Plan.Layers[0]), preset.Motion.Exit)
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

// TestEveryPhraseFamilyMotionReachesTheChrononRenderPlan is the runtime
// counterpart to the registry inventory test: every selectable family ID must
// resolve, lower to a text layer and survive serialization into Chronon's plan.
func TestCompositeEntityImageLayersKeepIndependentTimingAndMotion(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-entities","video_id":"composite-entities",
		"width":1920,"height":1080,"fps_num":24,"fps_den":1,
		"items":[{
			"id":"ada+grace","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":1000,"end_ms":7500,"duration_ms":6500,
			"asset_refs":[
				{"asset_id":"ada","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/ada.jpg","media_type":"image/jpeg"},
				{"asset_id":"grace","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/grace.jpg","media_type":"image/jpeg"}
			],
			"image_layers":[
				{"id":"ada","asset_id":"ada","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","caption":"Ada Lovelace","params":{"width":422,"height":453,"position_x":-460.8,"position_y":0,"fit":"contain"}},
				{"id":"grace","asset_id":"grace","start_ms":1500,"end_ms":6500,"preset_id":"image_scale_in","motion_id":"image_25d_yaw_flip_in","caption":"Grace Hopper","params":{"width":422,"height":453,"position_x":460.8,"position_y":0,"fit":"contain"}}
			]
		}]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite entity images: %v", err)
	}
	if len(result.Plan.Layers) != 4 {
		t.Fatalf("two composite images with captions lower to %d Chronon layers, want two images and two captions: %+v", len(result.Plan.Layers), result.Plan.Layers)
	}
	first, second := result.Plan.Layers[0], result.Plan.Layers[2]
	if first.Asset != "assets/semantic/ada.jpg" || second.Asset != "assets/semantic/grace.jpg" {
		t.Fatalf("composite assets = %q / %q", first.Asset, second.Asset)
	}
	if first.StartFrame != 24 || first.DurationFrames != 120 || second.StartFrame != 60 || second.DurationFrames != 120 {
		t.Fatalf("staggered layer frames = [%d,+%d), [%d,+%d), want [24,+120), [60,+120)", first.StartFrame, first.DurationFrames, second.StartFrame, second.DurationFrames)
	}
	if !first.EntityImage || !second.EntityImage || first.Enable3D || !second.Enable3D {
		t.Fatalf("entity-image or 2.5D routing lost: first=%+v second=%+v", first, second)
	}
	if len(first.Position) != 2 || first.Position[0] != -460.8 || len(second.Position) != 2 || second.Position[0] != 460.8 {
		t.Fatalf("paired geometry = %v / %v", first.Position, second.Position)
	}
	if first.Animation == nil || second.Animation == nil || len(first.Animation.Tracks) == 0 || len(second.Animation.Tracks) == 0 {
		t.Fatalf("both child motions must lower independently: %+v / %+v", first.Animation, second.Animation)
	}
	if reflect.DeepEqual(first.Animation.Tracks, second.Animation.Tracks) {
		t.Fatal("independently selected image motions collapsed to the same animation tracks")
	}
	if len(result.Assets) != 2 {
		t.Fatalf("composite prepared assets = %d, want both image assets", len(result.Assets))
	}
	if result.Plan.Layers[1].Type != "text" || result.Plan.Layers[1].Text != "Ada Lovelace" ||
		result.Plan.Layers[3].Type != "text" || result.Plan.Layers[3].Text != "Grace Hopper" {
		t.Fatalf("composite child captions were not lowered beside their portraits: %+v", result.Plan.Layers)
	}
}

func TestCompositeEntityImageLayerCountsTwoThroughFiveCompile(t *testing.T) {
	for count := 2; count <= 5; count++ {
		t.Run(fmt.Sprintf("layers-%d", count), func(t *testing.T) {
			assets := make([]any, 0, count)
			layers := make([]any, 0, count)
			parentEnd := int64(5000 + (count-1)*250)
			for index := 0; index < count; index++ {
				id := fmt.Sprintf("person-%d", index)
				digest := strings.Repeat(string(rune('a'+index)), 64)
				assets = append(assets, map[string]any{
					"asset_id": id, "sha256": digest,
					"url": "https://example.test/" + id + ".jpg", "media_type": "image/jpeg",
				})
				motionID := "image_focus_reveal"
				if index%2 == 1 {
					motionID = "image_25d_yaw_flip_in"
				}
				layers = append(layers, map[string]any{
					"id": id, "asset_id": id, "start_ms": index * 250,
					"end_ms": index*250 + 5000, "preset_id": "image_focus_in",
					"motion_id": motionID, "caption": "Person " + fmt.Sprint(index),
					"params": map[string]any{"width": 300, "height": 360, "position_x": index * 320, "position_y": 0, "fit": "contain"},
				})
			}
			document := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1", "plan_id": fmt.Sprintf("multi-%d", count),
				"video_id": "multi", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
				"items": []any{map[string]any{
					"id": fmt.Sprintf("group-%d", count), "kind": "entity_image", "template_id": "image_popup",
					"preset_id": "image_focus_in", "start_ms": 0, "end_ms": parentEnd, "duration_ms": parentEnd,
					"asset_refs": assets, "image_layers": layers,
				}},
			}
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile %d-image composite: %v", count, err)
			}
			if len(compiled.Plan.Layers) != count*2 || len(compiled.Assets) != count {
				t.Fatalf("compiled layers/assets = %d/%d, want %d image+caption pairs and assets", len(compiled.Plan.Layers), len(compiled.Assets), count)
			}
			for index := 0; index < count; index++ {
				image, caption := compiled.Plan.Layers[index*2], compiled.Plan.Layers[index*2+1]
				if image.Type != "image" || caption.Type != "text" || caption.Text != "Person "+fmt.Sprint(index) {
					t.Fatalf("child %d image/caption lowering = %+v / %+v", index, image, caption)
				}
				if image.Animation == nil || len(image.Animation.Tracks) == 0 {
					t.Fatalf("child %d lost its independent motion", index)
				}
			}
		})
	}
}

func TestCompositeImageContractRejectsUndeclaredAssetAndInvalidWindow(t *testing.T) {
	base := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"bad-composite","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in","start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/a.jpg"},{"asset_id":"b","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/b.jpg"}],"image_layers":[{"id":"a","asset_id":"a","start_ms":0,"end_ms":3000,"preset_id":"image_focus_in"},{"id":"b","asset_id":"%s","start_ms":2000,"end_ms":6000,"preset_id":"image_focus_in"}]}]}`
	for _, test := range []struct {
		name, asset string
	}{
		{name: "undeclared asset", asset: "missing"},
		{name: "window exceeds parent", asset: "b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CompileSemantic([]byte(fmt.Sprintf(base, test.asset))); err == nil {
				t.Fatal("invalid composite image contract was accepted")
			}
		})
	}
}

func TestEveryPhraseFamilyMotionReachesTheChrononRenderPlan(t *testing.T) {
	var ids []string
	for _, family := range []string{"typewriter", "classic_apple", "modern_apple"} {
		ids = append(ids, motion.Registry.FamilyMotionIDs(family)...)
	}
	if len(ids) != 107 {
		t.Fatalf("registered phrase family motions = %d, want 107", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("phrase family inventory repeats %q", id)
		}
		seen[id] = true
		t.Run(id, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-motion-%[1]s","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"phrase-%[1]s","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":%[2]q,"text":"EVERY PHRASE MOTION MUST LOWER","start_ms":0,"end_ms":5000}]}`, id, id))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile phrase motion %q: %v", id, err)
			}
			if len(result.Plan.Layers) != 1 {
				t.Fatalf("phrase motion %q compiled to %d layers, want 1", id, len(result.Plan.Layers))
			}
			layer := result.Plan.Layers[0]
			// Ogni 25 caratteri a capo correttamente: the compiled text is
			// word-wrapped, never word-cut, each line <=25 runes. The test
			// fixture's 30-char phrase must wrap to two centered lines.
			unwrapped := strings.Join(strings.Fields(strings.ReplaceAll(layer.Text, "\n", " ")), " ")
			if layer.Type != "text" || unwrapped != "EVERY PHRASE MOTION MUST LOWER" {
				t.Fatalf("phrase motion %q compiled to unexpected layer (unwrapped %q): %+v", id, unwrapped, layer)
			}
			for _, line := range strings.Split(layer.Text, "\n") {
				if utf8.RuneCountInString(line) > 25 {
					t.Fatalf("phrase motion %q line %q exceeds 25-char budget", id, line)
				}
				if line != strings.TrimSpace(line) {
					t.Fatalf("phrase motion %q line %q has border whitespace", id, line)
				}
				if strings.Contains(line, "  ") {
					t.Fatalf("phrase motion %q line %q has double space", id, line)
				}
			}
			hasTracks := layer.Animation != nil && len(layer.Animation.Tracks) > 0
			if !hasTracks && len(layer.TextAnimators) == 0 {
				t.Fatalf("phrase motion %q did not reach the Chronon plan as animation tracks/animators", id)
			}
			if _, err := result.Plan.Marshal(); err != nil {
				t.Fatalf("serialize phrase motion %q Chronon plan: %v", id, err)
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
	// Editorial Visual Motion V1 grows the certified web vocabulary to 14:
	// web_cursor_focus and web_section_spotlight joined the family.
	if got := len(motion.Registry.FamilyMotionIDs("web")); got != 14 {
		t.Fatalf("web family has %d motions, want 14", got)
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
			// params is the union of text, shape, image, map and producer
			// controls. This test owns only the text-runtime subset; the other
			// controls are checked by their canonical resolvers and parity tests.
			if pointer == "#/properties/items/items/properties/style" {
				for extra := range documented {
					t.Errorf("the schema documents %q, which the runtime validator does not enforce", extra)
				}
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
