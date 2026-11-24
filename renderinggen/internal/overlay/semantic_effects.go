package overlay

import (
	"fmt"
	"math"
	"strings"
)

// editorialImageLook maps the two selectable archival treatments to existing
// renderer effects. These are image-only stacks, not full-frame effects.
func editorialImageLook(name string) ([]LayerEffect, bool) {
	stack := map[string][]LayerEffect{
		"archive_print": {
			{Type: "catalog", Params: map[string]any{"effect_id": "color.saturation", "params": []map[string]any{{"param": "value", "value": 0.0}}}},
			{Type: "noise", Params: map[string]any{"amount": 0.055, "size": 1.4, "seed": 1942, "animated": false, "color_mode": "monochrome"}},
			{Type: "vignette", Params: map[string]any{"amount": 0.13, "radius": 0.82, "softness": 0.55}},
		},
		"vintage_film_projection": {
			{Type: "catalog", Params: map[string]any{"effect_id": "color.saturation", "params": []map[string]any{{"param": "value", "value": 0.72}}}},
			{Type: "noise", Params: map[string]any{"amount": 0.045, "size": 1.2, "seed": 1924, "animated": true, "color_mode": "monochrome"}},
			{Type: "vignette", Params: map[string]any{"amount": 0.2, "radius": 0.78, "softness": 0.62}},
			{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 0.8, "blue_offset": -0.8}},
		},
	}
	result, ok := stack[name]
	return result, ok
}

// blackAndWhiteEffect lowers the public black_and_white type to Chronon's
// native color.saturation catalog effect. Both surfaces (clip adjustment stack
// and image/shape layer stack) call this one helper, so a clip request and an
// image look cannot disagree on what "black and white" means. amount 1.0 is
// full desaturation, 0.0 leaves the frame untouched.
func blackAndWhiteEffect(amount float64) LayerEffect {
	return LayerEffect{Type: "catalog", Params: map[string]any{
		"effect_id": "color.saturation",
		"params":    []map[string]any{{"param": "value", "value": 1 - amount}},
	}}
}

// scanlineReferenceHeightPx is the frame height the public scanlines wavelength
// is authored against. The public field is normalized (bounds 0.002..1), while
// Chronon's native scanline pitch is in pixels; both lowering surfaces use this
// one reference so a clip preset and an image effect cannot disagree on the
// look. A 1080p plan therefore gets the authored pitch verbatim.
const scanlineReferenceHeightPx = 1080.0

// scanlinesEffect lowers the public scanlines pseudo-type to Chronon's native
// scanline modulation (a per-axis triangle wave over RGB). The old wave-warp
// lowering passed a normalized amount as an amplitude in pixels, which moved
// the image by a fraction of a pixel and survived as a no-op.
func scanlinesEffect(params map[string]any) LayerEffect {
	amount, _ := toFloat(params["amount"])
	wavelength, _ := toFloat(params["wavelength"])
	pitch := math.Round(wavelength * scanlineReferenceHeightPx)
	pitch = math.Max(1, math.Min(64, pitch))
	return LayerEffect{Type: "scanlines", Params: map[string]any{
		"axis":       "horizontal",
		"spacing_px": pitch,
		"amount":     amount,
		"phase":      0.0,
	}}
}

