package overlay

import (
	"fmt"
	"sort"
	"strings"
)

const OfficialPresetVersion = "renderinggen-official-presets.v2"

type PresetFamily string

const (
	PresetText  PresetFamily = "text"
	PresetImage PresetFamily = "image"
)

// OfficialPresetDefinition is the single RenderingGen catalog entry. The
// compiler lowers these execution primitives into the concrete Chronon plan;
// the preset id is retained only as metadata.
type OfficialPresetDefinition struct {
	ID        string
	Family    PresetFamily
	Font      string
	FontSize  float64
	Fill      []float64
	Anchor    string
	Alignment string
	Animation string
	Unit      string
	Enter     int
	Exit      int
	Glow      bool
	BoxWidth  int
	BoxHeight int
	Fit       string
}

type presetSpec struct {
	family                    PresetFamily
	anchor, align, anim, unit string
	enter, exit               int
	glow                      bool
	boxW, boxH                int
	fit                       string
}

func textSpec(anchor, align, anim, unit string, enter, exit int, glow bool) presetSpec {
	return presetSpec{family: PresetText, anchor: anchor, align: align, anim: anim, unit: unit, enter: enter, exit: exit, glow: glow}
}

func imageSpec(anchor, anim string) presetSpec {
	return presetSpec{family: PresetImage, anchor: anchor, align: "center", anim: anim, unit: "layer", enter: 8, exit: 6, boxW: 260, boxH: 260, fit: "contain"}
}

func makePreset(id string, s presetSpec) OfficialPresetDefinition {
	d := OfficialPresetDefinition{ID: id, Family: s.family, Font: "assets/fonts/Poppins-Bold.ttf", FontSize: 58, Fill: []float64{1, 1, 1, 1}, Anchor: s.anchor, Alignment: s.align, Animation: s.anim, Unit: s.unit, Enter: s.enter, Exit: s.exit, Glow: s.glow, BoxWidth: s.boxW, BoxHeight: s.boxH, Fit: s.fit}
	if s.family == PresetImage {
		d.Font, d.FontSize, d.Fill = "", 0, nil
	}
	return d
}

// officialPresets is the only production catalog. Do not add parallel maps
// for families, animation or geometry.
var officialPresets = map[string]OfficialPresetDefinition{
	"caption_card":         makePreset("caption_card", textSpec("center", "center", "fade_in", "line", 10, 8, false)),
	"lower_third_safe":     makePreset("lower_third_safe", textSpec("lower_third", "left", "focus_in", "line", 8, 6, false)),
	"phrase_focus_v1":      makePreset("phrase_focus_v1", textSpec("safe_area", "center", "focus_in", "line", 8, 6, false)),
	"name_glow_typewriter": makePreset("name_glow_typewriter", textSpec("lower_third", "left", "fade_in", "glyph", 10, 6, true)),
	"name_glow_slide":      makePreset("name_glow_slide", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, true)),
	"name_glow_pop":        makePreset("name_glow_pop", textSpec("lower_third", "left", "fade_in", "line", 8, 6, true)),
	"name_fade_in":         makePreset("name_fade_in", textSpec("lower_third", "left", "fade_in", "line", 8, 6, false)),
	"name_slide_up":        makePreset("name_slide_up", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, false)),
	"name_pop_in":          makePreset("name_pop_in", textSpec("lower_third", "left", "soft_pop", "line", 8, 6, false)),
	"name_scale_in":        makePreset("name_scale_in", textSpec("lower_third", "left", "scale_drop", "line", 8, 6, false)),
	"name_slide_left":      makePreset("name_slide_left", textSpec("lower_third", "left", "slide_in", "line", 8, 6, false)),
	"fast_fade_through":    makePreset("fast_fade_through", textSpec("safe_area", "center", "fade_in", "line", 4, 4, false)),
	"clean_slide_up":       makePreset("clean_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, false)),
	"slide_lateral":        makePreset("slide_lateral", textSpec("safe_area", "center", "slide_in", "line", 8, 6, false)),
	"phrase_word_reveal":   makePreset("phrase_word_reveal", textSpec("safe_area", "center", "fade_in", "word", 10, 6, false)),
	"undertext_pop":        makePreset("undertext_pop", textSpec("lower_third", "center", "fade_in", "line", 8, 6, false)),
	"phrase_fade_in":       makePreset("phrase_fade_in", textSpec("safe_area", "center", "fade_in", "line", 8, 6, false)),
	"phrase_scale_in":      makePreset("phrase_scale_in", textSpec("safe_area", "center", "scale_drop", "line", 8, 6, false)),
	"phrase_slide_up":      makePreset("phrase_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, false)),
	"phrase_soft_pop":      makePreset("phrase_soft_pop", textSpec("safe_area", "center", "soft_pop", "line", 8, 6, false)),
	"snap_scale":           makePreset("snap_scale", textSpec("safe_area", "center", "scale_drop", "line", 5, 4, false)),
	"active_word_pop":      makePreset("active_word_pop", textSpec("safe_area", "center", "fade_in", "word", 8, 6, false)),
	"image_focus_in":       makePreset("image_focus_in", imageSpec("image_right", "focus_in")),
	"image_fade_in":        makePreset("image_fade_in", imageSpec("image_right", "fade_in")),
	"image_scale_in":       makePreset("image_scale_in", imageSpec("image_right", "scale_drop")),
	"image_slide_left":     makePreset("image_slide_left", imageSpec("image_left", "slide_in")),
	"image_slide_right":    makePreset("image_slide_right", imageSpec("image_right", "slide_in")),
	"image_fast_fade":      makePreset("image_fast_fade", imageSpec("image_right", "fade_in")),
	"modern_rounded_pop":   makePreset("modern_rounded_pop", imageSpec("image_right", "scale_drop")),
	"bottom_card_rise":     makePreset("bottom_card_rise", imageSpec("bottom_right", "reveal_from_bottom")),
}

func officialPresetIDs() []string {
	ids := make([]string, 0, len(officialPresets))
	for id := range officialPresets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// OfficialPresetIDs returns a deterministic snapshot for certification tools;
// callers cannot mutate the production registry through it.
func OfficialPresetIDs() []string { return officialPresetIDs() }

func resolveOfficialPreset(id, kind string) (OfficialPresetDefinition, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OfficialPresetDefinition{}, nil
	}
	d, ok := officialPresets[id]
	if !ok {
		return OfficialPresetDefinition{}, fmt.Errorf("overlay: unsupported RenderingGen official preset %q", id)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != string(d.Family) {
		return OfficialPresetDefinition{}, fmt.Errorf("overlay: preset %q is %s, not %s", id, d.Family, kind)
	}
	return d, nil
}
