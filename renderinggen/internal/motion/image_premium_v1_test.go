package motion

import (
	"testing"
)

var expectedImagePremiumV1 = []string{
	"image_glow_depth_in", "image_border_draw_in", "image_soft_yaw_glow",
	"image_tilt_frame_in", "image_frame_scale_reveal", "image_glow_pulse_settle",
	"image_neon_trace", "image_corner_bloom", "image_depth_float",
	"image_parallax_frame", "image_mask_wipe_border", "image_split_light_reveal",
	"image_focus_breath", "image_roll_depth_in", "image_card_flip_soft",
	"image_border_expand", "image_glow_ring_expand", "image_caption_frame_combo",
	"image_spotlight_focus", "image_stack_focus",
}

func TestImagePremiumV1InventoryIsExactAndSeparate(t *testing.T) {
	ids := Registry.ImagePremiumV1MotionIDs()
	if len(ids) != len(expectedImagePremiumV1) {
		t.Fatalf("image_premium_v1 has %d motions, want %d: %v", len(ids), len(expectedImagePremiumV1), ids)
	}
	for _, id := range expectedImagePremiumV1 {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		if definition.Category != "image_premium_v1" || definition.ImageRecipe == nil {
			t.Errorf("%s category/recipe = %q/%+v", id, definition.Category, definition.ImageRecipe)
		}
		if err := ValidateDefinition(definition); err != nil {
			t.Errorf("%s invalid: %v", id, err)
		}
	}
	if got := len(Registry.ImageOverlayMotionIDs()); got != 18 {
		t.Fatalf("premium additions changed the legacy image inventory to %d; want 18", got)
	}
	if got := len(Registry.EditorialImageV1MotionIDs()); got != 14 {
		t.Fatalf("premium additions changed the editorial image inventory to %d; want 14", got)
	}
}

func TestImagePremiumV1RecipesDeclareRequiredRendererPrimitives(t *testing.T) {
	for _, id := range expectedImagePremiumV1 {
		plugin, _ := Registry.Resolve(id)
		d := plugin.(DeclarativePlugin).Definition
		r := d.ImageRecipe
		switch id {
		case "image_glow_depth_in", "image_soft_yaw_glow", "image_glow_pulse_settle", "image_focus_breath":
			if !componentHasAnimatedEffectParam(r, "light.glow", "intensity") {
				t.Errorf("%s does not animate glow intensity", id)
			}
		case "image_border_draw_in", "image_neon_trace":
			if !componentHasTrim(r) {
				t.Errorf("%s does not author a native Trim Path", id)
			}
		case "image_mask_wipe_border":
			if r.Mask == nil || r.Mask.Kind != "wipe_left" {
				t.Errorf("%s has no animated image wipe mask", id)
			}
		case "image_parallax_frame", "image_roll_depth_in", "image_tilt_frame_in", "image_depth_float", "image_card_flip_soft":
			if !componentHasSync3DFrame(d) {
				t.Errorf("%s frame is not synchronized in 3D", id)
			}
		case "image_caption_frame_combo":
			if !r.RequireCaption || len(r.CaptionTracks) == 0 {
				t.Errorf("%s has no required animated caption", id)
			}
		case "image_stack_focus":
			if !r.Stack || r.ActiveLayerParam != "active_layer_id" || len(r.InactiveTracks) == 0 {
				t.Errorf("%s does not define active/inactive image selection", id)
			}
		}
	}
}

func componentHasAnimatedEffectParam(recipe *ImageMotionRecipe, effectID, param string) bool {
	for _, component := range recipe.Components {
		for _, track := range component.EffectParamTracks {
			if track.EffectID == effectID && track.Param == param {
				return true
			}
		}
	}
	return false
}

func componentHasTrim(recipe *ImageMotionRecipe) bool {
	for _, component := range recipe.Components {
		if component.Trim != nil {
			return true
		}
	}
	return false
}

func componentHasSync3DFrame(definition MotionDefinition) bool {
	for _, component := range definition.ImageRecipe.Components {
		if component.SyncTransform && component.Shape == "path" {
			for _, track := range definition.Tracks {
				if IsCameraBacked3DProperty(track.Property) {
					return true
				}
			}
		}
	}
	return false
}

func TestImagePremiumV1RejectsUnreachableRecipeContracts(t *testing.T) {
	plugin, _ := Registry.Resolve("image_stack_focus")
	definition := plugin.(DeclarativePlugin).Definition
	definition.ImageRecipe.ActiveLayerParam = ""
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("stack recipe without active image selector was accepted")
	}

	plugin, _ = Registry.Resolve("image_neon_trace")
	definition = plugin.(DeclarativePlugin).Definition
	definition.ImageRecipe.Components[0].Trim.Animation.Keyframes[1].Frame = 0
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("non-increasing trim track was accepted")
	}

	plugin, _ = Registry.Resolve("image_focus_breath")
	definition = plugin.(DeclarativePlugin).Definition
	definition.RequiredProperties = []string{"scale"}
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("incomplete required_properties metadata was accepted")
	}
}