// resolveClipEffects lowers the clip-level adjustment stack. Parameter names,
// bounds, required fields and surface availability all come from the shared
// effect catalog; this function only decides how a public type becomes
// Chronon primitives (glitch fans out into three effects, scanlines become the
// renderer's native scanline modulation, and so on).
func resolveClipEffects(raw []semanticClipEffect) ([]LayerEffect, error) {
	out := make([]LayerEffect, 0, len(raw))
	for i, spec := range raw {
		typ := spec.Type
		if typ == "" {
			return nil, fmt.Errorf("overlay: clip effect %d requires a type", i)
		}
		definition, ok := effectDefinitionFor(typ, effectSurfaceClip)
		if !ok {
			return nil, fmt.Errorf("overlay: unsupported clip effect type %q", typ)
		}
		params := clipEffectParams(spec)
		for key := range params {
			if _, accepted := definition.params[key]; !accepted {
				return nil, fmt.Errorf("overlay: clip effect %s does not accept %s", typ, key)
			}
		}
		for _, key := range definition.clipRequired {
			if _, present := params[key]; !present {
				return nil, fmt.Errorf("overlay: clip effect %s requires %s", typ, key)
			}
		}
		if err := validateEffectParams(definition, typ, params, "clip effect"); err != nil {
			return nil, err
		}
		switch typ {
		case "glitch":
			amount := params["amount"].(float64)
			seed := 0
			if value, present := params["seed"].(int); present {
				seed = value
			}
			out = append(out, LayerEffect{Type: "wave_warp", Params: map[string]any{"amplitude": 0.025 * amount, "wavelength": 0.035, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}})
			out = append(out, LayerEffect{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 8 * amount, "blue_offset": -8 * amount}})
			out = append(out, LayerEffect{Type: "noise", Params: map[string]any{"amount": 0.12 * amount, "seed": seed, "animated": true, "color_mode": "rgb", "size": 1.0}})
		case "scanlines":
			out = append(out, scanlinesEffect(params))
		case "chromatic_aberration":
			amount := params["amount"].(float64)
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"red_offset": amount, "blue_offset": -amount}})
		case "bloom":
			threshold := 0.8
			if value, present := params["threshold"].(float64); present {
				threshold = value
			}
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"radius": params["radius"], "intensity": params["intensity"], "threshold": threshold}})
		case "lens_distortion":
			scale := 1.0
			if value, present := params["scale"].(float64); present {
				scale = value
			}
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"curvature": params["curvature"], "scale": scale}})
		case "glow":
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"radius": params["radius"], "intensity": params["intensity"]}})
		case "black_and_white":
			out = append(out, blackAndWhiteEffect(params["amount"].(float64)))
		case "banded_grain":
			speed := 0.0
			if value, present := params["speed"].(float64); present {
				speed = value
			}
			seed := 0
			if value, present := params["seed"].(int); present {
				seed = value
			}
			intensity, _ := params["intensity"].(float64)
			// Chronon has no banded_grain decoder. Lower this visual preset to
			// its native monochrome Noise effect so the request is renderable.
			out = append(out, LayerEffect{Type: "noise", Params: map[string]any{
				"amount": math.Min(1, intensity/4), "size": 1.0, "seed": seed,
				"animated": speed > 0, "color_mode": "monochrome",
			}})
		case "sharpen":
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"amount": params["amount"]}})
		default:
			out = append(out, LayerEffect{Type: typ, Params: params})
		}
	}
	return out, nil
}

// clipEffectParams projects the clip wire's flat typed fields onto the shared
// effect parameter vocabulary. Seeds are carried as int so the catalog's
// uint32 validation applies to both surfaces through toInt.
func clipEffectParams(spec semanticClipEffect) map[string]any {
	params := map[string]any{}
	if spec.Amount != nil {
		params["amount"] = *spec.Amount
	}
	if spec.Radius != nil {
		params["radius"] = *spec.Radius
	}
	if spec.Softness != nil {
		params["softness"] = *spec.Softness
	}
	if spec.BlockSize != nil {
		params["block_size"] = *spec.BlockSize
	}
	if spec.Size != nil {
		params["size"] = *spec.Size
	}
	if spec.Angle != nil {
		params["angle"] = *spec.Angle
	}
	if spec.Wavelength != nil {
		params["wavelength"] = *spec.Wavelength
	}
	if spec.Seed != nil {
		params["seed"] = int(*spec.Seed)
	}
	if spec.Animated != nil {
		params["animated"] = *spec.Animated
	}
	if spec.Intensity != nil {
		params["intensity"] = *spec.Intensity
	}
	if spec.Curvature != nil {
		params["curvature"] = *spec.Curvature
	}
	if spec.Scale != nil {
		params["scale"] = *spec.Scale
	}
	if spec.Threshold != nil {
		params["threshold"] = *spec.Threshold
	}
	if spec.Speed != nil {
		params["speed"] = *spec.Speed
	}
	return params
}

