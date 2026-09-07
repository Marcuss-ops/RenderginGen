// semantic_helpers.go owns the small lowering helpers shared by
// compileSemantic: motion compilation, preset validation and template
// classification. Preset choice and editorial decisions remain owned by
// PipelineGen — these helpers only validate and mechanically apply the plan.
package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"
)

func animationForMotion(id string, params map[string]any, textValue string, duration int64) (*LayerAnimation, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	tracks, err := plugin.Compile(motion.MotionContext{Text: textValue, DurationFrames: duration}, motion.MotionParams(params))
	if err != nil {
		return nil, fmt.Errorf("overlay: compile motion %q: %w", id, err)
	}
	animation := &LayerAnimation{Tracks: fromMotionTracks(tracks)}
	if textPlugin, ok := plugin.(motion.TextMotionPlugin); ok {
		textDefinitions, err := textPlugin.CompileText(
			motion.MotionContext{Text: textValue, DurationFrames: duration},
			motion.MotionParams(params))
		if err != nil {
			return nil, fmt.Errorf("compile text motion %q: %w", id, err)
		}
		animation.TextAnimators = fromTextMotionDefinitions(textDefinitions, duration)
		if err := validateTextMotion(animation.TextAnimators, id); err != nil {
			return nil, err
		}
	}
	return animation, nil
}

func validateTextMotion(animators []TextAnimator, id string) error {
	for _, animator := range animators {
		if len(animator.Selectors) == 0 || len(animator.Properties) == 0 {
			return fmt.Errorf("overlay: motion %q collapsed to an empty text animator", id)
		}
		nonOpacity := false
		for _, track := range animator.Properties {
			if track.Property != "opacity" {
				nonOpacity = true
				break
			}
		}
		selector := animator.Selectors[0]
		// An opacity-only animator is valid when it has a real per-element
		// selector sweep (for example opacity_wave). A layer selector without
		// that sweep is the legacy whole-layer fade we reject.
		perElementSweep := selector.Unit != "layer" && (selector.Start != nil || selector.End != nil)
		if !nonOpacity && !perElementSweep {
			return fmt.Errorf("overlay: motion %q collapsed to layer fade", id)
		}
	}
	return nil
}

func msFrames(start, end, fpsNum, fpsDen int64) (int64, int64) {
	return int64(math.Floor(float64(start) * float64(fpsNum) / float64(fpsDen) / 1000)),
		int64(math.Ceil(float64(end) * float64(fpsNum) / float64(fpsDen) / 1000))
}

// presetRequiredTemplates are semantic templates that must carry an official
// preset. RenderingGen validates the supplied opaque id, while PipelineGen
// remains responsible for editorial selection.
var presetRequiredTemplates = map[string]bool{
	"IMPORTANT_PHRASE": true,
	"IMPORTANT_WORD":   true,
	"NUMBER":           true,
	"QUOTE":            true,
	"CONCEPT":          true,
	"MONEY":            true,
	"PERCENT":          true,
	"PERSON":           true,
	"ORGANIZATION":     true,
	"LOCATION":         true,
	"IMAGE_OVERLAY":    true,
}

func presetFor(item semanticItem) (string, error) {
	// The plan's preset_id contract slot is the only spelling. RenderingGen's
	// official catalog is authoritative.
	//
	// ADR-029 forward-point (d): RenderingGen is an execution worker and must
	// NOT re-map a template_id to a preset (e.g. it must not know that PERSON
	// means lower_third_safe). The semantic_role → preset_id decision lives
	// only in PipelineGen's SemanticOverlayResolver. A preset-driven template
	// that does not carry a preset_id is rejected; preset-less primitives
	// (PRODUCT, LOGO, LIGHT_LEAK, …) legitimately compile without one.
	p := strings.TrimSpace(item.PresetID)
	if p != "" {
		family := "text"
		if isImageTemplate(item.Template) {
			family = "image"
		}
		return validatePreset(p, item.ID, family)
	}
	if presetRequiredTemplates[strings.ToUpper(item.Template)] {
		return "", fmt.Errorf("overlay: item %q requires preset_id (resolved by PipelineGen's SemanticOverlayResolver)", item.ID)
	}
	return "", nil
}
func validatePreset(p, id, kind string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("overlay: empty preset_id for item %q", id)
	}
	d, err := resolveOfficialPreset(p, kind)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}
