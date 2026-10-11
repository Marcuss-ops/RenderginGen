package overlay

import (
	"strings"
	"testing"
)

// Removed-catalog guard: the Classic phrase pack (14 animations), the twelve
// legacy TransitionLook looks and the six retired Final3D compositions must
// stay absent from the live worker. The producer no longer emits them; this
// test pins that no live catalog, preset map or motion registry re-admits
// them, and that a stale DB plan carrying one fails closed instead of
// rendering a wrong picture.
var removedClassicMotionIDs = []string{
	"classic_fade", "classic_rise", "classic_drop",
	"classic_slide_from_left", "classic_slide_from_right",
	"classic_scale_pop", "classic_zoom_settle", "classic_typewriter",
	"classic_glyph_lift", "classic_glyph_tracking_in",
	"classic_tracking_tighten", "classic_blur_focus",
	"classic_word_emphasis", "classic_word_scale_in",
}

var removedTransitionIDs = []string{
	"transition_wipe", "transition_push_through",
	"transition_dip_to_black", "transition_dip_to_color",
	"transition_blinds", "transition_iris_circle",
	"transition_glitch_slices", "transition_light_leak",
	"transition_barn_doors", "transition_curtain_lift",
	"transition_diamond_iris", "transition_four_way_doors",
}

var removedFinal3DIDs = []string{
	"logo_reveal", "product_orbit", "title_card_3d",
	"camera_push", "light_pulse", "macbook_product",
}

func removedCatalogIDs() []string {
	out := make([]string, 0, len(removedClassicMotionIDs)+len(removedTransitionIDs)+len(removedFinal3DIDs))
	out = append(out, removedClassicMotionIDs...)
	out = append(out, removedTransitionIDs...)
	out = append(out, removedFinal3DIDs...)
	return out
}

func TestRemovedCatalogIDsAbsentFromOfficialPresets(t *testing.T) {
	for _, id := range removedCatalogIDs() {
		if _, ok := officialPresets[id]; ok {
			t.Errorf("removed catalog id %q is present in officialPresets", id)
		}
		for _, family := range []string{string(PresetText), string(PresetImage)} {
			if _, err := resolveOfficialPreset(id, family); err == nil {
				t.Errorf("removed catalog id %q resolves under family %q: stale plans must fail closed", id, family)
			} else if !strings.Contains(err.Error(), "unsupported") {
				t.Errorf("removed catalog id %q error = %q, want an unsupported-preset failure", id, err)
			}
		}
	}
}

func TestRemovedClassicMotionsFailClosed(t *testing.T) {
	for _, id := range removedClassicMotionIDs {
		if _, err := animationForMotion(id, nil, "stale plan", 48, 0, true); err == nil {
			t.Errorf("removed classic motion %q compiles: stale plans must fail closed", id)
		}
	}
}