// compileClipGrid lowers the clip_grid block through the same shape resolver
// every shape item uses. The block is just a full-frame, screen-space grid;
// keeping a second bounds/fill/stroke implementation here let the two contracts
// drift (spacing, line width and dot radius each had duplicated limits).
func compileClipGrid(grid *semanticClipGrid, width, height int, duration int64, foregroundScale int) (Layer, error) {
	if grid == nil {
		return Layer{}, fmt.Errorf("overlay: clip_grid is required")
	}
	if grid.Scope != "" && grid.Scope != "full_canvas" && grid.Scope != "foreground" {
		return Layer{}, fmt.Errorf("overlay: clip_grid scope must be full_canvas or foreground")
	}
	canvasWidth, canvasHeight := width, height
	if grid.Scope == "foreground" && foregroundScale > 0 && foregroundScale < 100 {
		width = width * foregroundScale / 100
		height = height * foregroundScale / 100
	}
	isDots := grid.Style == "dots" || grid.Style == "dot_grid"
	shapeType := "grid"
	// The generated grid path is centred on the layer's local origin (lines
	// run -width/2..width/2), while `position` names the top-left of the layer
	// box in canvas coordinates. The box therefore has to be centred on the
	// canvas: anchoring a full-frame grid at -width/2 shifted it half a canvas
	// left/up and only the left/top half of the grid was visible.
	params := map[string]any{
		"shape":        shapeType,
		"width":        float64(width),
		"height":       float64(height),
		"grid_spacing": grid.SpacingPX,
		"position":     []float64{float64(canvasWidth-width) / 2, float64(canvasHeight-height) / 2},
		"opacity":      grid.Opacity,
	}
	if isDots {
		shapeType = "dot_grid"
		params["shape"] = shapeType
		params["fill"] = grid.Color
		dotRadius := 2.0
		if grid.DotRadiusPX != nil && *grid.DotRadiusPX > 0 {
			dotRadius = *grid.DotRadiusPX
		}
		// A dot wider than half a cell merges with its neighbours into one
		// smudge, so the declared grid stops being a grid.
		if dotRadius > grid.SpacingPX/2 {
			return Layer{}, fmt.Errorf("overlay: clip_grid dot_radius_px must not exceed half of spacing_px")
		}
		// The procedural dot grid rasterises one circle per cell. A spacing that
		// packs more than the renderer's generated-grid bound has to fail here
		// instead of asking the rasteriser for six figures of circles.
		columns := int(math.Max(1, math.Round(float64(width)/grid.SpacingPX)))
		rows := int(math.Max(1, math.Round(float64(height)/grid.SpacingPX)))
		if int64(columns)*int64(rows) > maxBackdropGridDots {
			return Layer{}, fmt.Errorf("overlay: clip_grid exceeds %d dots; increase spacing_px", maxBackdropGridDots)
		}
		params["dot_radius"] = dotRadius
	} else {
		if grid.LineWidthPX == nil {
			return Layer{}, fmt.Errorf("overlay: clip_grid line_width_px must be in [0.25,32]")
		}
		lineWidth := *grid.LineWidthPX
		params["grid_line_width"] = lineWidth
		params["stroke"] = map[string]any{"color": grid.Color, "width": lineWidth}
	}
	ri := resolvedItem{
		Item:   semanticItem{ID: "clip__grid", Kind: string(KindShape)},
		Params: params,
		Start:  0,
		End:    duration,
	}
	layer, err := resolveShape(ri, &semanticPlan{Width: canvasWidth, Height: canvasHeight})
	if err != nil {
		return Layer{}, err
	}
	layer.ScreenSpace = true
	return layer, nil
}

