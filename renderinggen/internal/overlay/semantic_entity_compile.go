// semantic_entity_compile.go owns the entity-card compiler: a portrait and its
// optional entity caption, lowered per the entity style variant and the image
// preset. Dispatched from semantic_compile.go's compileItem.

package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/numeric"
)

// compileEntityCard lowers a portrait and its optional entity caption. The
// image preset owns the portrait geometry and motion; position_x/position_y
// can move the portrait so caption_layout can use the opposite side.
func compileEntityCard(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	style, hasStyle, err := resolveEntityCardStyle(&ri, src)
	if err != nil {
		return nil, err
	}
	resolvedImageMotion, err := resolveEntityCardImageMotion(&ri, src)
	if err != nil {
		return nil, err
	}
	img, err := buildEntityCardImage(&ri, src, registry, style, hasStyle, resolvedImageMotion)
	if err != nil {
		return nil, err
	}
	var premium *motion.MotionDefinition
	if resolvedImageMotion != nil {
		premium = premiumImageRecipeDefinition(resolvedImageMotion.definition)
	}
	layers, err := compilePremiumImageLayers(ri, src, img, premium)
	if err != nil {
		return nil, err
	}
	layers, err = appendEntityCardCaption(&ri, src, layers, premium)
	if err != nil {
		return nil, err
	}
	// An entity card is a full-canvas composition with one photo asset, so a
	// run-level image_backdrop must blur that same photo behind the card.
	return addImageBackdrops(ri, src, registry, layers)
}

func resolveEntityCardStyle(ri *resolvedItem, src *semanticPlan) (entityStyleVariant, bool, error) {
	var currentStyle entityStyleVariant
	if ri.Item.EntityStyleID == "" {
		return currentStyle, false, nil
	}
	imageMotionOverride := ri.Item.MotionID
	captionMotionOverride := ri.Item.CaptionMotionID
	captionMotionParams := ri.Item.CaptionMotionParams
	style, ok := ResolveEntityStyle(ri.Item.EntityStyleID, src.entityStyleSampleKey(), src.VideoID, ri.Item.ID)
	if !ok {
		style = entityStyleRegistry.sample(entityStyleRegistry.query(nil), "", src.entityStyleSampleKey(), src.VideoID, ri.Item.ID)
	}
	if badgeColor := entityBadgeColorFromStyle(ri.Item.Style); badgeColor != "" {
		captionColor, err := highContrastCaptionColor(badgeColor)
		if err != nil {
			return entityStyleVariant{}, false, fmt.Errorf("overlay: entity item %q badge color: %w", ri.Item.ID, err)
		}
		style.IsBadge = true
		style.BadgeColor = badgeColor
		style.CaptionColor = captionColor
		ri.Item.CaptionColor = captionColor
	}
	currentStyle = style
	ri.Item.MotionID = style.ImageMotionID
	if imageMotionOverride != "" {
		ri.Item.MotionID = imageMotionOverride
	}
	ri.Item.CaptionMotionID = style.CaptionMotionID
	if captionMotionOverride != "" {
		ri.Item.CaptionMotionID = captionMotionOverride
	}
	ri.Item.CaptionMotionParams = captionMotionParams
	// SSOT typography: the payload's explicit typeface/color/layout always win
	// over the preset default; the style only fills a missing choice.
	if strings.TrimSpace(ri.Item.CaptionColor) == "" {
		ri.Item.CaptionColor = style.CaptionColor
	}
	if strings.TrimSpace(ri.Item.CaptionLayout) == "" {
		ri.Item.CaptionLayout = style.CaptionLayout
	}
	x := 0.0
	switch style.ImageSide {
	case "left":
		x = -float64(src.Width) * 0.25
	case "right":
		x = float64(src.Width) * 0.25
	case "center", "below":
		x = 0.0
	default:
		return entityStyleVariant{}, false, fmt.Errorf("overlay: entity style %q has unsupported image side %q", style.Name, style.ImageSide)
	}
	if _, explicit := numericValue(ri.Params["position_x"]); !explicit {
		ri.Params["position_x"] = x
	}
	if _, explicit := numericValue(ri.Params["position_y"]); !explicit {
		ri.Params["position_y"] = 0.0
	}
	return currentStyle, true, nil
}

func resolveEntityCardImageMotion(ri *resolvedItem, src *semanticPlan) (*resolvedMotion, error) {
	if ri.Item.MotionID == "" && ri.ImagePreset.ID != "" {
		ri.Item.MotionID = runtimeDefaultImageMotionID(src, ri.Item.ID)
	}
	resolvedImageMotion, err := resolveRegisteredMotion(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: entity image motion %q: %w", ri.Item.MotionID, err)
	}
	return resolvedImageMotion, nil
}

