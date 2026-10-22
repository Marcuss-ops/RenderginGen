// semantic_entity_compile.go owns the entity-card compiler: a portrait and its
// optional entity caption, lowered per the entity style variant and the image
// preset. Dispatched from semantic_compile.go's compileItem.
package overlay

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// compileEntityCard lowers a portrait and its optional entity caption. The
// image preset owns the portrait geometry and motion; position_x/position_y
// can move the portrait so caption_layout can use the opposite side.
func compileEntityCard(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	var currentStyle entityStyleVariant
	var hasStyle bool
	if ri.Item.EntityStyleID != "" {
		imageMotionOverride := ri.Item.MotionID
		captionMotionOverride := ri.Item.CaptionMotionID
		captionMotionParams := ri.Item.CaptionMotionParams
		style, ok := ResolveEntityStyle(ri.Item.EntityStyleID, src.entityStyleSampleKey(), src.VideoID, ri.Item.ID)
		if !ok {
			style = entityStyleRegistry.sample(entityStyleRegistry.query(nil), "", src.entityStyleSampleKey(), src.VideoID, ri.Item.ID)
		}
		// Layout, colour, image motion and caption motion all stay exactly as
		// the style (or the producer override) declares them. The selected
		// catalog motion reaches Chronon unchanged so GPU failures stay visible.
		currentStyle = style
		hasStyle = true
		ri.Item.MotionID = style.ImageMotionID
		if imageMotionOverride != "" {
			ri.Item.MotionID = imageMotionOverride
		}
		ri.Item.CaptionMotionID = style.CaptionMotionID
		if captionMotionOverride != "" {
			ri.Item.CaptionMotionID = captionMotionOverride
		}
		ri.Item.CaptionMotionParams = captionMotionParams
		ri.Item.CaptionLayout = style.CaptionLayout
		// SSOT typography (typography_ssot.go): the payload's explicit
		// typeface/color/layout always win over the preset default — same
		// precedence as the motion overrides above. The preset default only
		// fills in when the plan carries no choice, so selecting an entity
		// style can never silently retype or recolor a caption the
		// producer set (the old unconditional CaptionColor overwrite made
		// every render diverge per sampled style).
		if strings.TrimSpace(ri.Item.CaptionColor) == "" {
			ri.Item.CaptionColor = style.CaptionColor
		}
		if strings.TrimSpace(ri.Item.CaptionLayout) == "" {
			ri.Item.CaptionLayout = style.CaptionLayout
		}
		// Place the runtime portrait in one of two balanced columns, or center it
		// for below-caption layouts.
		x := 0.0
		switch style.ImageSide {
		case "left":
			x = -float64(src.Width) * 0.25
		case "right":
			x = float64(src.Width) * 0.25
		case "center", "below":
			x = 0.0
		default:
			return nil, fmt.Errorf("overlay: entity style %q has unsupported image side %q", style.Name, style.ImageSide)
		}
		ri.Params["position_x"] = x
		ri.Params["position_y"] = 0.0
	}
	if ri.Item.MotionID == "" && ri.ImagePreset.ID != "" {
		ri.Item.MotionID = runtimeDefaultImageMotionID(src, ri.Item.ID)
	}
	resolvedImageMotion, err := resolveRegisteredMotion(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: entity image motion %q: %w", ri.Item.MotionID, err)
	}
	img := imageLayer(ri, registry.Path(ri.Item.Assets[0].ID))
	if hasStyle && currentStyle.CameraMotionID != "" {
		img.Enable3D = true
	}
	if ri.ImagePreset.ID != "" {
		applyPresetDefinition(&img, ri.ImagePreset)
		var imgAnimation *LayerAnimation
		if ri.Item.MotionID != "" {
			imgAnimation, err = imageMotionAnimationResolved(ri.Item.MotionID, ri.Item.MotionParams, ri.End-ri.Start, ri.ImagePreset.Motion.Exit, ri.Item.ID, TargetImage, resolvedImageMotion)
		} else {
			imgAnimation, err = animationForPreset(ri.ImagePreset, "", ri.End-ri.Start)
		}
		if err != nil {
			return nil, err
		}
		applyMotionRouting(&img, imgAnimation)
		// Keep the source portrait uncropped; an explicit position lets the
		// producer place it on either side while the preset retains its size.
		img.Position = []float64{0, 0}
		img.Fit = FitContain
	}
	defaultImageBox(&img)
	if x, ok := numericValue(ri.Params["position_x"]); ok {
		y, _ := numericValue(ri.Params["position_y"])
		img.Position = []float64{x, y}
	}
	// Entities with text own a portrait card: a non-vertical box is covered
	// into the supported portrait (same height and center), so the caption
	// compiled from its bounds follows automatically. Vertical images pass
	// through untouched.
	if strings.TrimSpace(ri.Item.EntityCaption) != "" {
		autoFitEntityPortrait(&img)
	}
	if err := applyFrameTreatment(&img, ri.Item.Frame); err != nil {
		return nil, fmt.Errorf("overlay: entity image item %q: %w", ri.Item.ID, err)
	}
	if ri.Item.MotionID != "" && ri.ImagePreset.ID == "" {
		return nil, fmt.Errorf("overlay: entity image item %q with motion_id requires image_preset_id", ri.Item.ID)
	}
	var premium *motion.MotionDefinition
	if resolvedImageMotion != nil {
		premium = premiumImageRecipeDefinition(resolvedImageMotion.definition)
	}
	layers, err := compilePremiumImageLayers(ri, src, img, premium)
	if err != nil {
		return nil, err
	}
	if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
		imageIndex := premiumImageLayerIndex(layers)
		captionItem := ri.Item
		captionLayer, err := compileEntityCaptionLayer(ri, src, captionItem, caption, &layers[imageIndex], "entity")
		if err != nil {
			return nil, err
		}
		if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
			premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
		}
		layers = append(layers, captionLayer)
	}
	if premium != nil && premium.ImageRecipe.RequireCaption && strings.TrimSpace(ri.Item.EntityCaption) == "" {
		return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
	}
	return layers, nil
}
