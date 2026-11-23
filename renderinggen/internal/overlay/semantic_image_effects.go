// semantic_image_effects.go owns the runtime effect decoration of image
// items: the item's `params.effects` entry is the data-driven effect stack a
// producer requests at runtime (job payload, no recompile), lowered onto every
// base image layer the item produces through the shared layer-effect resolver.
// Recipe components (premium plates, captions, glow plates) keep their own
// authored effects untouched: the runtime stack only decorates the photographic
// surfaces, never the recipe's structural layers.

package overlay

import (
	"fmt"
	"strings"
)

// compileImageItem lowers one semantic image item and then runs the shared
// finalizer. The core lowering is compileImageItemCore; compileItem routes
// entity cards and entity groups through the same finalizer, so an item's
// declared backdrop, glow, runtime effect stack and grid can never be skipped
// because of the path that lowered its base layers.
func compileImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	layers, err := compileImageItemCore(ri, src, registry)
	if err != nil {
		return nil, err
	}
	return finalizeImageLayers(ri, src, registry, layers, true)
}

// finalizeImageLayers is the ONE post-lowering pipeline for image-producing
// items, in execution order:
//
//  1. optional per-member backdrops (owner items only — an entity card already
//     emitted its own single backdrop during lowering);
//  2. the image glow plate;
//  3. the item's runtime params.effects stack;
//  4. the standalone params.grid overlay.
//
// It replaces three copies of the same glow→effects→grid chain inside
// compileItem and the fourth inside compileImageItem, so a new decoration
// stage is added in exactly one place.
func finalizeImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry, layers []Layer, backdrops bool) ([]Layer, error) {
	var err error
	if backdrops {
		// One entry point owns the single/member/shared-backdrop decision.
		layers, err = addImageBackdrops(ri, src, registry, layers)
		if err != nil {
			return nil, err
		}
	}
	layers, err = applyImageGlowToBaseLayers(ri, layers)
	if err != nil {
		return nil, err
	}
	layers, err = applyImageItemEffects(ri, layers)
	if err != nil {
		return nil, err
	}
	return addStandaloneImageGrid(ri, src, layers)
}

// decorateImageLayerWithItemEffects applies the item's runtime effect stack to
// one freshly lowered image layer, leaving effect-free items untouched. Recipe
// members call it for their own params.effects so a per-child stack is honoured
// exactly like a composite child's.
func decorateImageLayerWithItemEffects(ri resolvedItem, layer Layer) (Layer, error) {
	if _, declared := ri.Params["effects"]; !declared {
		return layer, nil
	}
	layers, err := applyImageItemEffects(ri, []Layer{layer})
	if err != nil {
		return Layer{}, err
	}
	return layers[0], nil
}

// applyImageItemEffects lowers item-level params.effects onto each base image
// layer of the item. Base image layers are the layers the shared single-layer
// path builds (standalone image, every image_layers child and every group
// member); their ids all end in ":image", which keeps recipe component layers
// out of the decoration. Authored effects on those layers stay first in the
// stack; the runtime stack is appended after them. Every decorated layer gets
// its own parameter copy so one layer can never mutate a sibling's stack.
func applyImageItemEffects(ri resolvedItem, layers []Layer) ([]Layer, error) {
	raw, exists := ri.Params["effects"]
	if !exists || raw == nil {
		return layers, nil
	}
	effects, err := resolveLayerEffects(raw, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	if len(effects) == 0 {
		return layers, nil
	}
	decorated := false
	for index := range layers {
		layer := &layers[index]
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") {
			continue
		}
		for _, effect := range effects {
			params := make(map[string]any, len(effect.Params))
			for key, value := range effect.Params {
				params[key] = value
			}
			layer.Effects = append(layer.Effects, LayerEffect{Type: effect.Type, Params: params})
		}
		decorated = true
	}
	if !decorated {
		return nil, fmt.Errorf("overlay: item %q requests runtime image effects but lowered no base image layer to decorate", ri.Item.ID)
	}
	return layers, nil
}