func isImageTemplate(t string) bool {
	switch strings.ToUpper(t) {
	case "IMAGE_OVERLAY", "PRODUCT", "LOGO", "LIGHT_LEAK":
		return true
	}
	return false
}
func isEntityTemplate(t string) bool {
	switch strings.ToUpper(t) {
	case "PERSON", "PERSON_DEFAULT", "ORGANIZATION", "ORGANIZATION_DEFAULT", "LOCATION", "LOCATION_DEFAULT", "GPE_DEFAULT":
		return true
	}
	return false
}

// entityRefText returns the display text from the plan's entity_ref block:
// surface_text first, then the canonical name.
// The compiler never invents a name — an empty ref yields empty text.
func entityRefText(item semanticItem) string {
	if item.EntityRef == nil {
		return ""
	}
	if strings.TrimSpace(item.EntityRef.SurfaceText) != "" {
		return item.EntityRef.SurfaceText
	}
	if strings.TrimSpace(item.EntityRef.Name) != "" {
		return item.EntityRef.Name
	}
	return ""
}

func imageLayer(item semanticItem, start, end int64, asset string, params map[string]any) Layer {
	w, h := intParam(params, "width", 320), intParam(params, "height", 320)
	return Layer{ID: item.ID + "_image", Type: "image", Asset: asset, BoxWidth: w, BoxHeight: h, Size: []float64{float64(w), float64(h)}, Fit: stringParam(params, "fit", "contain"), Radius: float64(intParam(params, "radius", 0)), StartFrame: start, DurationFrames: end - start}
}

func applyPresetDefinition(layer *Layer, d OfficialPresetDefinition) {
	if d.Family == PresetImage {
		if layer.BoxWidth == 320 && d.Layout.BoxWidth > 0 {
			layer.BoxWidth = d.Layout.BoxWidth
		}
		if layer.BoxHeight == 320 && d.Layout.BoxHeight > 0 {
			layer.BoxHeight = d.Layout.BoxHeight
		}
		if layer.Fit == "contain" && d.Layout.Fit != "" {
			layer.Fit = d.Layout.Fit
		}
		if d.ID == "modern_rounded_pop" && layer.Radius <= 0 {
			layer.Radius = float64(min(layer.BoxWidth, layer.BoxHeight)) * 0.16
		}
		return
	}
	font := d.Style.FontFamily
	if layer.Style != nil && layer.Style.Font != "" {
		// Prepared fixtures may use a workspace-relative alias (for example
		// fonts/Poppins-Bold.ttf). Preserve it while still applying the
		// catalog-owned visual properties.
		font = layer.Style.Font
	}
	layer.Style = &LayerStyle{Font: font, FontSize: d.Style.FontSize, Fill: rgbaHex(d.Style.Fill)}
	if d.Style.Shadow != nil {
		s := d.Style.Shadow
		layer.Style.Shadow = &LayerShadow{Color: s.Color, Opacity: s.Opacity, Blur: s.Blur, Offset: append([]float64(nil), s.Offset...)}
	}
}

func animationForDefinition(d OfficialPresetDefinition) (*LayerAnimation, error) {
	if d.Motion.Name == "" && d.Motion.ID == "" {
		return nil, nil
	}
	tracks, err := tracksForMotion(d.Motion)
	if err != nil {
		return nil, err
	}
	return &LayerAnimation{Tracks: tracks}, nil
}

func rgbaHex(v []float64) string {
	if len(v) != 4 {
		return ""
	}
	// Chronon render-plan.v2 uses opaque six-digit CSS colors. Alpha is
	// carried by layer/style opacity fields, never embedded in the color.
	return fmt.Sprintf("#%02X%02X%02X", int(v[0]*255), int(v[1]*255), int(v[2]*255))
}

func intParam(p map[string]any, key string, fallback int) int {
	if n, ok := p[key].(float64); ok {
		return int(n)
	}
	return fallback
}
func stringParam(p map[string]any, key, fallback string) string {
	if s, ok := p[key].(string); ok && s != "" {
		return s
	}
	return fallback
}
