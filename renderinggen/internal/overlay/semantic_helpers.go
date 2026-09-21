// semantic_helpers.go owns the small lowering helpers shared by
// compileSemantic: motion compilation, preset validation and template
// classification. Preset choice and editorial decisions remain owned by
// PipelineGen — these helpers only validate and mechanically apply the plan.
package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// animationForMotion lowers a producer-selected motion_id through the shared
// spine (see lowerMotion). presetExit is the selected preset's own exit window:
// a producer that overrides only the entrance still gets an out instead of a
// hard cut. The motion's registered windows fill in whatever the caller omits.
func animationForMotion(id string, params map[string]any, textValue string, duration int64, presetExit int) (*LayerAnimation, error) {
	enter, exit := motionWindows(params, presetExit)
	animation, err := lowerMotion(id, enter, exit, motion.MotionParams(params), textValue, duration)
	if err != nil {
		return nil, err
	}
	if animation == nil {
		return nil, nil
	}
	if err := validateTextMotion(animation.TextAnimators, id); err != nil {
		return nil, err
	}
	return animation, nil
}

// motionWindows is deliberately small and bounded: producers may tune the
// readable timing window, but they cannot use motion_params to inject a new
// animation contract. Values are frame counts at the plan's frame rate.
func motionWindows(params map[string]any, presetExit int) (enter, exit int) {
	exit = presetExit
	if params == nil {
		return 0, exit
	}
	if value, ok := positiveMotionFrameParam(params["enter_frames"]); ok {
		enter = value
	}
	if value, ok := positiveMotionFrameParam(params["exit_frames"]); ok {
		exit = value
	}
	return enter, exit
}

func positiveMotionFrameParam(value any) (int, bool) {
	var frames int
	switch v := value.(type) {
	case float64:
		frames = int(v)
	case float32:
		frames = int(v)
	case int:
		frames = v
	case int64:
		frames = int(v)
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		frames = int(parsed)
	default:
		return 0, false
	}
	if frames < 1 || frames > 240 {
		return 0, false
	}
	return frames, true
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

func presetFor(item semanticItem, spec TemplateSpec) (string, error) {
	// The plan's preset_id contract slot is the only spelling. RenderingGen's
	// official catalog is authoritative, and the template's preset requirement
	// and family come from the single registry (registry.go).
	//
	// ADR-029 forward-point (d): RenderingGen is an execution worker and must
	// NOT re-map a template_id to a preset. The semantic_role → preset_id decision lives
	// only in PipelineGen's SemanticOverlayResolver. A preset-driven template
	// that does not carry a preset_id is rejected; preset-less primitives
	// (PRODUCT, LOGO, LIGHT_LEAK, …) legitimately compile without one.
	p := strings.TrimSpace(item.PresetID)
	if p != "" {
		family := string(spec.Family)
		if family == "" {
			family = string(PresetText)
		}
		return validatePreset(p, item.ID, family)
	}
	if spec.RequiresPreset {
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

// imageLayer is the single owner of an image layer's identity and geometry. It
// derives the layer id from the item id ("<id>:image"), including for the
// image-only entity path.
func imageLayer(ri resolvedItem, asset string) Layer {
	w, h := intParam(ri.Params, "width", 320), intParam(ri.Params, "height", 320)
	return Layer{ID: imageLayerID(ri.Item.ID), Type: "image", Asset: asset, BoxWidth: w, BoxHeight: h, Size: []float64{float64(w), float64(h)}, Fit: stringParam(ri.Params, "fit", "contain"), Radius: float64(intParam(ri.Params, "radius", 0)), StartFrame: ri.Start, DurationFrames: ri.End - ri.Start}
}

// imageLayerID / overlayLayerID are the only spellings of an item's layer ids.
// Distinct suffixes keep a video overlay from colliding with an image layer of
// the same item id. A `:text` spelling was declared beside them and referenced
// by no compiler (the text layer carries the item id itself, see
// compileItem's text branch), so it was deleted instead of left as a third id
// shape every reader has to consider and no writer emits.
func imageLayerID(itemID string) string   { return itemID + ":image" }
func overlayLayerID(itemID string) string { return itemID + ":overlay" }

func applyPresetDefinition(layer *Layer, d PresetDefinition) {
	if d.Family == PresetImage {
		if layer.BoxWidth == 320 && d.Layout.BoxWidth > 0 {
			layer.BoxWidth = d.Layout.BoxWidth
		}
		if layer.BoxHeight == 320 && d.Layout.BoxHeight > 0 {
			layer.BoxHeight = d.Layout.BoxHeight
		}
		// BoxWidth/BoxHeight are compiler-only layout inputs, while Size is
		// the serialized Chronon geometry. Keep them synchronized when an
		// official image preset supplies its dimensions; otherwise Chronon
		// receives the stale 320x320 fallback and the image card geometry can
		// diverge from the resolved layout.
		if len(layer.Size) >= 2 {
			layer.Size[0] = float64(layer.BoxWidth)
			layer.Size[1] = float64(layer.BoxHeight)
		}
		if layer.Fit == "contain" && d.Layout.Fit != "" {
			layer.Fit = d.Layout.Fit
		}
		if layer.Radius <= 0 {
			layer.Radius = ImagePresetRadius(d.ID, layer.BoxWidth, layer.BoxHeight)
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
	if d.Style.Stroke != nil {
		stroke := d.Style.Stroke
		layer.Style.Stroke = &LayerStroke{Color: stroke.Color, Width: stroke.Width}
	}
	if d.Style.Shadow != nil {
		s := d.Style.Shadow
		layer.Style.Shadow = &LayerShadow{Color: s.Color, Opacity: s.Opacity, Blur: s.Blur, Offset: append([]float64(nil), s.Offset...)}
	}
	// The preset's halo travels as the wire glow block Chronon's decoder reads
	// (render_plan_decoder_layer.cpp). Never overwrite a producer-declared
	// glow: the plan owns the explicit override, the preset is the default.
	if d.Style.Glow != nil && layer.Style.Glow == nil {
		g := d.Style.Glow
		layer.Style.Glow = &LayerGlow{
			Radius: g.Radius, Intensity: g.Intensity, Threshold: g.Threshold,
			Falloff: g.Falloff, CoreStrength: g.CoreStrength,
			AuraStrength: g.AuraStrength, BloomStrength: g.BloomStrength,
			HighQuality: g.HighQuality,
		}
	}
}

// animationForDefinition lowers a preset's motion without a concrete layer
// duration: the entrance is used exactly as authored and no exit window is
// emitted. Callers that know the layer duration use animationForPreset, which
// is the same lowering with both windows resolved against that duration.
func animationForDefinition(d PresetDefinition) (*LayerAnimation, error) {
	return animationForPreset(d, "", 0)
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