// resolveLayerEffects lowers an item's ordered effect stack. The shared catalog
// owns availability, parameters and bounds; this function only merges the
// entry-level shorthand (`radius`, `amount`, …) into `params`, applies the
// legacy intensity aliases and keeps authored order.
func resolveLayerEffects(raw any, itemID string) ([]LayerEffect, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q effects must be an array", itemID)
	}
	if len(list) > 64 {
		return nil, fmt.Errorf("overlay: item %q effects cannot exceed 64 entries", itemID)
	}
	subject := fmt.Sprintf("item %q effect", itemID)
	out := make([]LayerEffect, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q effect entry must be an object", itemID)
		}
		effType, _ := m["type"].(string)
		effType = strings.TrimSpace(effType)
		if effType == "" {
			return nil, fmt.Errorf("overlay: item %q effect requires a type", itemID)
		}
		// "none" is the explicit spelling of the default state: an absent or
		// empty stack already means no effect, and naming it must stay
		// unambiguous. It cannot be combined with a real effect or carry
		// parameters, so a producer never has to guess which instruction wins.
		if effType == "none" {
			if len(list) != 1 {
				return nil, fmt.Errorf("overlay: item %q effect \"none\" cannot be combined with other effects", itemID)
			}
			if len(m) > 1 {
				return nil, fmt.Errorf("overlay: item %q effect \"none\" accepts no parameters", itemID)
			}
			continue
		}
		// Editorial image looks are payload-level shortcuts for stable stacks
		// of renderer-native effects. They decorate the image surface only;
		// scene background selection remains an independent plan-level choice.
		if stack, ok := editorialImageLook(effType); ok {
			params, hasParams := m["params"].(map[string]any)
			if len(m) > 1 && (!hasParams || len(params) != 0 || len(m) > 2) {
				return nil, fmt.Errorf("overlay: item %q effect %s does not accept parameters", itemID, effType)
			}
			out = append(out, stack...)
			continue
		}
		definition, supported := effectDefinitionFor(effType, effectSurfaceLayer)
		if !supported {
			return nil, fmt.Errorf("overlay: item %q unsupported effect type %q", itemID, effType)
		}
		params, err := mergeEffectParams(m, effType, itemID)
		if err != nil {
			return nil, err
		}
		if err := applyEffectIntensityAlias(effType, params, itemID); err != nil {
			return nil, err
		}
		if err := validateEffectParams(definition, effType, params, subject); err != nil {
			return nil, err
		}
		// Clip/image parity: pseudo-types lower to the same renderer-native
		// stack on both surfaces so a clip preset replays 1:1 on an image.
		if lowered, pseudo := lowerPseudoEffect(effType, params); pseudo {
			out = append(out, lowered...)
			continue
		}
		out = append(out, LayerEffect{Type: effType, Params: params})
	}
	return out, nil
}

// mergeEffectParams flattens the entry-level shorthand (`radius`, `amount`, …)
// into one params map and rejects the ambiguous shapes: a non-object params
// block, a key declared twice, or a property the catalog does not own.
func mergeEffectParams(m map[string]any, effType, itemID string) (map[string]any, error) {
	params := make(map[string]any)
	if rawParams, exists := m["params"]; exists {
		pMap, ok := rawParams.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q effect %s params must be an object", itemID, effType)
		}
		for key, value := range pMap {
			params[key] = value
		}
	}
	for key, value := range m {
		if key == "type" || key == "params" {
			continue
		}
		if _, duplicate := params[key]; duplicate {
			return nil, fmt.Errorf("overlay: item %q effect %s repeats parameter %s", itemID, effType, key)
		}
		params[key] = value
	}
	for key := range m {
		if !layerEffectProperty(key) {
			return nil, fmt.Errorf("overlay: item %q effect %s has unsupported property %q", itemID, effType, key)
		}
	}
	return params, nil
}

// applyEffectIntensityAlias rewrites the legacy `intensity` spelling into the
// parameter each effect family actually reads. bloom/glow keep the name, so the
// converted number still has to pass validation.
func applyEffectIntensityAlias(effType string, params map[string]any, itemID string) error {
	intensity, present := params["intensity"]
	if !present {
		return nil
	}
	value, err := toFloat(intensity)
	if err != nil {
		return fmt.Errorf("overlay: item %q effect %s intensity must be a number: %w", itemID, effType, err)
	}
	switch effType {
	case "noise", "vignette":
		if _, has := params["amount"]; !has {
			params["amount"] = value
		}
	case "fractal_noise":
		if _, has := params["amplitude"]; !has {
			params["amplitude"] = value
		}
	case "bloom", "glow":
		params["intensity"] = value
	}
	return nil
}