// The caption half of the image subsystem: linked-caption geometry and
// role-based lowering through the shared text spec seam. Image layer lowering
// and composition validation stay in semantic_image_compile.go.
func compileImageCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string, role textRole) (Layer, error) {
	captionIdentity := parent.Item.ID + ":" + childID
	captionMotion := ""
	if child.CaptionMotionID != "" {
		captionMotion = EntityCaptionMotionID(child.CaptionMotionID)
	} else {
		captionMotion = entityCaptionMotionIDForLayout("", child.CaptionLayout, src.PlanID, src.VideoID, captionIdentity)
	}
	// A 3D caption motion is kept as declared: the text lowering derives
	// enable_3d from the animators it emits, so Chronon receives the camera-
	// backed caption exactly as the producer asked for it.
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("overlay: item %q image caption has no positive image geometry", parent.Item.ID)
	}
	// The resolver owns the geometry: image bottom_center + margin anchor,
	// safe-area clamping and long-name font fitting all happen there. The
	// lowering below only writes the resolver's answer into the layer.
	imageBounds := EntityCardImageBoundsFromCenter(src.Width, src.Height, image.Position, image.Size[0], image.Size[1])
	layout, err := ResolveEntityCardLayoutAtSize(src.Width, src.Height, imageBounds, caption, child.CaptionLayout, 0)
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
	if !motionAdmitsTarget(captionMotion, TargetCaption) {
		captionMotion = entityCaptionMotionIDForLayout("", child.CaptionLayout, src.PlanID, src.VideoID, captionIdentity)
	}
	if !motionAdmitsTarget(captionMotion, TargetCaption) {
		return Layer{}, fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", parent.Item.ID, captionMotion)
	}
	positionX := captionBounds.CenterX
	positionY := captionBounds.CenterY
	styleParams := map[string]any{
		"position_x":   positionX,
		"position_y":   positionY,
		"font_size_px": captionBounds.FontSize,
	}
	// Run-level runtime text style is the explicit payload choice. Thread the
	// payload's look (font, glow, stroke, shadow) onto the caption layer so
	// entity captions render the same style as phrases; the caption's
	// geometry-resolved font_size_px stays untouched. Fall back to the
	// language-level fallback supplies the family when the caller did not
	// choose one. Entity composition presets never own a typeface.
	for _, key := range runtimeTextStyleKeys {
		if key == "font_size_px" {
			continue
		}
		if value, ok := child.Params[key]; ok {
			styleParams[key] = value
		}
		// Parent runtime style is the canonical payload choice for every
		// caption in the image recipe. Child animation/preset data supplies
		// defaults only; it cannot silently change typography between renders.
		if value, ok := parent.Params[key]; ok {
			styleParams[key] = value
		}
	}
	mergeStyle := func(value map[string]any) {
		if current, currentOK := styleParams["style"].(map[string]any); currentOK {
			merged := make(map[string]any, len(current)+len(value))
			for key, entry := range current {
				merged[key] = entry
			}
			for key, entry := range value {
				merged[key] = entry
			}
			styleParams["style"] = merged
		} else {
			styleParams["style"] = value
		}
	}
	if value, ok := child.Params["style"].(map[string]any); ok {
		mergeStyle(value)
	}
	if value, ok := parent.Params["style"].(map[string]any); ok {
		mergeStyle(value)
	}
	if family := strings.TrimSpace(child.CaptionFontFamily); family != "" {
		if _, parentChoice := parent.Params["font_family"]; !parentChoice {
			styleParams["font_family"] = family
		}
	}
	overrideFill := ""
	if style, ok := child.Params["style"].(map[string]any); ok {
		if fill, ok := style["fill"].(string); ok {
			overrideFill = strings.TrimSpace(fill)
		}
	}
	if style, ok := parent.Params["style"].(map[string]any); ok {
		if fill, ok := style["fill"].(string); ok {
			overrideFill = strings.TrimSpace(fill)
		}
	}
	if overrideFill == "" {
		overrideFill = strings.TrimSpace(child.CaptionColor)
	}

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
		Size:         []float64{captionBounds.Width, captionBounds.Height},
		Position:     []float64{positionX, positionY},
		CanvasWidth:  src.Width,
		CanvasHeight: src.Height,
		Style:        &LayerStyle{Font: OfficialFontPathForLanguage(src.Language)},
		MotionID:     captionMotion, MotionParams: motionParams,
		StyleParams: styleParams, MotionTarget: TargetCaption, StylePolicy: role,
		FontSize: captionBounds.FontSize, OverrideFill: overrideFill,
	}
	captionLayer, err := compileResolvedText(captionLayerID, image.StartFrame, image.StartFrame+image.DurationFrames, captionSpec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: entity image caption %q: %w", captionLayerID, err)
	}
	// A caption whose image was fitted from its probed source aspect keeps the
	// historical pinned font interval: the label is resolved against the FINAL
	// card geometry in this compile, and the interval must travel with
	// shrink_only so Chronon accepts the limits.
	if image.SourceFitted {
		pinCaptionFitSize(captionLayer.Style)
	}
	captionLayer.CaptionForImageID = image.ID
	captionLayer.CaptionLayout = child.CaptionLayout
	return captionLayer, nil
}

// pinCaptionFitSize locks a fitted caption to the size the layout resolved.
// Chronon rejects font size limits without a non-none fit mode, so the interval
// must travel with shrink_only (min == max renders the pinned size verbatim);
// emitting the limits alone failed every fitted entity card.
func pinCaptionFitSize(style *LayerStyle) {
	if style == nil {
		return
	}
	style.MinFontSize = style.FontSize
	style.MaxFontSize = style.FontSize
	style.FitMode = FitModeShrinkOnly
}

// compileEntityCaptionLayer keeps entity-card call sites explicit while
// sharing all geometry, role resolution, and lowering with image captions.
func compileEntityCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string) (Layer, error) {
	return compileImageCaptionLayer(parent, src, child, caption, image, childID, textRoleEntityCaption)
}

