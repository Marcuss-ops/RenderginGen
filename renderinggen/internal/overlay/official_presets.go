package overlay

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

type PresetFamily string

const (
	PresetText  PresetFamily = "text"
	PresetImage PresetFamily = "image"
)

// PresetDefinition is the only RenderingGen preset contract. Its fields are
// deliberately grouped by responsibility so new visual variants do not grow
// one-off boolean flags.
type PresetDefinition struct {
	ID     string
	Family PresetFamily
	Style  PresetStyle
	Layout PresetLayout
	Motion MotionDefinition
}

type PresetStyle struct {
	FontFamily string
	FontSize   float64
	Fill       []float64
	Shadow     *StyleShadow
}

type StyleShadow struct {
	Color   string
	Opacity float64
	Blur    float64
	Offset  []float64
}

type PresetLayout struct {
	Anchor    string
	Alignment string
	BoxWidth  int
	BoxHeight int
	Fit       string
}

type MotionDefinition = motion.MotionDefinition

// officialFontPath is the single font asset every official preset references.
// The typewriter presets historically pointed at "fonts/Inter-Bold.ttf", a
// logical path no asset in the repository provides; the certification runtime
// masked the stale spelling by substituting Poppins-Bold.ttf for it. Declaring
// the path once removes the second spelling and the substitution shim with it.
const officialFontPath = "assets/fonts/Poppins-Bold.ttf"

type presetSpec struct {
	family                    PresetFamily
	anchor, align, anim, unit string
	enter, exit               int
	shadow                    *StyleShadow
	boxW, boxH                int
	fit                       string
}

func textSpec(anchor, align, anim, unit string, enter, exit int, shadow *StyleShadow) presetSpec {
	return presetSpec{family: PresetText, anchor: anchor, align: align, anim: anim, unit: unit, enter: enter, exit: exit, shadow: shadow}
}

func glowStyle() *StyleShadow {
	return &StyleShadow{Color: "#38BDF8", Opacity: 0.82, Blur: 14, Offset: []float64{0, 2}}
}

func imageSpec(anchor, anim string) presetSpec {
	return presetSpec{family: PresetImage, anchor: anchor, align: "center", anim: anim, unit: "layer", enter: 8, exit: 6, boxW: 260, boxH: 260, fit: "contain"}
}

func makePreset(id string, s presetSpec) PresetDefinition {
	d := PresetDefinition{ID: id, Family: s.family,
		Style:  PresetStyle{FontFamily: officialFontPath, FontSize: 58, Fill: []float64{1, 1, 1, 1}, Shadow: s.shadow},
		Layout: PresetLayout{Anchor: s.anchor, Alignment: s.align, BoxWidth: s.boxW, BoxHeight: s.boxH, Fit: s.fit},
		Motion: MotionDefinition{ID: s.anim, Unit: s.unit, Enter: s.enter, Exit: s.exit}}
	if s.family == PresetImage {
		d.Style.FontFamily, d.Style.FontSize, d.Style.Fill = "", 0, nil
	}
	return d
}

func makePhrasePreset(id, anim, unit string) PresetDefinition {
	d := makePreset(id, textSpec("center", "center", anim, unit, 72, 6, nil))
	d.Style.Fill = []float64{0.08, 0.08, 0.12, 1.0}
	return d
}

func makeTypewriterPreset(id, anim string, fontSize float64, fill []float64, shadow *StyleShadow) PresetDefinition {
	return PresetDefinition{
		ID:     id,
		Family: PresetText,
		Style:  PresetStyle{FontFamily: officialFontPath, FontSize: fontSize, Fill: fill, Shadow: shadow},
		Layout: PresetLayout{Anchor: "center", Alignment: "center", BoxWidth: 1920, BoxHeight: 160, Fit: "contain"},
		Motion: MotionDefinition{ID: anim, Unit: "glyph", Enter: 72, Exit: 6},
	}
}

