package overlay

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// visual_accents_recipe.go lowers the four official Visual Accents V1
// families through the SAME image-recipe machinery image_premium_v1 uses —
// component layers, native path trim, native Field2D masks and 2.5D sync —
// never through a per-family mini renderer:
//
//	brush_v1      stroked path components with trim animation
//	web_rect_v1   rounded panel components under the animated entity image
//	paint_v1      native Field2D mask on the image layer
//	light_leak_v1 blurred ellipse glow plates composited around the image
//
// visualAccentsDefinition resolves one visual accents V1 motion definition.
func visualAccentsDefinition(id string) (*motion.MotionDefinition, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil || plugin == nil {
		return nil, err
	}
	declarative, ok := plugin.(motion.DeclarativePlugin)
	if !ok {
		return nil, nil
	}
	definition := declarative.Definition
	if !motion.VisualAccentsV1CategoriesContains(definition.Category) {
		return nil, nil
	}
	return &definition, nil
}

// compileVisualAccentsImageLayers lowers one visual accents motion around a
// single compiled image layer. The branch order mirrors the family gates in
// motion.ValidateDefinition, so a catalog shape cannot route into the wrong
// lowering.
func compileVisualAccentsImageLayers(ri resolvedItem, src *semanticPlan, image Layer, definition *motion.MotionDefinition) ([]Layer, error) {
	if definition == nil {
		return []Layer{image}, nil
	}
	recipe := definition.ImageRecipe
	if recipe == nil {
		return []Layer{image}, nil
	}
	// Paint V1: native Field2D mask on the image layer itself.
	if recipe.Mask != nil {
		mask, err := compileVisualAccentsFieldMask(image, recipe.Mask, image.DurationFrames, int64(definition.Enter))
		if err != nil {
			return nil, err
		}
		image.Masks = append(image.Masks, mask)
		image.PremiumWipeMask = true
		return []Layer{image}, nil
	}
	// Brush / web rect / light leak: component layers around the image.
	return compileVisualAccentsComponents(src, image, *definition)
}

// compileVisualAccentsFieldMask lowers a paint_v1 field mask to the native
// Field2D mask stack entry Chronon already samples (MaskType::Field2D). The
// authored progress track is retimed against the motion entrance exactly like
// the premium wipe path is.
func compileVisualAccentsFieldMask(image Layer, mask *motion.ImageMaskDefinition, duration, authoredEnter int64) (LayerMask, error) {
	width, height := 0.0, 0.0
	if len(image.Size) >= 2 {
		width, height = image.Size[0], image.Size[1]
	}
	if mask.Field == nil {
		return LayerMask{}, fmt.Errorf("overlay: image layer %q has a %q mask without a field recipe", image.ID, mask.Kind)
	}
	field := mask.Field
	if field.Frequency <= 0 || field.Frequency > 4096 {
		return LayerMask{}, fmt.Errorf("overlay: image layer %q field frequency %v is outside (0, 4096]", image.ID, field.Frequency)
	}
	if len(field.Operators) > 8 {
		return LayerMask{}, fmt.Errorf("overlay: image layer %q field operator chain exceeds 8", image.ID)
	}
	feather := mask.Feather
	if feather < 0 || math.IsNaN(feather) || math.IsInf(feather, 0) {
		return LayerMask{}, fmt.Errorf("overlay: image layer %q mask feather %v is invalid", image.ID, feather)
	}
	layerField := map[string]any{
		"generator": field.Generator,
		"seed":      field.Seed,
		"frequency": field.Frequency,
	}
	if field.Octaves > 0 {
		layerField["octaves"] = field.Octaves
	}
	if len(field.Operators) > 0 {
		operators := make([]map[string]any, 0, len(field.Operators))
		for _, operator := range field.Operators {
			entry := map[string]any{"kind": operator.Kind}
			for key, value := range operator.Params {
				entry[key] = value
			}
			operators = append(operators, entry)
		}
		layerField["operators"] = operators
	}
	// The reveal travels on the native mask parameter tracks: expansion grows
	// the field threshold from fully closed (-feather minus a full field swing)
	// to neutral 0, while opacity finishes the coverage from 0 to 1. Field
	// masks reject a morph animation by contract, so progress maps here —
	// the renderer clamps and smoothsteps both parameters per frame.
	animation := premiumPathAnimation(mask.Progress, duration, authoredEnter)
	expansionKeys := make([]AnimationKeyframe, len(animation.Keyframes))
	opacityKeys := make([]AnimationKeyframe, len(animation.Keyframes))
	const fieldSwing = 2.0 // field values live in [0,1]; swing covers any warp overshoot
	for i, keyframe := range animation.Keyframes {
		progress, ok := numericTrackValue(keyframe.Value)
		if !ok {
			return LayerMask{}, fmt.Errorf("overlay: image layer %q paint mask progress keyframe %d is not scalar", image.ID, i)
		}
		progress = math.Max(0, math.Min(1, progress))
		expansionKeys[i] = AnimationKeyframe{Frame: keyframe.Frame, Value: -(fieldSwing + feather) * (1 - progress)}
		opacityKeys[i] = AnimationKeyframe{Frame: keyframe.Frame, Value: progress}
	}
	// The wire contract: field masks carry an explicit size (their sampling
	// frame) and their parameter tracks are bare keyframe lists — the decoder
	// derives the property from the field name.
	return LayerMask{
		Type:    "field",
		Mode:    "add",
		Feather: feather,
		Size:    []float64{width, height},
		Field:   layerField,
		ExpansionTrack: &LayerMaskParameterTrack{
			Keyframes: expansionKeys},
		OpacityTrack: &LayerMaskParameterTrack{
			Keyframes: opacityKeys},
	}, nil
}

// compileVisualAccentsComponents lowers the brush/web/light-leak recipe
// components through the same compiler image_premium_v1 uses, then applies
// the family normalization pass.
func compileVisualAccentsComponents(src *semanticPlan, image Layer, definition motion.MotionDefinition) ([]Layer, error) {
	layers, err := compilePremiumComponents(src, image, definition)
	if err != nil {
		return nil, err
	}
	return normalizedPremiumComponents(src, image, layers), nil
}
