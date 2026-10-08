// semantic_image_caption.go owns the caption half of the image subsystem:
// linked-caption geometry and role-based lowering through the shared text
// spec seam. Image layer lowering stays in
// semantic_image_compile.go; composition validation in
// semantic_image_validate.go.
package overlay

import (
	"fmt"
	"strings"
)

func compileImageCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string, role textRole) (Layer, error) {
	captionMotionPlanID, captionMotionVideoID, captionMotionItemID = src.PlanID, src.VideoID, parent.Item.ID
	defer func() { captionMotionPlanID, captionMotionVideoID, captionMotionItemID = "", "", "" }()
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("overlay: item %q image caption has no positive image geometry", parent.Item.ID)
	}
	// The resolver owns the geometry: image bottom_center + margin anchor,
	// safe-area clamping and long-name font fitting all happen there. The
	// lowering below only writes the resolver's answer into the layer.
	imageBounds := EntityCardImageBoundsFromCenter(src.Width, src.Height, image.Position, image.Size[0], image.Size[1])
	layout, err := ResolveEntityCardLayoutAt(src.Width, src.Height, imageBounds, caption, child.CaptionLayout)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q entity caption layout: %w", parent.Item.ID, err)
	}
	// Vertical fallback: the resolver may shift the image up so the pair
	// image + margin + caption fits inside the safe area. Image layers are
	// positioned relative to the canvas center, so an upward canvas shift of
	// `shift` px converts to exactly minus `shift`; the caption's geometry
	// below is canvas-absolute.
	if shift := imageBounds.Y - layout.ImageBounds.Y; shift != 0 {
		if len(image.Position) < 2 {
			image.Position = []float64{0, 0}
		}
		image.Position[1] -= shift
	}
	captionBounds := layout.CaptionBounds
	width := int(captionBounds.Width)
	if width < 1 {
		width = 1
	}
	captionLayerID := parent.Item.ID + ":" + childID + ":caption"
	// Caption semantics resolve directly to a role-aware text spec, without
	// borrowing a phrase template, preset, or phrase-specific motion policy.
	captionMotion := EntityCaptionMotionID(child.CaptionMotionID)
	if !motionAdmitsTarget(captionMotion, "caption") {
		captionMotion = entityCaptionMotionPool[styleHash("caption_motion_fallback", captionMotionPlanID, captionMotionVideoID, captionMotionItemID, uint64(len(entityCaptionMotionPool)))]
	}
	if !motionAdmitsTarget(captionMotion, "caption") {
		return Layer{}, fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", parent.Item.ID, captionMotion)
	}
	positionX := captionBounds.CenterX
	positionY := captionBounds.CenterY
	styleParams := map[string]any{
		"position_x":   positionX,
		"position_y":   positionY,
		"font_size_px": captionBounds.FontSize,
	}
	// A run-level font_family is the explicit runtime style choice. Preserve
	// the entity preset's typeface only when the caller did not choose one.
	if family, ok := parent.Params["font_family"].(string); ok && strings.TrimSpace(family) != "" {
		styleParams["font_family"] = strings.TrimSpace(family)
	}
	if _, present := styleParams["font_family"]; !present && strings.TrimSpace(child.CaptionFontFamily) != "" {
		styleParams["font_family"] = strings.TrimSpace(child.CaptionFontFamily)
	}
	overrideFill := strings.TrimSpace(child.CaptionColor)
	if overrideFill != "" {
		if _, err := parseHexColor(overrideFill); err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q caption color: %w", parent.Item.ID, err)
		}
	}
	motionParams := child.CaptionMotionParams
	if motionParams == nil {
		motionParams = map[string]any{"enter_frames": 8}
	}
	captionSpec := resolvedTextSpec{
		Text: caption, BoxWidth: width, BoxHeight: int(captionBounds.Height),
		Size:     []float64{captionBounds.Width, captionBounds.Height},
		Position: []float64{positionX, positionY}, Style: &LayerStyle{Font: OfficialFontPathForLanguage(src.Language)},
		MotionID: captionMotion, MotionParams: motionParams,
		StyleParams: styleParams, MotionTarget: "caption", StylePolicy: role,
		FontSize: captionBounds.FontSize, OverrideFill: overrideFill,
	}
	captionLayer, err := compileResolvedText(captionLayerID, image.StartFrame, image.StartFrame+image.DurationFrames, captionSpec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: entity image caption %q: %w", captionLayerID, err)
	}
	captionLayer.CaptionForImageID = image.ID
	captionLayer.CaptionLayout = child.CaptionLayout
	return captionLayer, nil
}

// compileEntityCaptionLayer keeps entity-card call sites explicit while
// sharing all geometry, role resolution, and lowering with image captions.
func compileEntityCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string) (Layer, error) {
	return compileImageCaptionLayer(parent, src, child, caption, image, childID, textRoleEntityCaption)
}
