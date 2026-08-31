package overlay

import (
	"fmt"
	"strings"
)

// OfficialPresetVersion is bumped when the RenderingGen-owned visual catalog
// changes. Chronon receives the resolved id as an execution primitive; it is
// not the authority for this catalog.
const OfficialPresetVersion = "renderinggen-official-presets.v1"

// officialPresets is intentionally kept in RenderingGen. PipelineGen may send
// an opaque preset_id, but only this worker decides whether that id is a
// supported production preset and which layer family it belongs to.
var officialPresets = map[string]string{
	// Compatibility names retained in the v1 contract; their definitions are
	// still resolved/materialized by RenderingGen, never by PipelineGen.
	"caption_card": "text", "lower_third_safe": "text", "phrase_focus_v1": "text",
	"name_glow_typewriter": "text", "name_glow_slide": "text", "name_glow_pop": "text",
	"name_fade_in": "text", "name_slide_up": "text", "name_pop_in": "text", "name_scale_in": "text", "name_slide_left": "text",
	"fast_fade_through": "text", "clean_slide_up": "text", "slide_lateral": "text", "phrase_word_reveal": "text", "undertext_pop": "text",
	"phrase_fade_in": "text", "phrase_scale_in": "text", "phrase_slide_up": "text", "phrase_soft_pop": "text",
	"snap_scale": "text", "active_word_pop": "text",
	"image_focus_in": "image", "image_fade_in": "image", "image_scale_in": "image", "image_slide_left": "image", "image_slide_right": "image", "image_fast_fade": "image", "modern_rounded_pop": "image", "bottom_card_rise": "image",
}

func resolveOfficialPreset(id, kind string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	family, ok := officialPresets[id]
	if !ok {
		return "", fmt.Errorf("overlay: unsupported RenderingGen official preset %q", id)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "image" && family != "image" {
		return "", fmt.Errorf("overlay: preset %q is %s, not image", id, family)
	}
	if kind == "text" && family != "text" {
		return "", fmt.Errorf("overlay: preset %q is image, not text", id)
	}
	return id, nil
}