// officialPresets is the only production catalog. Do not add parallel maps
// for families, animation or geometry.
var officialPresets = map[string]PresetDefinition{
	// Static text is intentionally part of the small smoke/E2E catalog: it
	// proves text/subtitle pixels without requiring an animation window longer
	// than a short canary composition.
	"static_text_smoke":                 makePreset("static_text_smoke", textSpec("safe_area", "center", "", "line", 0, 0, nil)),
	"caption_card":                      makePreset("caption_card", textSpec("center", "center", "fade_in", "line", 10, 8, nil)),
	"lower_third_safe":                  makePreset("lower_third_safe", textSpec("lower_third", "left", "focus_in", "line", 8, 6, nil)),
	"phrase_focus_v1":                   makePreset("phrase_focus_v1", textSpec("safe_area", "center", "focus_in", "line", 8, 6, nil)),
	"name_glow_typewriter":              makePreset("name_glow_typewriter", textSpec("lower_third", "left", "character_cascade", "glyph", 10, 6, glowStyle())),
	"name_glow_slide":                   makePreset("name_glow_slide", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, glowStyle())),
	"name_glow_pop":                     makePreset("name_glow_pop", textSpec("lower_third", "left", "fade_in", "line", 8, 6, glowStyle())),
	"name_fade_in":                      makePreset("name_fade_in", textSpec("lower_third", "left", "fade_in", "line", 8, 6, nil)),
	"name_slide_up":                     makePreset("name_slide_up", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, nil)),
	"name_pop_in":                       makePreset("name_pop_in", textSpec("lower_third", "left", "soft_pop", "line", 8, 6, nil)),
	"name_scale_in":                     makePreset("name_scale_in", textSpec("lower_third", "left", "scale_drop", "line", 8, 6, nil)),
	"name_slide_left":                   makePreset("name_slide_left", textSpec("lower_third", "left", "slide_in", "line", 8, 6, nil)),
	"fast_fade_through":                 makePreset("fast_fade_through", textSpec("safe_area", "center", "fade_in", "line", 4, 4, nil)),
	"clean_slide_up":                    makePreset("clean_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, nil)),
	"slide_lateral":                     makePreset("slide_lateral", textSpec("safe_area", "center", "slide_in", "line", 8, 6, nil)),
	"phrase_word_reveal":                makePreset("phrase_word_reveal", textSpec("safe_area", "center", "word_reveal", "word", 10, 6, nil)),
	"undertext_pop":                     makePreset("undertext_pop", textSpec("lower_third", "center", "fade_in", "line", 8, 6, nil)),
	"phrase_fade_in":                    makePreset("phrase_fade_in", textSpec("safe_area", "center", "fade_in", "line", 8, 6, nil)),
	"phrase_scale_in":                   makePreset("phrase_scale_in", textSpec("safe_area", "center", "scale_drop", "line", 8, 6, nil)),
	"phrase_slide_up":                   makePreset("phrase_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, nil)),
	"phrase_soft_pop":                   makePreset("phrase_soft_pop", textSpec("safe_area", "center", "soft_pop", "line", 8, 6, nil)),
	"snap_scale":                        makePreset("snap_scale", textSpec("safe_area", "center", "scale_drop", "line", 5, 4, nil)),
	"active_word_pop":                   makePreset("active_word_pop", textSpec("safe_area", "center", "word_reveal", "word", 8, 6, nil)),
	"kinetic_split_word":                makePhrasePreset("kinetic_split_word", "kinetic_split_word", "word"),
	"dynamic_island_expansion":          makePhrasePreset("dynamic_island_expansion", "dynamic_island_expansion", "line"),
	"masked_upward_reveal":              makePhrasePreset("masked_upward_reveal", "masked_upward_reveal", "line"),
	"staggered_char_float":              makePhrasePreset("staggered_char_float", "staggered_char_float", "glyph"),
	"high_specular_light_sweep":         makePhrasePreset("high_specular_light_sweep", "high_specular_light_sweep", "glyph"),
	"depth_of_field_rack_focus":         makePhrasePreset("depth_of_field_rack_focus", "depth_of_field_rack_focus", "glyph"),
	"micro_tracker_kerning_compression": makePhrasePreset("micro_tracker_kerning_compression", "micro_tracker_kerning_compression", "glyph"),
	"isometric_3d_fold":                 makePhrasePreset("isometric_3d_fold", "isometric_3d_fold", "glyph"),
	"soft_edge_spotlight_dissolve":      makePhrasePreset("soft_edge_spotlight_dissolve", "soft_edge_spotlight_dissolve", "glyph"),
	"chromatic_aberration_pop":          makePhrasePreset("chromatic_aberration_pop", "chromatic_aberration_pop", "glyph"),
	"fluid_gradient_text_flow":          makePhrasePreset("fluid_gradient_text_flow", "fluid_gradient_text_flow", "glyph"),
	"velocity_inertia_snap":             makePhrasePreset("velocity_inertia_snap", "velocity_inertia_snap", "line"),
	"vertical_rolling_counter":          makePhrasePreset("vertical_rolling_counter", "vertical_rolling_counter", "glyph"),
	"glassmorphism_card_tilt":           makePhrasePreset("glassmorphism_card_tilt", "glassmorphism_card_tilt", "glyph"),
	"pixel_grid_alpha_matrix":           makePhrasePreset("pixel_grid_alpha_matrix", "pixel_grid_alpha_matrix", "glyph"),
	"image_focus_in":                    makePreset("image_focus_in", imageSpec("image_right", "focus_in")),
	"image_fade_in":                     makePreset("image_fade_in", imageSpec("image_right", "fade_in")),
	"image_scale_in":                    makePreset("image_scale_in", imageSpec("image_right", "scale_drop")),
	"image_slide_left":                  makePreset("image_slide_left", imageSpec("image_left", "slide_in")),
	"image_slide_right":                 makePreset("image_slide_right", imageSpec("image_right", "slide_from_right")),
	"image_fast_fade":                   makePreset("image_fast_fade", imageSpec("image_right", "fade_in")),
	"modern_rounded_pop":                makePreset("modern_rounded_pop", imageSpec("image_right", "scale_drop")),
	"bottom_card_rise":                  makePreset("bottom_card_rise", imageSpec("bottom_right", "reveal_from_bottom")),
	"phrase_typewriter_clean":           makeTypewriterPreset("phrase_typewriter_clean", "typewriter_clean", 72, []float64{0.08, 0.08, 0.12, 1.0}, nil),
	"phrase_typewriter_pop":             makeTypewriterPreset("phrase_typewriter_pop", "typewriter_pop", 74, []float64{0.06, 0.08, 0.14, 1.0}, &StyleShadow{Color: "#000000", Opacity: 0.35, Blur: 8, Offset: []float64{0, 4}}),
	"phrase_typewriter_neon":            makeTypewriterPreset("phrase_typewriter_neon", "typewriter_neon", 70, []float64{0.01, 0.45, 0.65, 1.0}, &StyleShadow{Color: "#38BDF8", Opacity: 0.90, Blur: 22, Offset: []float64{0, 0}}),
	"phrase_typewriter_tracking":        makeTypewriterPreset("phrase_typewriter_tracking", "typewriter_tracking", 66, []float64{0.18, 0.22, 0.28, 1.0}, &StyleShadow{Color: "#000000", Opacity: 0.25, Blur: 10, Offset: []float64{0, 4}}),
	"phrase_typewriter_glitch":          makeTypewriterPreset("phrase_typewriter_glitch", "typewriter_glitch", 76, []float64{0.75, 0.12, 0.12, 1.0}, &StyleShadow{Color: "#EF4444", Opacity: 0.75, Blur: 16, Offset: []float64{0, 0}}),
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

// ResolveOfficialPreset returns the official definition for id, without a
// kind check. Certification tools use it to build fixtures from the same
// registry entry the runtime resolves.
func ResolveOfficialPreset(id string) (PresetDefinition, error) {
	id = strings.TrimSpace(id)
	d, ok := officialPresets[id]
	if !ok {
		return PresetDefinition{}, fmt.Errorf("overlay: unsupported RenderingGen official preset %q", id)
	}
	return d, nil
}

// ImagePresetRadius is the single authority for the modern_rounded_pop corner
// radius: 16% of the shorter side. Every path that needs the radius — the
// semantic compiler's applyPresetDefinition — delegates here so a future rule
// change is made in exactly one place.
func ImagePresetRadius(presetID string, width, height int) float64 {
	if presetID != "modern_rounded_pop" {
		return 0
	}
	if width > height {
		width = height
	}
	return float64(width) * 0.16
}

func resolveOfficialPreset(id, kind string) (PresetDefinition, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return PresetDefinition{}, nil
	}
	d, ok := officialPresets[id]
	if !ok {
		return PresetDefinition{}, fmt.Errorf("overlay: unsupported RenderingGen official preset %q", id)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != string(d.Family) {
		return PresetDefinition{}, fmt.Errorf("overlay: preset %q is %s, not %s", id, d.Family, kind)
	}
	return d, nil
}
