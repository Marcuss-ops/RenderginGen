package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

const premiumImageAssetRef = `{"asset_id":"premium-photo","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/premium.png","media_type":"image/png"}`

func TestImageStackClassificationComesFromRecipeMetadata(t *testing.T) {
	stack := &motion.MotionDefinition{Category: "future_image_family", ImageRecipe: &motion.ImageMotionRecipe{Stack: true}}
	if !isMultiImageRecipe(stack) {
		t.Fatal("stack recipe was not recognized outside the premium category")
	}
	if got := premiumImageRecipeDefinition(stack); got != stack {
		t.Fatal("generic image recipe was rejected because its category was not premium")
	}
	visualAccent := &motion.MotionDefinition{Category: "brush_v1", ImageRecipe: &motion.ImageMotionRecipe{}}
	if got := premiumImageRecipeDefinition(visualAccent); got != nil {
		t.Fatal("visual accent recipe entered the generic image recipe lowering")
	}
	if isMultiImageRecipe(&motion.MotionDefinition{Category: "image_premium_v1", ImageRecipe: &motion.ImageMotionRecipe{}}) {
		t.Fatal("non-stack premium recipe was classified as a stack")
	}
	if !animationUsesMultiImageRecipe("image_stack_focus") {
		t.Fatal("registered image_stack_focus recipe was not classified as a stack")
	}
}

func premiumImagePlan(id, extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"premium-%s","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"premium-image","kind":"image","template_id":"image_popup","preset_id":"image_focus_in","motion_id":%q,"start_ms":0,"end_ms":5000,"asset_refs":[%s]%s}]}`,
		id, id, premiumImageAssetRef, extra))
}

