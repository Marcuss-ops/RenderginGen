package overlay

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"
)

const OfficialPresetVersion = "renderinggen-official-presets.v2"

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

// Kept as an internal compatibility alias while callers move to the grouped
// contract. It does not create a second registry or serialized schema.
type OfficialPresetDefinition = PresetDefinition

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

func makePreset(id string, s presetSpec) OfficialPresetDefinition {
	d := OfficialPresetDefinition{ID: id, Family: s.family,
		Style:  PresetStyle{FontFamily: "assets/fonts/Poppins-Bold.ttf", FontSize: 58, Fill: []float64{1, 1, 1, 1}, Shadow: s.shadow},
		Layout: PresetLayout{Anchor: s.anchor, Alignment: s.align, BoxWidth: s.boxW, BoxHeight: s.boxH, Fit: s.fit},
		Motion: MotionDefinition{Name: s.anim, Unit: s.unit, Enter: s.enter, Exit: s.exit}}
	if s.family == PresetImage {
		d.Style.FontFamily, d.Style.FontSize, d.Style.Fill = "", 0, nil
	}
	return d
}

// officialPresets is the only production catalog. Do not add parallel maps
// for families, animation or geometry.
var officialPresets = map[string]OfficialPresetDefinition{
	// Static text is intentionally part of the small smoke/E2E catalog: it
	// proves text/subtitle pixels without requiring an animation window longer
	// than a short canary composition.
	"static_text_smoke":    makePreset("static_text_smoke", textSpec("safe_area", "center", "", "line", 0, 0, nil)),
	"caption_card":         makePreset("caption_card", textSpec("center", "center", "fade_in", "line", 10, 8, nil)),
	"lower_third_safe":     makePreset("lower_third_safe", textSpec("lower_third", "left", "focus_in", "line", 8, 6, nil)),
	"phrase_focus_v1":      makePreset("phrase_focus_v1", textSpec("safe_area", "center", "focus_in", "line", 8, 6, nil)),
	"name_glow_typewriter": makePreset("name_glow_typewriter", textSpec("lower_third", "left", "character_cascade", "glyph", 10, 6, glowStyle())),
	"name_glow_slide":      makePreset("name_glow_slide", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, glowStyle())),
	"name_glow_pop":        makePreset("name_glow_pop", textSpec("lower_third", "left", "fade_in", "line", 8, 6, glowStyle())),
	"name_fade_in":         makePreset("name_fade_in", textSpec("lower_third", "left", "fade_in", "line", 8, 6, nil)),
	"name_slide_up":        makePreset("name_slide_up", textSpec("lower_third", "left", "reveal_from_bottom", "line", 8, 6, nil)),
	"name_pop_in":          makePreset("name_pop_in", textSpec("lower_third", "left", "soft_pop", "line", 8, 6, nil)),
	"name_scale_in":        makePreset("name_scale_in", textSpec("lower_third", "left", "scale_drop", "line", 8, 6, nil)),
	"name_slide_left":      makePreset("name_slide_left", textSpec("lower_third", "left", "slide_in", "line", 8, 6, nil)),
	"fast_fade_through":    makePreset("fast_fade_through", textSpec("safe_area", "center", "fade_in", "line", 4, 4, nil)),
	"clean_slide_up":       makePreset("clean_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, nil)),
	"slide_lateral":        makePreset("slide_lateral", textSpec("safe_area", "center", "slide_in", "line", 8, 6, nil)),
	"phrase_word_reveal":   makePreset("phrase_word_reveal", textSpec("safe_area", "center", "word_reveal", "word", 10, 6, nil)),
	"undertext_pop":        makePreset("undertext_pop", textSpec("lower_third", "center", "fade_in", "line", 8, 6, nil)),
	"phrase_fade_in":       makePreset("phrase_fade_in", textSpec("safe_area", "center", "fade_in", "line", 8, 6, nil)),
	"phrase_scale_in":      makePreset("phrase_scale_in", textSpec("safe_area", "center", "scale_drop", "line", 8, 6, nil)),
	"phrase_slide_up":      makePreset("phrase_slide_up", textSpec("safe_area", "center", "reveal_from_bottom", "line", 8, 6, nil)),
	"phrase_soft_pop":      makePreset("phrase_soft_pop", textSpec("safe_area", "center", "soft_pop", "line", 8, 6, nil)),
	"snap_scale":           makePreset("snap_scale", textSpec("safe_area", "center", "scale_drop", "line", 5, 4, nil)),
	"active_word_pop":      makePreset("active_word_pop", textSpec("safe_area", "center", "word_reveal", "word", 8, 6, nil)),
	"image_focus_in":       makePreset("image_focus_in", imageSpec("image_right", "focus_in")),
	"image_fade_in":        makePreset("image_fade_in", imageSpec("image_right", "fade_in")),
	"image_scale_in":       makePreset("image_scale_in", imageSpec("image_right", "scale_drop")),
	"image_slide_left":     makePreset("image_slide_left", imageSpec("image_left", "slide_in")),
	"image_slide_right":    makePreset("image_slide_right", imageSpec("image_right", "slide_from_right")),
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

// OfficialPresetRegistry is the read-only view used by certification tools.
// The production catalog remains private, so callers cannot create a second
// mutable source of truth.
type OfficialPresetRegistry struct{}

// OfficialPresets is the canonical registry handle.
var OfficialPresets OfficialPresetRegistry

// All returns a deterministic snapshot of every official definition.
func (OfficialPresetRegistry) All() []OfficialPresetDefinition {
	ids := officialPresetIDs()
	out := make([]OfficialPresetDefinition, 0, len(ids))
	for _, id := range ids {
		out = append(out, officialPresets[id])
	}
	return out
}

// Resolve resolves through the same production catalog used by the compiler.
func (OfficialPresetRegistry) Resolve(id string) (OfficialPresetDefinition, error) {
	return ResolveOfficialPreset(id)
}

// ResolveOfficialPreset returns the official definition for id, without a
// kind check. Certification tools use it to build fixtures from the same
// registry entry the runtime resolves.
func ResolveOfficialPreset(id string) (OfficialPresetDefinition, error) {
	id = strings.TrimSpace(id)
	d, ok := officialPresets[id]
	if !ok {
		return OfficialPresetDefinition{}, fmt.Errorf("overlay: unsupported RenderingGen official preset %q", id)
	}
	return d, nil
}

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