// semantic_image_effects.go lowers the payload's image.glow block onto the native
// Chronon glow effect. Image glow is deliberately an effect, not a text-style
// glow: it must decorate the photographic media layer and never a caption.
// applyImageGlowToBaseLayers applies image.glow to every base image layer the
// item lowered. Base layers end in ":image", which keeps decorative recipe
// plates and backdrops out of the halo. The operation is a no-op when the
// payload has no image_glow_radius key, so existing plans are untouched.
func applyImageGlowToBaseLayers(ri resolvedItem, layers []Layer) ([]Layer, error) {
	raw, exists := ri.Params["image_glow_radius"]
	if !exists {
		return layers, nil
	}
	radius, ok := numericValue(raw)
	if !ok || !finite(radius) || radius < 0 || radius > maxRuntimeGlowSize {
		return nil, fmt.Errorf("overlay: image item %q image_glow_radius must be within [0,%g]", ri.Item.ID, maxRuntimeGlowSize)
	}
	if radius == 0 {
		return layers, nil
	}
	applied := false
	for index := range layers {
		layer := &layers[index]
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") {
			continue
		}
		if err := applyImageGlow(layer, ri.Params); err != nil {
			return nil, fmt.Errorf("overlay: image item %q: %w", ri.Item.ID, err)
		}
		applied = true
	}
	if !applied {
		return nil, fmt.Errorf("overlay: image item %q requests image.glow but lowered no base image layer", ri.Item.ID)
	}
	return layers, nil
}

// applyImageGlow validates and appends the native glow effect. Radius 0 or an
// explicit intensity of 0 disables the effect without emitting a zero-radius
// halo. Color is converted to Chronon's RGBA effect parameter.
func applyImageGlow(layer *Layer, params map[string]any) error {
	rawRadius, exists := params["image_glow_radius"]
	if !exists {
		return nil
	}
	radius, ok := numericValue(rawRadius)
	if !ok || !finite(radius) || radius < 0 || radius > maxRuntimeGlowSize {
		return fmt.Errorf("image_glow_radius must be within [0,%g]", maxRuntimeGlowSize)
	}
	if radius == 0 {
		return nil
	}
	intensity := 0.25
	if raw, exists := params["image_glow_intensity"]; exists {
		value, ok := numericValue(raw)
		if !ok || !finite(value) || value < 0 || value > maxRuntimeGlowIntensity {
			return fmt.Errorf("image_glow_intensity must be within [0,%g]", maxRuntimeGlowIntensity)
		}
		intensity = value
	}
	if intensity == 0 {
		return nil
	}
	color := "#FFFFFF"
	if raw, exists := params["image_glow_color"]; exists {
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("image_glow_color must be #RRGGBB")
		}
		color = value
	}
	rgba, err := parseHexColor(color)
	if err != nil {
		return fmt.Errorf("image_glow_color: %w", err)
	}
	layer.Effects = append(layer.Effects, LayerEffect{
		Type: "glow",
		Params: map[string]any{
			"radius":    radius,
			"intensity": intensity,
			"color":     rgba,
		},
	})
	return nil
}

// addStandaloneImageGrid lowers an item-level params.grid block as a
// full-canvas screen-space grid bound to the item lifetime. It is the image
// counterpart of plan-level clip_grid: same geometry contract (spacing, color,
// line/dot styling, opacity) plus the same scope switch
// (full_canvas/foreground). Unlike image_backdrop.grid it needs no blurred
// backdrop and works for standalone images, composites and entity cards.
func addStandaloneImageGrid(ri resolvedItem, src *semanticPlan, layers []Layer) ([]Layer, error) {
	raw, requested := ri.Params["grid"]
	if !requested || raw == nil {
		return layers, nil
	}
	if src == nil || src.Width <= 0 || src.Height <= 0 {
		return nil, fmt.Errorf("overlay: item %q grid requires a positive canvas", ri.Item.ID)
	}
	grid, err := parseStandaloneImageGrid(raw, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	if grid == nil {
		return layers, nil
	}
	gridLayer, err := compileBackdropGridWithScale(grid, src.Width, src.Height, ri.End-ri.Start, src.ForegroundScale)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q grid: %w", ri.Item.ID, err)
	}
	gridLayer.ID = ri.Item.ID + ":image-grid"
	gridLayer.StartFrame = ri.Start
	gridLayer.DurationFrames = ri.End - ri.Start
	return append(layers, gridLayer), nil
}

// parseStandaloneImageGrid decodes params.grid with the same bounds as
// image_backdrop.grid plus the clip_grid scope switch. A nil result means the
// block explicitly disabled the grid.
func parseStandaloneImageGrid(raw any, itemID string) (*semanticClipGrid, error) {
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q grid must be an object", itemID)
	}
	// Reuse the backdrop grid parser so bounds cannot drift; rewrite the item
	// id in errors from image_backdrop.grid to grid.
	grid, err := parseBackdropGrid(params, itemID)
	if err != nil {
		return nil, err
	}
	return grid, nil
}
