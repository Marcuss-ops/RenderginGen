package overlay

import (
	"fmt"
	"strings"
)

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

// The image-bound sound-effect contract: one
// already-selected audio asset attached to an image's first visible frame.
// The cue is validated against the canonical microsecond window (not the
// millisecond projection) and lowered to a frame window here, so
// compileSemanticDecoded only registers assets and collects the result.

// resolveSoundEffectCue validates one item's sound_effect declaration and
// returns the concrete audio clip plus the last frame the cue may cover. The
// caller clamps it to the finalized canvas duration.
func resolveSoundEffectCue(item semanticItem, resolvedItem resolvedItem, src *semanticPlan, registry *assetRegistry) (AudioClipPlan, int64, error) {
	sfx := item.SoundEffect
	if sfx == nil {
		return AudioClipPlan{}, 0, nil
	}
	if sfx.StartOffsetMS < 0 || sfx.DurationMS <= 0 {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect requires non-negative start_offset_ms and positive duration_ms", item.ID)
	}
	if sfx.GainDB != nil && (!finite(*sfx.GainDB) || *sfx.GainDB < -96 || *sfx.GainDB > 24) {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect gain_db must be finite and in [-96,24]", item.ID)
	}
	// The item window is canonical microseconds; the millisecond
	// projection is only a compatibility mirror and must not decide
	// whether a cue fits. Child image layers still carry milliseconds,
	// which imageLayerWindowUS projects exactly once.
	itemStartUS, itemEndUS := itemWindowUS(item)
	visibleStartOffsetMS := int64(0)
	visibleEndOffsetUS := itemEndUS - itemStartUS
	if len(item.ImageLayers) > 0 {
		firstLayer := item.ImageLayers[0]
		for _, layer := range item.ImageLayers[1:] {
			if layer.StartMS < firstLayer.StartMS || layer.StartMS == firstLayer.StartMS && layer.EndMS > firstLayer.EndMS {
				firstLayer = layer
			}
		}
		visibleStartOffsetMS = firstLayer.StartMS
		_, visibleEndOffsetUS = imageLayerWindowUS(firstLayer)
		if visibleEndOffsetUS > itemEndUS-itemStartUS {
			visibleEndOffsetUS = itemEndUS - itemStartUS
		}
		if sfx.StartOffsetMS != visibleStartOffsetMS {
			return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q composite sound_effect must start at first visible image layer (%dms)", item.ID, visibleStartOffsetMS)
		}
	} else if sfx.StartOffsetMS != 0 {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect start_offset_ms must be zero for a single-layer image", item.ID)
	}
	visibleStartOffsetUS := visibleStartOffsetMS * 1000
	visibleDurationUS := visibleEndOffsetUS - visibleStartOffsetUS
	// Compare the cue against the exact canonical window. The shortened
	// first check keeps the multiplication below overflow-safe.
	if visibleDurationUS <= 0 || sfx.DurationMS > visibleDurationUS/1000 || sfx.DurationMS*1000 > visibleDurationUS {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect exceeds its first visible image window", item.ID)
	}
	if strings.TrimSpace(sfx.AssetRef.MediaType) != "audio/mp4" {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect asset must declare media_type audio/mp4", item.ID)
	}
	path, err := registry.Register(sfx.AssetRef)
	if err != nil {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect asset: %w", item.ID, err)
	}
	startOffsetFrame, visibleDurationEndFrame := usFrames(visibleStartOffsetUS, visibleEndOffsetUS, int64(src.FPSNum), int64(src.FPSDen))
	startFrame := resolvedItem.Start + startOffsetFrame
	visibleEndFrame := resolvedItem.Start + visibleDurationEndFrame
	_, clipOut := msFrames(0, sfx.DurationMS, int64(src.FPSNum), int64(src.FPSDen))
	if startFrame < 0 || visibleEndFrame <= startFrame || clipOut <= 0 {
		return AudioClipPlan{}, 0, fmt.Errorf("overlay: item %q sound_effect timing collapses to no frames", item.ID)
	}
	gain := -18.0
	if sfx.GainDB != nil {
		gain = *sfx.GainDB
	}
	clip := AudioClipPlan{ID: "overlay-sfx-" + item.ID, Asset: path, Start: startFrame, ClipOut: clipOut, GainDB: gain, Role: "sfx"}
	return clip, visibleEndFrame, nil
}
