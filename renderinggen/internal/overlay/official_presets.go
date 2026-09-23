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
	Stroke     *StyleStroke
	Shadow     *StyleShadow
	Glow       *StyleGlow
}

type StyleStroke struct {
	Color string
	Width float64
}

type StyleShadow struct {
	Color   string
	Opacity float64
	Blur    float64
	Offset  []float64
}

// StyleGlow mirrors Chronon's single glow contract: radius, intensity and tint.
type StyleGlow struct {
	Radius    float64
	Intensity float64
	Color     string
}

// canaryGlow provides one restrained neutral halo; callers may change its
// three visible controls but cannot select a quality path or stacked lobes.
func canaryGlow() *StyleGlow {
	return &StyleGlow{Radius: 42, Intensity: 0.25, Color: "#FFFFFF"}
}

type PresetLayout struct {
	Anchor    string
	Alignment string
	BoxWidth  int
	BoxHeight int
	Fit       string
}

type MotionDefinition = motion.MotionDefinition

// runtimeFontPath resolves the closed set of bundled font-family IDs accepted
// in overlay item params. Paths are fixed workspace assets, never paths
// supplied by a plan.
func runtimeFontPath(family string) (string, bool) {
	switch family {
	case "poppins":
		return "assets/fonts/Poppins-Bold.ttf", true
	case "inter":
		return "assets/fonts/Inter-Bold.ttf", true
	case "dejavu_sans":
		return officialCyrillicFontPath, true
	default:
		return "", false
	}
}

// presetSpec is the family-agnostic authoring row the per-family builders
// (official_presets_text.go / official_presets_image.go) fill in. It exists so
// a family can state its geometry and timing without the shared spine below
// growing a knob per family.
type presetSpec struct {
	family                    PresetFamily
	anchor, align, anim, unit string
	enter, exit               int
	shadow                    *StyleShadow
	glow                      *StyleGlow
	boxW, boxH                int
	fit                       string
}

// makePreset is the shared lowering from an authoring row to a
// PresetDefinition: family defaults first, then the family-specific rules that
// make a text preset legible (stroke + shadow) or an image preset invisible as
// type (no font, no fill).
func makePreset(id string, s presetSpec) PresetDefinition {
	d := PresetDefinition{ID: id, Family: s.family,
		Style:  PresetStyle{FontFamily: officialFontPath, FontSize: 58, Fill: []float64{1, 1, 1, 1}, Shadow: s.shadow, Glow: s.glow},
		Layout: PresetLayout{Anchor: s.anchor, Alignment: s.align, BoxWidth: s.boxW, BoxHeight: s.boxH, Fit: s.fit},
		Motion: MotionDefinition{ID: s.anim, Unit: s.unit, Enter: s.enter, Exit: s.exit}}
	if s.family == PresetImage {
		d.Style.FontFamily, d.Style.FontSize, d.Style.Fill = "", 0, nil
	} else if id != StaticTextSmokePresetID {
		if d.Style.Shadow == nil {
			d.Style.Shadow = &StyleShadow{Color: "#000000", Opacity: 0.72, Blur: 12, Offset: []float64{0, 4}}
		}
		d.Style.Stroke = &StyleStroke{Color: "#111827", Width: 3.5}
	}
	return d
}

// officialPresets is the only production catalog. Do not add parallel maps
// for families, animation or geometry: the text and image entries are authored
// in their own files but merged into THIS single map, so a lookup can never
// depend on which family file happened to be consulted.
var officialPresets = map[string]PresetDefinition{
	// Static text remains a small smoke/E2E preset beside the sole phrase style.
	StaticTextSmokePresetID: makePreset(StaticTextSmokePresetID, staticTextSmokeSpec()),
	PhraseDefaultPresetID:   phraseDefaultPreset(),

	"image_focus_in":     makePreset("image_focus_in", imageSpec("image_right", "image_focus_reveal")),
	"image_fade_in":      makePreset("image_fade_in", imageSpec("image_right", "image_fade_reveal")),
	"image_scale_in":     makePreset("image_scale_in", imageSpec("image_right", "image_scale_reveal")),
	"image_slide_left":   makePreset("image_slide_left", imageSpec("image_left", "image_slide_left_reveal")),
	"image_slide_right":  makePreset("image_slide_right", imageSpec("image_right", "image_slide_right_reveal")),
	"image_fast_fade":    makePreset("image_fast_fade", imageFastSpec("image_right", "fade_in")),
	"modern_rounded_pop": makePreset("modern_rounded_pop", imageSpec("image_right", "scale_drop")),
	"bottom_card_rise":   makePreset("bottom_card_rise", imageSpec("bottom_right", "reveal_from_bottom")),
}

// ValidateCatalogParity fails closed when the preset registry this package owns
// and the preset families the ChrononTemplate catalog declares disagree.
//
// The split is deliberate: the catalog owns WHICH presets exist (ids, and which
// family each belongs to), this package owns HOW each one renders. That leaves
// exactly one failure mode — the two lists drifting apart, so a preset the
// catalog advertises has no definition here (and resolves to an error at claim
// time), or a definition here is unreachable because nothing asks for it. Both
// are build-ordering mistakes, so the worker checks this before it reports
// ready.
func ValidateCatalogParity() error {
	for _, family := range []PresetFamily{PresetText, PresetImage} {
		declared := make(map[string]bool)
		for _, id := range motion.OverlayPresetIDs(string(family)) {
			declared[id] = true
		}
		if len(declared) == 0 {
			return fmt.Errorf("overlay: the embedded ChrononTemplate catalog declares no %s preset family", family)
		}
		for _, d := range officialPresets {
			if d.Family != family {
				continue
			}
			if !declared[d.ID] {
				return fmt.Errorf("overlay: %s preset %q is defined here but not declared by the ChrononTemplate catalog", family, d.ID)
			}
		}
		for id := range declared {
			if _, ok := officialPresets[id]; !ok {
				return fmt.Errorf("overlay: the ChrononTemplate catalog declares %s preset %q, which this build cannot render", family, id)
			}
		}
	}

	imageMotions := motion.Registry.Image25DCleanV1MotionIDs()
	if len(imageMotions) != 8 {
		return fmt.Errorf("overlay: the ChrononTemplate catalog declares %d image_25d_clean_v1 motions, expected 8 layer-only motions", len(imageMotions))
	}
	for _, id := range imageMotions {
		plugin, err := motion.Registry.Resolve(id)
		if err != nil {
			return fmt.Errorf("overlay: image motion %q is not registered: %w", id, err)
		}
		declarative, ok := plugin.(motion.DeclarativePlugin)
		if !ok {
			return fmt.Errorf("overlay: image motion %q is not catalog-declarative", id)
		}
		definition := declarative.Definition
		if definition.Unit != "layer" || definition.Enter < 42 || definition.Enter > 55 ||
			definition.Exit != 12 || len(definition.Tracks) == 0 || len(definition.TextAnimators) != 0 {
			return fmt.Errorf("overlay: image motion %q violates the clean 2.5D layer contract", id)
		}
		imageTarget := false
		for _, target := range definition.Targets {
			imageTarget = imageTarget || target == "image"
		}
		if !imageTarget {
			return fmt.Errorf("overlay: image motion %q does not declare the image target", id)
		}
	}
	return nil
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

// ImagePresetRadius is the single authority for the canonical image corner
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