func TestImagePremiumV1EveryRecipeLowersToNativeV3(t *testing.T) {
	ids := motion.Registry.CategoryMotionIDs("image_premium_v1")
	if len(ids) != 21 {
		t.Fatalf("premium catalog has %d ids, want 21: %v", len(ids), ids)
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			var result CompileResult
			var err error
			switch id {
			case "image_stack_focus":
				result, err = CompileSemantic(premiumStackPlan("front"))
			case "image_caption_frame_combo":
				result, err = CompileSemantic(premiumImagePlan(id, `,"entity_caption":"Premium caption"`))
			default:
				result, err = CompileSemantic(premiumImagePlan(id, ""))
			}
			if err != nil {
				t.Fatalf("compile premium recipe: %v", err)
			}
			wire, err := result.Plan.Marshal()
			if err != nil {
				t.Fatalf("marshal premium recipe: %v", err)
			}
			var document struct {
				Schema  string `json:"schema"`
				Version int    `json:"version"`
				Layers  []struct {
					Type              string                  `json:"type"`
					Shape             *LayerShape             `json:"shape"`
					Animation         *LayerAnimation         `json:"animation"`
					Enable3D          bool                    `json:"enable_3d"`
					Effects           []LayerEffect           `json:"effects"`
					EffectParamTracks []LayerEffectParamTrack `json:"effect_param_tracks"`
					Masks             []LayerMask             `json:"masks"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(wire, &document); err != nil {
				t.Fatalf("decode V3 render plan: %v", err)
			}
			if document.Schema != "chronon.render-plan.v3" || document.Version != 3 {
				t.Fatalf("premium plan schema/version = %q/%d, want V3", document.Schema, document.Version)
			}
			if len(document.Layers) < 2 {
				t.Fatalf("premium motion produced %d layers, want image plus component", len(document.Layers))
			}
			var imageFound, componentFound bool
			for _, layer := range document.Layers {
				if layer.Type == "image" {
					imageFound = true
					if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
						t.Errorf("premium image motion has no native layer tracks")
					}
					if layer.Enable3D != requiresPremium3D(id) {
						t.Errorf("image enable_3d = %v, want %v", layer.Enable3D, requiresPremium3D(id))
					}
				}
				if layer.Type == "shape" || layer.Type == "color" {
					componentFound = true
				}
				if id == "image_border_draw_in" || id == "image_neon_trace" {
					if layer.Shape != nil && len(layer.Shape.Operators) > 0 {
						if layer.Shape.Operators[0].Kind != "trim" || layer.Shape.Operators[0].Params.Animation == nil {
							t.Errorf("%s Trim Path operator is not animated: %+v", id, layer.Shape.Operators[0])
						}
						componentFound = true
					}
				}
				if id == "image_glow_depth_in" || id == "image_focus_breath" {
					if len(layer.Effects) > 0 && len(layer.EffectParamTracks) > 0 {
						componentFound = true
					}
				}
				if id == "image_mask_wipe_border" && len(layer.Masks) > 0 {
					mask := layer.Masks[0]
					if mask.Type != "path" || len(mask.Path) != len(mask.TargetPath) || mask.Animation == nil {
						t.Errorf("animated path mask contract is incomplete: %+v", mask)
					}
				}
				if id == "image_split_light_reveal" && layer.Shape != nil {
					if gradient, ok := layer.Shape.Fill.(*LayerGradient); ok && len(gradient.ColorStops) >= 2 {
						componentFound = true
					}
				}
			}
			if !imageFound || !componentFound {
				t.Fatalf("premium recipe image/component presence = %v/%v", imageFound, componentFound)
			}
		})
	}
}

func requiresPremium3D(id string) bool {
	switch id {
	case "image_glow_depth_in", "image_soft_yaw_glow", "image_tilt_frame_in", "image_depth_float", "image_parallax_frame", "image_roll_depth_in", "image_card_flip_soft", "image_spotlight_focus", "image_stack_focus":
		return true
	default:
		return false
	}
}

func TestImageStackFocusSelectsActiveLayerAndRejectsBadSelectors(t *testing.T) {
	plan := premiumStackPlan("front")
	result, err := CompileSemantic(plan)
	if err != nil {
		t.Fatalf("compile selected stack: %v", err)
	}
	var back, front *Layer
	for i := range result.Plan.Layers {
		layer := &result.Plan.Layers[i]
		// The premium active-frame shape descends from the front image and
		// inherits its ID as a prefix (":front:image:premium:..."); only the
		// image layers themselves carry the stack animation under test.
		if layer.Type != "image" {
			continue
		}
		if strings.Contains(layer.ID, ":back:image") {
			back = layer
		}
		if strings.Contains(layer.ID, ":front:image") {
			front = layer
		}
	}
	if back == nil || front == nil || back.Animation == nil || front.Animation == nil {
		t.Fatalf("stack selection emitted incomplete image layers: %+v", result.Plan.Layers)
	}
	backZ, backOK := premiumNumericTrackValue(back.Animation, "position_z", 1)
	frontZ, frontOK := premiumNumericTrackValue(front.Animation, "position_z", 1)
	if !backOK || !frontOK || backZ != 0 || frontZ != 40 {
		t.Fatalf("active/inactive image tracks are not selected correctly: back Z=%v/%v front Z=%v/%v", backZ, backOK, frontZ, frontOK)
	}
	for _, bad := range [][]byte{
		premiumStackPlan("missing"),
		[]byte(strings.Replace(string(premiumStackPlan("front")), `,"motion_params":{"active_layer_id":"front"}`, "", 1)),
	} {
		if _, err := CompileSemantic(bad); err == nil {
			t.Errorf("invalid active-layer selection accepted: %s", bad)
		}
	}
}

func premiumNumericTrackValue(animation *LayerAnimation, property string, keyframe int) (float64, bool) {
	for _, track := range animation.Tracks {
		if track.Property != property || keyframe >= len(track.Keyframes) {
			continue
		}
		value, ok := track.Keyframes[keyframe].Value.(float64)
		return value, ok
	}
	return 0, false
}

func premiumStackPlan(active string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"stack","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"stack","kind":"image","template_id":"image_popup","preset_id":"image_focus_in","motion_id":"image_stack_focus","motion_params":{"active_layer_id":%q},"start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"back","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/back.png"},{"asset_id":"front","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/front.png"}],"image_layers":[{"id":"back","asset_id":"back","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"},{"id":"front","asset_id":"front","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"}]}]}`, active))
}

func TestImagePremiumCaptionAndDurationContractsFailClosed(t *testing.T) {
	if _, err := CompileSemantic(premiumImagePlan("image_caption_frame_combo", "")); err == nil {
		t.Fatal("caption-required motion accepted a missing caption")
	}
	short := []byte(strings.Replace(string(premiumImagePlan("image_glow_depth_in", "")), `"end_ms":5000`, `"end_ms":500`, 1))
	if _, err := CompileSemantic(short); err == nil {
		t.Fatal("premium motion accepted a layer duration below catalog bounds")
	}
	caption := `,"entity_caption":"Grace Hopper"`
	result, err := CompileSemantic(premiumImagePlan("image_caption_frame_combo", caption))
	if err != nil {
		t.Fatalf("compile required caption recipe: %v", err)
	}
	found := false
	for _, layer := range result.Plan.Layers {
		if layer.Type == "text" && layer.Text == "Grace Hopper" && layer.Animation != nil && len(layer.Animation.Tracks) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("required animated caption did not reach the plan: %+v", result.Plan.Layers)
	}
}

func TestStandaloneImageCaptionsSurviveRecipeLowering(t *testing.T) {
	plainImage := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"standalone-caption","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"standalone","kind":"image","template_id":"image_popup","preset_id":"image_focus_in","start_ms":0,"end_ms":3000,"entity_caption":"Caption acceptance","asset_refs":[%s]}]}`, premiumImageAssetRef))
	for _, tc := range []struct {
		name string
		plan []byte
	}{
		{name: "plain standalone image", plan: plainImage},
		{name: "premium image recipe", plan: premiumImagePlan("image_caption_frame_combo", `,"entity_caption":"Caption acceptance"`)},
		{name: "visual accent recipe", plan: premiumImagePlan("brush_arrow_point", `,"entity_caption":"Caption acceptance"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := CompileSemantic(tc.plan)
			if err != nil {
				t.Fatalf("compile image caption plan: %v", err)
			}
			var image *Layer
			var caption *Layer
			for i := range result.Plan.Layers {
				layer := &result.Plan.Layers[i]
				if layer.Type == "image" && (image == nil || layer.ID == "premium-image:image") {
					image = layer
				}
				if layer.Type == "text" && layer.Text == "Caption acceptance" {
					caption = layer
				}
			}
			if image == nil || caption == nil {
				t.Fatalf("image/caption output missing: image=%+v caption=%+v layers=%+v", image, caption, result.Plan.Layers)
			}
			if caption.CaptionForImageID != image.ID {
				t.Fatalf("caption image link = %q, want %q", caption.CaptionForImageID, image.ID)
			}
			if caption.StartFrame != image.StartFrame || caption.DurationFrames != image.DurationFrames {
				t.Fatalf("caption timing=(%d,%d), image timing=(%d,%d)", caption.StartFrame, caption.DurationFrames, image.StartFrame, image.DurationFrames)
			}
			wire, err := result.Plan.Marshal()
			if err != nil {
				t.Fatalf("marshal compiled caption plan: %v", err)
			}
			var wireDocument struct {
				Layers []map[string]json.RawMessage `json:"layers"`
			}
			if err := json.Unmarshal(wire, &wireDocument); err != nil {
				t.Fatalf("decode Chronon plan: %v", err)
			}
			for _, wireLayer := range wireDocument.Layers {
				text, ok := wireLayer["text"]
				if !ok {
					continue
				}
				var decodedText string
				if err := json.Unmarshal(text, &decodedText); err != nil || decodedText != "Caption acceptance" {
					continue
				}
				for _, internalKey := range []string{"role", "style_policy", "caption_for_image_id", "caption_layout"} {
					if _, leaked := wireLayer[internalKey]; leaked {
						t.Errorf("internal caption field %q leaked into Chronon wire", internalKey)
					}
				}
			}
		})
	}
}

func TestImagePremiumNativeV3ComponentOrderAndMaskTopology(t *testing.T) {
	result, err := CompileSemantic(premiumImagePlan("image_caption_frame_combo", `,"entity_caption":"Frame label"`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) < 3 || result.Plan.Layers[0].Type != "shape" || result.Plan.Layers[1].Type != "image" {
		t.Fatalf("before-image frame ordering = %+v", result.Plan.Layers)
	}
	masked, err := CompileSemantic(premiumImagePlan("image_mask_wipe_border", ""))
	if err != nil {
		t.Fatal(err)
	}
	image := masked.Plan.Layers[0]
	if len(image.Masks) != 1 || len(image.Masks[0].Path) != len(image.Masks[0].TargetPath) {
		t.Fatalf("mask paths don't have matching morph topology: %+v", image.Masks)
	}
	for _, command := range image.Masks[0].Path {
		if command.Type == "close" {
			continue
		}
		if len(command.Point) != 2 {
			t.Fatalf("mask path point is not vec2: %+v", command)
		}
	}
}