func buildEntityCardImage(ri *resolvedItem, src *semanticPlan, registry *assetRegistry, style entityStyleVariant, hasStyle bool, resolvedImageMotion *resolvedMotion) (Layer, error) {
	img := imageLayer(*ri, registry.Path(ri.Item.Assets[0].ID))
	if hasStyle && style.CameraMotionID != "" {
		img.Enable3D = true
	}
	if ri.ImagePreset.ID != "" {
		applyPresetDefinition(&img, ri.ImagePreset)
		var imgAnimation *LayerAnimation
		var err error
		if ri.Item.MotionID != "" {
			imgAnimation, err = imageMotionAnimationResolved(ri.Item.MotionID, ri.Item.MotionParams, ri.End-ri.Start, ri.ImagePreset.Motion.Exit, ri.Item.ID, TargetImage, resolvedImageMotion)
		} else {
			imgAnimation, err = animationForPreset(ri.ImagePreset, "", ri.End-ri.Start)
		}
		if err != nil {
			return Layer{}, err
		}
		applyMotionRouting(&img, imgAnimation)
		img.Position = []float64{0, 0}
		img.Fit = FitContain
	}
	defaultImageBox(&img)
	if x, ok := numericValue(ri.Params["position_x"]); ok {
		y, _ := numericValue(ri.Params["position_y"])
		img.Position = []float64{x, y}
	}
	if strings.TrimSpace(ri.Item.EntityCaption) != "" {
		img.EntityImage = true
	}
	if err := applyFrameTreatment(&img, ri.Item.Frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: entity image item %q: %w", ri.Item.ID, err)
	}
	if err := applyProbedImageGeometry(&img, src.assetSizes); err != nil {
		return Layer{}, err
	}
	if ri.Item.MotionID != "" && ri.ImagePreset.ID == "" {
		return Layer{}, fmt.Errorf("overlay: entity image item %q with motion_id requires image_preset_id", ri.Item.ID)
	}
	return img, nil
}

func appendEntityCardCaption(ri *resolvedItem, src *semanticPlan, layers []Layer, premium *motion.MotionDefinition) ([]Layer, error) {
	if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
		imageIndex := premiumImageLayerIndex(layers)
		captionItem := ri.Item
		captionLayer, err := compileEntityCaptionLayer(*ri, src, captionItem, caption, &layers[imageIndex], "entity")
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

// The entity_group lowering: a multi-entity
// composite whose members are placed in deterministic canvas slots.
//
// A group item carries 2..5 image_layers members; each member keeps its own
// asset, preset, motion, caption and timing, exactly like an ordinary
// composite child. The group decides slot geometry via params.group_layout
// ("auto" by member count, or "duo"/"trio"/"quad"/"penta" which must match
// the count), using the same certified card table as image-group recipes.
// Explicit per-child position_x/position_y (or width+height) always win over
// the slot defaults. Anything else fails closed.
// groupLayoutNames is the closed layout vocabulary. "auto" resolves from the
// member count; every other value must match it exactly.
var groupLayoutNames = map[string]int{
	"auto":  0,
	"duo":   2,
	"trio":  3,
	"quad":  4,
	"penta": 5,
}

// compileEntityGroup validates the group contract, applies slot defaults and
// delegates member lowering to the shared composite-member primitive. An entity
// group is a current producer construct and owns its own layout, not a legacy
// pre-group_motion_id replay.
func compileEntityGroup(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	n := len(ri.Item.ImageLayers)
	if n < 2 || n > 5 {
		return nil, fmt.Errorf("overlay: entity group item %q requires 2..5 image_layers members, got %d", ri.Item.ID, n)
	}
	if raw, ok := ri.Params["group_layout"]; ok {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q group_layout must be a string", ri.Item.ID)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		want, ok := groupLayoutNames[name]
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q has unsupported group_layout %q", ri.Item.ID, name)
		}
		if want != 0 && want != n {
			return nil, fmt.Errorf("overlay: entity group item %q layout %q needs %d members, got %d", ri.Item.ID, name, want, n)
		}
	}
	sx := float64(src.Width) / 1920.0
	sy := float64(src.Height) / 1080.0
	grouped := ri
	grouped.Item.ImageLayers = make([]SemanticImageLayer, n)
	for i, child := range ri.Item.ImageLayers {
		grouped.Item.ImageLayers[i] = child
		slot, ok := groupMemberSlot(n, i)
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q has no card slot for member %d of %d", ri.Item.ID, i, n)
		}
		params := child.Params
		if params == nil {
			params = map[string]any{}
		} else {
			cp := make(map[string]any, len(params)+4)
			for k, v := range params {
				cp[k] = v
			}
			params = cp
		}
		if _, ok := params["position_x"]; !ok {
			params["position_x"] = slot.CenterX * sx
			params["position_y"] = slot.CenterY * sy
		}
		// Card size is a slot default too, but an authored width/height pair
		// keeps the member's own box and fit (the canary sets 420x420).
		_, hasWidth := params["width"]
		_, hasHeight := params["height"]
		if !hasWidth && !hasHeight {
			params["width"] = slot.Width * sx
			params["height"] = slot.Height * sy
			if _, ok := params["fit"]; !ok {
				params["fit"] = FitCover
			}
		}
		grouped.Item.ImageLayers[i].Params = params
	}
	// Entity-group members carry their own per-child motion and are decorated
	// by the shared member primitive. The item-level premium recipe is resolved
	// here exactly like the sealed composite path resolved it, so a group item
	// that names a premium image motion keeps that decoration.
	_, imageRecipe, err := resolveImageRecipeMotion(grouped.Item.MotionID, false)
	if err != nil {
		return nil, err
	}
	layers, gridSource, err := compileCompositeImageMembers(grouped, src, registry, premiumImageRecipeDefinition(imageRecipe))
	if err != nil {
		return nil, err
	}
	// One grid per item: a child-declared grid lowers once, and only when the
	// item itself does not declare params.grid (the caller lowers that one).
	if _, itemGrid := ri.Params["grid"]; !itemGrid && gridSource != nil {
		return addStandaloneImageGrid(*gridSource, src, layers)
	}
	return layers, nil
}