// lowerPseudoEffect expands the pseudo-types that compile to a renderer-native
// stack, or normalizes the shorthand a native effect reads. pseudo=false means
// the effect lowers as itself.
func lowerPseudoEffect(effType string, params map[string]any) ([]LayerEffect, bool) {
	switch effType {
	case "glitch":
		amount := effectFloat(params, "amount", 0)
		seed := effectSeedValue(params)
		return []LayerEffect{
			{Type: "wave_warp", Params: map[string]any{"amplitude": 0.025 * amount, "wavelength": 0.035, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}},
			{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 8 * amount, "blue_offset": -8 * amount}},
			{Type: "noise", Params: map[string]any{"amount": 0.12 * amount, "seed": seed, "animated": true, "color_mode": "monochrome", "size": 1.0}},
		}, true
	case "scanlines":
		return []LayerEffect{scanlinesEffect(params)}, true
	case "banded_grain":
		intensity := effectFloat(params, "intensity", 0)
		speed := effectFloat(params, "speed", 0)
		seed := effectSeedValue(params)
		return []LayerEffect{{Type: "noise", Params: map[string]any{"amount": math.Min(1, intensity/4), "size": 1.0, "seed": seed, "animated": speed > 0, "color_mode": "monochrome"}}}, true
	case "black_and_white":
		return []LayerEffect{blackAndWhiteEffect(effectFloat(params, "amount", 1))}, true
	case "chromatic_aberration":
		// `amount` is the shorthand for a symmetric split; an explicit
		// red_offset keeps full control and is lowered as authored.
		if _, has := params["red_offset"]; !has {
			if raw, ok := params["amount"]; ok {
				if amount, err := toFloat(raw); err == nil {
					return []LayerEffect{{Type: effType, Params: map[string]any{"red_offset": amount, "blue_offset": -amount}}}, true
				}
			}
		}
	}
	return nil, false
}

// effectFloat reads an optional numeric effect param, keeping the catalog's
// documented fallback when the entry omits it or carries a non-number.
func effectFloat(params map[string]any, key string, fallback float64) float64 {
	if value, present := params[key]; present {
		if n, err := toFloat(value); err == nil {
			return n
		}
	}
	return fallback
}

func effectSeedValue(params map[string]any) int {
	seed := 0
	if value, present := params["seed"]; present {
		if n, err := toInt(value); err == nil {
			seed = n
		}
	}
	return seed
}

// effectSurface identifies where a public effect type is accepted. The two
// surfaces exist because the producer contract differs (image/shape layers
// carry an open params object, the clip adjustment stack carries flat typed
// fields), not because the effects themselves have two definitions.
type effectSurface uint8

const (
	effectSurfaceLayer effectSurface = iota
	effectSurfaceClip
)

type effectParamKind uint8

const (
	effectParamNumber effectParamKind = iota
	effectParamInteger
	effectParamUint32
	effectParamBool
)

type effectParamSpec struct {
	kind         effectParamKind
	minimum      float64
	maximum      float64
	exclusiveMin bool
}

// effectDefinition is the ONE declaration of a public effect type. This table
// replaces the former split between effectRegistry (availability), the bounds
// map in validateEffectParams and the per-type switch in resolveClipEffects.
// A new effect, a new parameter or a tightened bound is now a single edit here.
type effectDefinition struct {
	// layer and clip declare which surfaces accept the type. A type accepted
	// on both surfaces shares this parameter table.
	layer bool
	clip  bool
	// params lists the parameters the renderer understands for this type.
	// Parameters absent from the table are still allowed inside a layer's
	// `params` object (the renderer catalog may own extra fields), but the
	// clip wire validates against this list because its schema is closed.
	params map[string]effectParamSpec
	// clipRequired lists the parameters the clip schema requires. Layer
	// effects keep their historic optional-parameter contract.
	clipRequired []string
}

func effectNumber() effectParamSpec {
	return effectParamSpec{kind: effectParamNumber, maximum: math.MaxFloat64}
}

func effectBounded(min, max float64) effectParamSpec {
	return effectParamSpec{kind: effectParamNumber, minimum: min, maximum: max}
}

func effectBoundedExclusiveMin(min, max float64) effectParamSpec {
	return effectParamSpec{kind: effectParamNumber, minimum: min, maximum: max, exclusiveMin: true}
}

func effectInteger(min, max float64) effectParamSpec {
	return effectParamSpec{kind: effectParamInteger, minimum: min, maximum: max}
}

func effectSeed() effectParamSpec { return effectParamSpec{kind: effectParamUint32} }

func effectBoolean() effectParamSpec { return effectParamSpec{kind: effectParamBool} }

