// semantic_helpers.go owns the small lowering helpers shared by
// compileSemantic: motion compilation, preset validation and template
// classification. Preset choice and editorial decisions remain owned by
// PipelineGen — these helpers only validate and mechanically apply the plan.
package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// animationForMotion lowers a producer-selected motion_id through the shared
// spine (see lowerMotion). Semantic clips hold their resolved pose until the
// clip boundary; an exit is opt-in through motion_params.exit_frames.
func animationForMotion(id string, params map[string]any, textValue string, duration int64, presetExit int, phraseFloor ...bool) (*LayerAnimation, error) {
	isPhrase := len(phraseFloor) > 0 && phraseFloor[0]
	return animationForMotionTarget(id, params, textValue, duration, presetExit, "", "", isPhrase)
}

func animationForMotionTarget(id string, params map[string]any, textValue string, duration int64, presetExit int, itemID, target string, phraseFloor bool) (*LayerAnimation, error) {
	resolved, err := resolveRegisteredMotion(id)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q motion %q: %w", itemID, id, err)
	}
	return animationForResolvedMotionTarget(id, resolved, params, textValue, duration, presetExit, itemID, target, phraseFloor)
}

func animationForResolvedMotionTarget(id string, resolved *resolvedMotion, params map[string]any, textValue string, duration int64, presetExit int, itemID, target string, phraseFloor bool) (*LayerAnimation, error) {
	enter, exit, err := motionWindows(params, presetExit)
	if err != nil {
		return nil, err
	}
	animation, err := lowerResolvedMotion(id, resolved, enter, exit, motion.MotionParams(params), textValue, duration, phraseFloor, itemID, target)
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
// animation contract. Values are frame counts at the plan's frame rate. Zero
// uses the catalog entrance default; an omitted exit_frames keeps the preset or
// catalog exit, while explicit zero disables it. Positive overrides must be
// integers in [1, 240].
func motionWindows(params map[string]any, presetExit int) (enter, exit int, err error) {
	// The preset/catalog owns the default exit window. A producer can override
	// it explicitly, and zero disables the exit without dropping the default
	// value when the field is omitted.
	exit = presetExit
	if params == nil {
		return 0, exit, nil
	}
	enter, err = motionFrameParam(params, "enter_frames")
	if err != nil {
		return 0, 0, err
	}
	if exitOverride, err := motionFrameParam(params, "exit_frames"); err != nil {
		return 0, 0, err
	} else if _, present := params["exit_frames"]; present {
		if exitOverride > 0 {
			exit = exitOverride
		} else {
			exit = -1
		}
	}
	return enter, exit, nil
}

func motionFrameParam(params map[string]any, name string) (int, error) {
	value, present := params[name]
	if !present {
		return 0, nil
	}
	frames, err := toFloat(value)
	if err != nil || !finite(frames) || math.Trunc(frames) != frames || frames < 0 || frames > 240 {
		return 0, fmt.Errorf("overlay: motion_params.%s must be an integer in [0, 240] (zero keeps the catalog/preset value)", name)
	}
	return int(frames), nil
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

func itemFrames(item semanticItem, fpsNum, fpsDen int64) (int64, int64) {
	if item.StartUS != nil && item.DurationUS != nil {
		return usFrames(*item.StartUS, *item.StartUS+*item.DurationUS, fpsNum, fpsDen)
	}
	return msFrames(item.StartMS, item.EndMS, fpsNum, fpsDen)
}

// itemWindowUS returns the item's canonical [start, end) window in
// microseconds. Canonical start_us/duration_us wins; legacy millisecond items
// are projected at 1000us/ms so every downstream calculation reads one unit
// instead of the rounded millisecond mirror.
func itemWindowUS(item semanticItem) (int64, int64) {
	if item.StartUS != nil && item.DurationUS != nil {
		return *item.StartUS, *item.StartUS + *item.DurationUS
	}
	return item.StartMS * 1000, item.EndMS * 1000
}

// imageLayerWindowUS returns a child layer's declared window in microseconds,
// relative to its parent item start. Child image layers only carry
// milliseconds, so this is the single projection point for every recipe.
func imageLayerWindowUS(layer SemanticImageLayer) (int64, int64) {
	return layer.StartMS * 1000, layer.EndMS * 1000
}

// imageLayerFrames converts a child layer window to frames relative to its
// parent item start.
func imageLayerFrames(layer SemanticImageLayer, fpsNum, fpsDen int64) (int64, int64) {
	startUS, endUS := imageLayerWindowUS(layer)
	return usFrames(startUS, endUS, fpsNum, fpsDen)
}

func usFrames(startUS, endUS, fpsNum, fpsDen int64) (int64, int64) {
	frameAtUS := func(us int64) int64 {
		return int64(math.Round(float64(us) * float64(fpsNum) / float64(fpsDen) / 1_000_000))
	}
	return frameAtUS(startUS), frameAtUS(endUS)
}

func msFrames(start, end, fpsNum, fpsDen int64) (int64, int64) {
	// Millisecond endpoints are quantized independently to the nearest frame
	// boundary. Rounding the start down and the end up expands every non-aligned
	// interval; at 24 fps that can add a frame to many otherwise correctly sized
	// overlay assets, leaving a packet-copy assembler to repeat or decode a bad
	// tail frame. The wire interval is half-open [start, end), so nearest-frame
	// boundaries preserve the requested duration without a fixed frame count.
	frameAtMS := func(ms int64) int64 {
		return int64(math.Round(float64(ms) * float64(fpsNum) / float64(fpsDen) / 1000))
	}
	return frameAtMS(start), frameAtMS(end)
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
		if spec.Kind == KindShape && (p == "shape_static" || p == "none") {
			return p, nil
		}
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
	// 0 means "not authored": the preset may supply the box, and only after
	// that does defaultImageBox apply the 320 fallback. A 320 sentinel here
	// would make the certified penta 320px strip indistinguishable from an
	// absent width and silently inflate it to the preset's 480px box.
	w, h := intParam(ri.Params, "width", 0), intParam(ri.Params, "height", 0)
	return Layer{ID: imageLayerID(ri.Item.ID), Type: "image", Asset: asset, BoxWidth: w, BoxHeight: h, Size: []float64{float64(w), float64(h)}, Fit: stringParam(ri.Params, "fit", "contain"), Radius: float64(intParam(ri.Params, "radius", 0)), StartFrame: ri.Start, DurationFrames: ri.End - ri.Start}
}

// defaultImageBox applies the fallback card box only when neither the item nor
// its preset declared one, then keeps the serialized Size in sync with the
// compiler-only Box fields.
func defaultImageBox(layer *Layer) {
	if layer.BoxWidth <= 0 {
		layer.BoxWidth = 320
	}
	if layer.BoxHeight <= 0 {
		layer.BoxHeight = 320
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
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
		if layer.BoxWidth <= 0 && d.Layout.BoxWidth > 0 {
			layer.BoxWidth = d.Layout.BoxWidth
		}
		if layer.BoxHeight <= 0 && d.Layout.BoxHeight > 0 {
			layer.BoxHeight = d.Layout.BoxHeight
		}
		// BoxWidth/BoxHeight are compiler-only layout inputs, while Size is
		// the serialized Chronon geometry. Keep them synchronized when an
		// official image preset supplies its dimensions; otherwise Chronon
		// receives the stale zero/unset box and the image card geometry can
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
	if d.Style.UsePresetFont {
		if resolved, ok := runtimeFontPath(d.Style.FontFamily); ok {
			font = resolved
		}
	}
	if layer.Style != nil && layer.Style.Font != "" {
		// Prepared fixtures may use a workspace-relative alias (for example
		// fonts/Poppins-Bold.ttf). Preserve it while still applying the
		// catalog-owned visual properties.
		font = layer.Style.Font
	}
	layer.Style = &LayerStyle{
		Font: font, FontSize: d.Style.FontSize, Fill: rgbaHex(d.Style.Fill),
		FitMode: d.Style.TextFitMode, MinFontSize: d.Style.MinFontSize, MaxFontSize: d.Style.MaxFontSize,
	}
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
			Radius: g.Radius, Intensity: g.Intensity, Color: g.Color,
		}
	}
	if d.Style.Background != nil && layer.Style.Background == nil {
		bg := *d.Style.Background
		bg.Padding = append([]float64(nil), d.Style.Background.Padding...)
		layer.Style.Background = &bg
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