// semantic_entity_compile.go lowers the resolved items into layers. It owns the
// per-item contract (compile, optional media stroke, ledger counter, P4 window,
// canvas extension) and the camera observation that must happen for every item
// before the single camera authority is applied. Extracted from
// compileSemanticDecoded so that function stays a list of plan stages.

func appendResolvedItemLayers(plan *Plan, resolved []resolvedItem, src *semanticPlan, registry *assetRegistry, geoRegistry *geofeatures.Registry, cameras *cameraControllerSet, stats *Stats) error {
	for _, ri := range resolved {
		if err := cameras.observe(ri, src); err != nil {
			return err
		}
		layers, err := compileItem(ri, src, registry, geoRegistry)
		if err != nil {
			return err
		}

		for _, layer := range layers {
			// A premium image motion can expand one item into several linked
			// layers. Attach a clip stroke to the actual image layer only.
			plan.Layers = append(plan.Layers, layer)
			if layer.FrameStroke != nil && layer.Type != "shape" {
				strokeLayer, err := compileMediaStrokeLayer(layer, src.Width, src.Height)
				if err != nil {
					return fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
				}
				plan.Layers = append(plan.Layers, strokeLayer)
			}
		}
		// Stroke shapes require render-plan.v3 even though the semantic item
		// itself is a media layer.
		stats.addResolved(ri)
		// Transient per-item window for the P4 segmented-output planner,
		// booked from the SAME resolvedItem the layers were lowered from.
		plan.ItemWindows = append(plan.ItemWindows, ItemWindow{
			ItemID: ri.Item.ID, StartFrame: ri.Start, EndFrame: ri.End})
		if ri.End > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = ri.End
		}
	}
	return nil
}

var supportedShapeTypes = map[string]struct{}{
	"rect": {}, "rounded_rect": {}, "ellipse": {}, "line": {}, "path": {},
	"star": {}, "polygon": {}, "arrow": {}, "arc": {}, "grid": {},
	"dot_grid": {}, "device_frame": {}, "world_map": {}, "chart": {},
}

func isSupportedShapeType(t string) bool {
	_, ok := supportedShapeTypes[t]
	return ok
}

func isChartBarItem(item resolvedItem) bool {
	if item.Kind != KindShape || item.Params == nil {
		return false
	}
	marked, ok := item.Params["chart_bar"].(bool)
	return ok && marked
}

func finite(value float64) bool { return numeric.Finite(value) }

func validateRGBA(color []float64) error {
	if len(color) != 4 {
		return fmt.Errorf("expected RGBA[4], got %d components", len(color))
	}
	for index, component := range color {
		if !finite(component) || component < 0 || component > 1 {
			return fmt.Errorf("component %d must be finite and in [0, 1]", index)
		}
	}
	return nil
}

func validatePair(raw any, itemID, name string) ([]float64, error) {
	pair, err := parse2Floats(raw)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q %s must contain two finite numbers", itemID, name)
	}
	return pair, nil
}

func isSupportedPathCommand(command string) bool {
	switch command {
	case "move_to", "line_to", "quadratic_to", "cubic_to", "close", "M", "L", "Q", "C", "Z":
		return true
	default:
		return false
	}
}

func validateDimension(value float64, name, itemID string) error {
	if !finite(value) || value <= 0 {
		return fmt.Errorf("overlay: item %q requires positive finite %s", itemID, name)
	}
	return nil
}