// effectDefinitions is the closed public vocabulary shared by every lowering
// path. Type names are the producer-visible names, so a clip pseudo-type
// (glitch, scanlines) lives beside the renderer-native ones instead of in a
// second private switch.
var effectDefinitions = map[string]effectDefinition{
	"noise": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount":   effectBounded(0, 1),
			"size":     effectBoundedExclusiveMin(0, 256),
			"seed":     effectSeed(),
			"animated": effectBoolean(),
		},
		clipRequired: []string{"amount", "size"},
	},
	"fractal_noise": {
		layer: true,
		params: map[string]effectParamSpec{
			"amplitude": effectBounded(0, 4),
			"frequency": effectBoundedExclusiveMin(0, 10),
			"octaves":   effectInteger(1, 16),
		},
	},
	"vignette": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount":   effectBounded(0, 1),
			"radius":   effectBounded(0, 1),
			"softness": effectBounded(0, 1),
		},
		clipRequired: []string{"amount", "radius", "softness"},
	},
	"gaussian_blur": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"radius": effectBounded(0, 256),
		},
		clipRequired: []string{"radius"},
	},
	"bloom": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"radius":    effectBounded(0, 256),
			"intensity": effectBounded(0, 4),
			"threshold": effectBounded(0, 1),
		},
		clipRequired: []string{"radius", "intensity"},
	},
	"glow": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"radius":    effectBounded(0, 256),
			"intensity": effectBounded(0, 4),
		},
		clipRequired: []string{"radius", "intensity"},
	},
	"chromatic_aberration": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount":      effectBounded(-128, 128),
			"red_offset":  effectNumber(),
			"blue_offset": effectNumber(),
		},
		clipRequired: []string{"amount"},
	},
	"mosaic": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"block_size": effectInteger(1, 512),
		},
		clipRequired: []string{"block_size"},
	},
	"halftone": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"size":   effectInteger(1, 128),
			"angle":  effectBounded(-3600, 3600),
			"amount": effectBounded(0, 1),
		},
		clipRequired: []string{"size", "angle", "amount"},
	},
	"sharpen": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount": effectBounded(0, 10),
		},
		clipRequired: []string{"amount"},
	},
	"lens_distortion": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"curvature": effectBounded(-1, 1),
			"scale":     effectBounded(0.1, 4),
		},
		clipRequired: []string{"curvature"},
	},
	"banded_grain": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"intensity": effectBounded(0, 4),
			"speed":     effectBounded(-1000, 1000),
			"seed":      effectSeed(),
		},
		clipRequired: []string{"intensity"},
	},
	"glitch": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount": effectBounded(0, 1),
			"seed":   effectSeed(),
		},
		clipRequired: []string{"amount"},
	},
	"scanlines": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount":     effectBounded(0, 0.25),
			"wavelength": effectBounded(0.002, 1),
		},
		clipRequired: []string{"amount", "wavelength"},
	},
	// black_and_white is the archival/opening treatment: amount 1.0 is full
	// desaturation, 0.0 leaves the frame untouched. It lowers to Chronon's
	// native color.saturation catalog effect with value = 1 - amount, the same
	// primitive the archive image look already renders, so a clip request and
	// an image stack never diverge on what "black and white" means.
	"black_and_white": {
		layer: true, clip: true,
		params: map[string]effectParamSpec{
			"amount": effectBounded(0, 1),
		},
		clipRequired: []string{"amount"},
	},
	// Renderer-native effects with no surface-specific bounds. Their parameter
	// validation is limited to finiteness and colour parsing, exactly as before
	// the catalog existed.
	"drop_shadow":        {layer: true},
	"light_rays":         {layer: true},
	"threshold":          {layer: true},
	"lut_3d":             {layer: true},
	"echo":               {layer: true},
	"keying":             {layer: true},
	"directional_blur":   {layer: true},
	"radial_blur":        {layer: true},
	"turbulent_displace": {layer: true},
	"wave_warp":          {layer: true},
	"ripple":             {layer: true},
	"displacement_map":   {layer: true},
	"corner_pin":         {layer: true},
	"twirl":              {layer: true},
	"bulge":              {layer: true},
	"mesh_warp":          {layer: true},
	"catalog":            {layer: true},
}
