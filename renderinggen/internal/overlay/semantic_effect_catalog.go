package overlay

import (
	"fmt"
	"math"
)

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
		clip: true,
		params: map[string]effectParamSpec{
			"curvature": effectBounded(-1, 1),
			"scale":     effectBounded(0.1, 4),
		},
		clipRequired: []string{"curvature"},
	},
	"banded_grain": {
		clip: true,
		params: map[string]effectParamSpec{
			"intensity": effectBounded(0, 4),
			"speed":     effectBounded(-1000, 1000),
			"seed":      effectSeed(),
		},
		clipRequired: []string{"intensity"},
	},
	"glitch": {
		clip: true,
		params: map[string]effectParamSpec{
			"amount": effectBounded(0, 1),
			"seed":   effectSeed(),
		},
		clipRequired: []string{"amount"},
	},
	"scanlines": {
		clip: true,
		params: map[string]effectParamSpec{
			"amount":     effectBounded(0, 0.25),
			"wavelength": effectBounded(0.002, 1),
		},
		clipRequired: []string{"amount", "wavelength"},
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

// effectDefinitionFor returns the definition when the type is part of the
// closed vocabulary for the requested surface.
func effectDefinitionFor(typ string, surface effectSurface) (effectDefinition, bool) {
	definition, ok := effectDefinitions[typ]
	if !ok {
		return effectDefinition{}, false
	}
	if surface == effectSurfaceLayer && !definition.layer {
		return effectDefinition{}, false
	}
	if surface == effectSurfaceClip && !definition.clip {
		return effectDefinition{}, false
	}
	return definition, true
}

// layerEffectProperty reports whether a layer effect entry may carry key at the
// top level (beside `type`/`params`). The set is exactly `$defs/layer_effect`
// in the published schema: anything else belongs inside `params`, so accepting
// it here would let a producer pass validation with a plan the schema rejects.
func layerEffectProperty(key string) bool {
	switch key {
	case "type", "params", "intensity", "amount", "amplitude", "size", "frequency",
		"octaves", "radius", "softness":
		return true
	}
	return false
}

// validateEffectParams normalizes every declared parameter of typ in place and
// rejects values outside the shared bounds. subject names the declaration for
// the error message ("item \"x\" effect" or "clip effect").
func validateEffectParams(definition effectDefinition, typ string, params map[string]any, subject string) error {
	for key, raw := range params {
		if key == "color" {
			color, err := parseRGBA(raw)
			if err != nil {
				return fmt.Errorf("overlay: %s %s color is invalid: %w", subject, typ, err)
			}
			if err := validateRGBA(color); err != nil {
				return fmt.Errorf("overlay: %s %s color is invalid: %w", subject, typ, err)
			}
			params[key] = color
			continue
		}
		if number, err := toFloat(raw); err == nil && !finite(number) {
			return fmt.Errorf("overlay: %s %s parameter %s must be finite", subject, typ, key)
		}
	}
	for key, spec := range definition.params {
		raw, exists := params[key]
		if !exists {
			continue
		}
		switch spec.kind {
		case effectParamNumber:
			value, err := toFloat(raw)
			if err != nil {
				return fmt.Errorf("overlay: %s %s %s must be a number: %w", subject, typ, key, err)
			}
			if err := effectValueInBounds(value, spec, subject, typ, key); err != nil {
				return err
			}
			params[key] = value
		case effectParamInteger:
			value, err := toInt(raw)
			if err != nil {
				return fmt.Errorf("overlay: %s %s %s must be an integer", subject, typ, key)
			}
			if err := effectValueInBounds(float64(value), spec, subject, typ, key); err != nil {
				return err
			}
			params[key] = value
		case effectParamUint32:
			value, err := toInt(raw)
			if err != nil || value < 0 || uint64(value) > uint64(^uint32(0)) {
				return fmt.Errorf("overlay: %s %s %s must be an unsigned 32-bit integer", subject, typ, key)
			}
			params[key] = value
		case effectParamBool:
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("overlay: %s %s %s must be a boolean", subject, typ, key)
			}
		}
	}
	return nil
}

func effectValueInBounds(value float64, spec effectParamSpec, subject, typ, key string) error {
	below := value < spec.minimum || (spec.exclusiveMin && value == spec.minimum)
	if !below && value <= spec.maximum {
		return nil
	}
	comparison := "["
	if spec.exclusiveMin {
		comparison = "("
	}
	return fmt.Errorf("overlay: %s %s %s must be in %s%g, %g]", subject, typ, key, comparison, spec.minimum, spec.maximum)
}