func validateShapeContract(shape *LayerShape, width, height float64, itemID string) error {
	if err := validateRGBAFromShape(shape); err != nil {
		return fmt.Errorf("overlay: item %q shape.fill is invalid: %w", itemID, err)
	}
	if shape.Radius < 0 || !finite(shape.Radius) {
		return fmt.Errorf("overlay: item %q shape.radius must be non-negative and finite", itemID)
	}
	maxRadius := math.Min(width, height) * 0.5
	for _, radius := range shape.CornerRadius {
		if radius < 0 || radius > maxRadius || !finite(radius) {
			return fmt.Errorf("overlay: item %q corner_radius values must be in [0, %v]", itemID, maxRadius)
		}
	}
	if shape.Stroke != nil {
		if shape.Stroke.Width < 0 || !finite(shape.Stroke.Width) {
			return fmt.Errorf("overlay: item %q stroke width must be non-negative and finite", itemID)
		}
		if _, err := parseHexColor(shape.Stroke.Color); err != nil {
			return fmt.Errorf("overlay: item %q stroke color is invalid: %w", itemID, err)
		}
	}
	return nil
}

func validateRGBAFromShape(shape *LayerShape) error {
	fill, ok := shape.Fill.([]float64)
	if !ok || fill == nil {
		return nil
	}
	return validateRGBA(fill)
}

func firstParam(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// optionalShapeNumber reads the first present key and validates one numeric
// shape parameter. max=+Inf means "finite only" (and min=0 adds the usual
// non-negative wording); every bounded field reports the same interval format.
func optionalShapeNumber(params map[string]any, itemID, name string, min, max float64, keys ...string) (float64, bool, error) {
	raw, ok := firstParam(params, keys...)
	if !ok {
		return 0, false, nil
	}
	value, err := toFloat(raw)
	if err != nil || !finite(value) || value < min || value > max {
		switch {
		case math.IsInf(max, 1) && min == 0:
			return 0, true, fmt.Errorf("overlay: item %q shape.%s must be non-negative and finite", itemID, name)
		case math.IsInf(max, 1):
			return 0, true, fmt.Errorf("overlay: item %q shape.%s must be finite", itemID, name)
		default:
			return 0, true, fmt.Errorf("overlay: item %q shape.%s %v must be in [%g, %g]", itemID, name, raw, min, max)
		}
	}
	return value, true, nil
}

// resolveShape is the one lowering boundary for shape items: semantic params
// enter here, are validated fail-closed, and leave as the normalized Chronon
// layer representation.
func resolveShape(ri resolvedItem, src *semanticPlan) (Layer, error) {
	if ri.Params == nil {
		return Layer{}, fmt.Errorf("overlay: item %q shape params are required", ri.Item.ID)
	}
	shapeType := stringParam(ri.Params, "shape", stringParam(ri.Params, "type", "rect"))
	if raw, exists := ri.Params["shape"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape must be a string", ri.Item.ID)
		}
	}
	if raw, exists := ri.Params["type"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape.type must be a string", ri.Item.ID)
		}
	}
	if rawShape, hasShape := ri.Params["shape"]; hasShape {
		if rawType, hasType := ri.Params["type"]; hasType && rawType != rawShape {
			return Layer{}, fmt.Errorf("overlay: item %q shape and type disagree", ri.Item.ID)
		}
	}
	if shapeType == "dotgrid" {
		shapeType = "dot_grid"
	} else if shapeType == "worldmap" {
		shapeType = "world_map"
	}
	if !isSupportedShapeType(shapeType) {
		return Layer{}, fmt.Errorf("overlay: item %q has unsupported shape type %q", ri.Item.ID, shapeType)
	}

	width, height, pos, err := resolveShapeCanvas(ri, src)
	if err != nil {
		return Layer{}, err
	}

	shape := &LayerShape{Type: shapeType}

	if err := applyShapeProceduralParams(shape, ri, shapeType, width, height); err != nil {
		return Layer{}, err
	}

	if err := applyWorldMapParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}
	if err := applyArrowParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}
	if err := applyChartParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}

	if pathValue, exists := ri.Params["path"]; exists {
		path, err := parsePath(pathValue, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Path = path
	}

	effects, err := applyShapePaint(shape, ri, shapeType)
	if err != nil {
		return Layer{}, err
	}

	layer := Layer{
		ID:             ri.Item.ID,
		Type:           "shape",
		Size:           []float64{width, height},
		Position:       pos,
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
		Shape:          shape,
		Effects:        effects,
	}

	if err := validateShapeContract(shape, width, height, ri.Item.ID); err != nil {
		return Layer{}, err
	}
	if op, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "opacity", 0, 1, "opacity"); err != nil {
		return Layer{}, err
	} else if ok {
		layer.Opacity = &op
	}
	return layer, nil
}
