package motion

import (
	"fmt"
	"math"
)

// validateImagePremiumV1 is the family gate for image_premium_v1 recipes. It is
// a sequence of independent checks — base contract, targets, recipe shape,
// per-component shape/paint/motion, mask — so a failure names the exact layer
// that is wrong instead of hiding inside one 280-line function.
func validateImagePremiumV1(d MotionDefinition) error {
	if d.Category != "image_premium_v1" {
		// Visual Accents V1 families are recipe-driven by design and carry their
		// own family-shaped gate in validateVisualAccentsV1Family; every other
		// category must stay recipe-free so a recipe cannot silently ship
		// outside a certified lowering path.
		if d.ImageRecipe != nil && !VisualAccentsV1CategoriesContains(d.Category) {
			return fmt.Errorf("motion %q: image_recipe is only valid in image_premium_v1", d.ID)
		}
		return nil
	}
	if d.ImageRecipe == nil || d.Unit != "layer" || d.RenderSafe == nil || !*d.RenderSafe ||
		d.Requires3D == nil || d.RequiresCamera == nil || d.DurationBounds == nil ||
		d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames ||
		d.Enter <= 0 || int64(d.Enter) > d.DurationBounds.MaximumFrames || d.Exit < 0 ||
		int64(d.Exit) >= d.DurationBounds.MinimumFrames ||
		len(d.Tracks) == 0 || len(d.ImageRecipe.Components) == 0 ||
		len(d.SupportedContent) == 0 || !containsMotionString(d.SupportedContent, "image") ||
		len(d.RequiredProperties) == 0 {
		return fmt.Errorf("motion %q: image_premium_v1 requires a layer recipe, render safety, enter and valid duration bounds", d.ID)
	}
	if err := validatePremiumTargets(d); err != nil {
		return err
	}
	has3D, err := validatePremiumImageTracks(d)
	if err != nil {
		return err
	}
	recipe := d.ImageRecipe
	if err := validatePremiumRecipeShape(d, recipe); err != nil {
		return err
	}
	has3D, err = validateImageMotionTracks(d.ID, "inactive image track", recipe.InactiveTracks, has3D)
	if err != nil {
		return err
	}
	has3D, err = validateImageMotionTracks(d.ID, "caption track", recipe.CaptionTracks, has3D)
	if err != nil {
		return err
	}
	has3D, err = validatePremiumComponents(d, recipe, has3D)
	if err != nil {
		return err
	}
	if err := validatePremiumMask(d, recipe); err != nil {
		return err
	}
	if *d.Requires3D != has3D {
		return fmt.Errorf("motion %q: requires_3d=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.Requires3D, has3D)
	}
	if *d.RequiresCamera != has3D {
		return fmt.Errorf("motion %q: requires_camera=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.RequiresCamera, has3D)
	}
	return nil
}

// validatePremiumTargets pins that a premium recipe targets images and nothing
// else: a text or map target would route a recipe through a lowering that does
// not exist.
func validatePremiumTargets(d MotionDefinition) error {
	imageTarget := false
	for _, target := range d.Targets {
		imageTarget = imageTarget || target == "image"
	}
	if !imageTarget {
		return fmt.Errorf("motion %q: image_premium_v1 must target image", d.ID)
	}
	for _, target := range d.Targets {
		if target != "image" {
			return fmt.Errorf("motion %q: image_premium_v1 has unsupported target %q", d.ID, target)
		}
	}
	return nil
}

// validatePremiumImageTracks validates the layer's own tracks and pins that
// required_properties is exactly the declared track set, with no duplicates.
func validatePremiumImageTracks(d MotionDefinition) (bool, error) {
	has3D := false
	trackProperties := make(map[string]bool, len(d.Tracks))
	for _, track := range d.Tracks {
		if err := validateImageMotionTrack(d.ID, "image track", track); err != nil {
			return false, err
		}
		trackProperties[track.Property] = true
		has3D = has3D || IsCameraBacked3DProperty(track.Property)
	}
	required := make(map[string]bool, len(d.RequiredProperties))
	for _, property := range d.RequiredProperties {
		if !trackProperties[property] || required[property] {
			return false, fmt.Errorf("motion %q: required property %q is missing or duplicated", d.ID, property)
		}
		required[property] = true
	}
	if len(required) != len(trackProperties) {
		return false, fmt.Errorf("motion %q: required_properties must list every image track property", d.ID)
	}
	return has3D, nil
}

// validatePremiumRecipeShape covers the recipe-level flags that are not about a
// single component: multi-image stacking, the reserved active-layer parameter
// and the caption requirement.
func validatePremiumRecipeShape(d MotionDefinition, recipe *ImageMotionRecipe) error {
	if recipe.Stack && recipe.ActiveLayerParam == "" {
		return fmt.Errorf("motion %q: multi-image recipes must name the active_layer_id parameter", d.ID)
	}
	if !recipe.Stack && recipe.ActiveLayerParam != "" {
		return fmt.Errorf("motion %q: active_layer_param requires a multi-image recipe", d.ID)
	}
	if recipe.Stack && recipe.ActiveLayerParam != "active_layer_id" {
		return fmt.Errorf("motion %q: multi-image recipe must use active_layer_id", d.ID)
	}
	if recipe.RequireCaption && (len(recipe.CaptionTracks) == 0 || !containsMotionString(d.Targets, "image")) {
		return fmt.Errorf("motion %q: required image captions need an image target and caption_tracks", d.ID)
	}
	return nil
}

// validatePremiumComponents walks the recipe's components: ids must be unique
// before anything else, because every later error names the component.
func validatePremiumComponents(d MotionDefinition, recipe *ImageMotionRecipe, has3D bool) (bool, error) {
	componentIDs := make(map[string]bool, len(recipe.Components))
	for _, component := range recipe.Components {
		if component.ID == "" || componentIDs[component.ID] {
			return false, fmt.Errorf("motion %q: image recipe component ids must be non-empty and unique", d.ID)
		}
		componentIDs[component.ID] = true
		component3D, err := validatePremiumComponent(d.ID, component)
		if err != nil {
			return false, err
		}
		has3D = has3D || component3D
	}
	return has3D, nil
}

// validatePremiumComponent validates one component in three independent groups:
// geometry/type, paint, and animation. Each group returns the 3D routing it
// implies so requires_3d/requires_camera can be checked once at the end.
func validatePremiumComponent(id string, component ImageMotionComponent) (bool, error) {
	if err := validatePremiumComponentShape(id, component); err != nil {
		return false, err
	}
	has3D := component.ZOffset != 0
	if err := validatePremiumComponentPaint(id, component); err != nil {
		return false, err
	}
	track3D, err := validatePremiumComponentMotion(id, component)
	if err != nil {
		return false, err
	}
	return has3D || track3D, nil
}

// validatePremiumComponentShape validates what the component IS: supported type,
// shape vocabulary, placement, bounded geometry, and the transform-sync rules.
func validatePremiumComponentShape(id string, component ImageMotionComponent) error {
	if component.Type != "shape" && component.Type != "color" {
		return fmt.Errorf("motion %q: component %q has unsupported type %q", id, component.ID, component.Type)
	}
	if component.Type == "shape" && component.Shape != "rounded_rect" && component.Shape != "ellipse" && component.Shape != "path" && component.Shape != "rect" {
		return fmt.Errorf("motion %q: component %q has unsupported shape %q", id, component.ID, component.Shape)
	}
	if component.Placement != "" && component.Placement != "before_image" && component.Placement != "after_image" {
		return fmt.Errorf("motion %q: component %q placement must be before_image or after_image", id, component.ID)
	}
	if len(component.SizeScale) != 0 && len(component.SizeScale) != 2 {
		return fmt.Errorf("motion %q: component %q size_scale must contain two values", id, component.ID)
	}
	for _, value := range component.SizeScale {
		if !finiteMotionValue(value) || value <= 0 {
			return fmt.Errorf("motion %q: component %q size_scale values must be finite and positive", id, component.ID)
		}
	}
	if len(component.PositionOffset) != 0 && len(component.PositionOffset) != 2 {
		return fmt.Errorf("motion %q: component %q position_offset must contain two values", id, component.ID)
	}
	for _, value := range component.PositionOffset {
		if !finiteMotionValue(value) {
			return fmt.Errorf("motion %q: component %q position_offset values must be finite", id, component.ID)
		}
	}
	if !finiteMotionValue(component.ZOffset) || !finiteMotionValue(component.RadiusScale) || component.RadiusScale < 0 {
		return fmt.Errorf("motion %q: component %q offsets and radius scale must be finite; radius scale cannot be negative", id, component.ID)
	}
	if component.SyncTransform && component.Type != "shape" {
		return fmt.Errorf("motion %q: only shape components can synchronize an image transform", id)
	}
	if component.SyncTransform && len(component.Tracks) > 0 {
		return fmt.Errorf("motion %q: synchronized frame %q cannot override the source transform with component tracks", id, component.ID)
	}
	if component.Type == "color" && component.CanvasSize && component.Shape != "" {
		return fmt.Errorf("motion %q: canvas color component %q cannot declare a shape", id, component.ID)
	}
	if component.CanvasSize && component.Type != "color" {
		return fmt.Errorf("motion %q: only color components may cover the full canvas", id)
	}
	if component.Type == "shape" && component.Shape == "path" && component.PathKind != "rounded_rect" && component.PathKind != "line" {
		return fmt.Errorf("motion %q: component %q path requires a supported path_kind", id, component.ID)
	}
	if component.Type == "shape" && component.Shape != "path" && component.PathKind != "" {
		return fmt.Errorf("motion %q: component %q path_kind is only valid on path shapes", id, component.ID)
	}
	if component.Type == "shape" && component.Shape == "rect" && component.RadiusScale != 0 {
		return fmt.Errorf("motion %q: rect component %q cannot set a corner radius scale", id, component.ID)
	}
	return nil
}

// validatePremiumComponentPaint validates the component's pixels: fills, stroke
// and gradient. A shape with no paint at all would rasterize as nothing.
func validatePremiumComponentPaint(id string, component ImageMotionComponent) error {
	if len(component.Fill) != 0 {
		if len(component.Fill) != 4 {
			return fmt.Errorf("motion %q: component %q fill must be RGBA", id, component.ID)
		}
		for _, value := range component.Fill {
			if !finiteMotionValue(value) || value < 0 || value > 1 {
				return fmt.Errorf("motion %q: component %q fill channels must be in [0, 1]", id, component.ID)
			}
		}
	}
	if component.Opacity != nil && (!finiteMotionValue(*component.Opacity) || *component.Opacity < 0 || *component.Opacity > 1) {
		return fmt.Errorf("motion %q: component %q opacity must be in [0, 1]", id, component.ID)
	}
	if component.Type == "color" && len(component.Fill) != 4 {
		return fmt.Errorf("motion %q: color component %q requires RGBA fill", id, component.ID)
	}
	if component.Type == "shape" && len(component.Fill) == 0 && component.Gradient == nil && component.Stroke == nil {
		return fmt.Errorf("motion %q: shape component %q needs a fill, gradient or stroke", id, component.ID)
	}
	if component.Type == "shape" && component.Shape == "path" && component.Stroke == nil {
		return fmt.Errorf("motion %q: path component %q requires a stroke", id, component.ID)
	}
	if component.Stroke != nil {
		if component.Stroke.Width < 0 || !finiteMotionValue(component.Stroke.Width) || !validMotionHexColor(component.Stroke.Color) {
			return fmt.Errorf("motion %q: component %q stroke needs a hex color and finite non-negative width", id, component.ID)
		}
	}
	return validatePremiumGradient(id, component)
}

// validatePremiumGradient validates a linear gradient: two finite endpoints, at
// least two stops, non-decreasing positions and in-gamut RGBA colors.
func validatePremiumGradient(id string, component ImageMotionComponent) error {
	gradient := component.Gradient
	if gradient == nil {
		return nil
	}
	if gradient.Type != "linear" {
		return fmt.Errorf("motion %q: component %q has unsupported gradient type %q (only linear is currently lowered)", id, component.ID, gradient.Type)
	}
	if len(gradient.Start) != 2 || len(gradient.End) != 2 {
		return fmt.Errorf("motion %q: component %q linear gradient requires start and end points", id, component.ID)
	}
	for _, point := range [][]float64{gradient.Start, gradient.End} {
		for _, value := range point {
			if !finiteMotionValue(value) || math.Abs(value) > 1000000 {
				return fmt.Errorf("motion %q: component %q gradient geometry is invalid", id, component.ID)
			}
		}
	}
	if len(gradient.Stops) < 2 {
		return fmt.Errorf("motion %q: component %q gradient needs at least two color stops", id, component.ID)
	}
	previousStop := -1.0
	for _, stop := range gradient.Stops {
		if !finiteMotionValue(stop.Position) || stop.Position < 0 || stop.Position > 1 || stop.Position < previousStop || len(stop.Color) != 4 {
			return fmt.Errorf("motion %q: component %q gradient stop is invalid", id, component.ID)
		}
		for _, value := range stop.Color {
			if !finiteMotionValue(value) || value < 0 || value > 1 {
				return fmt.Errorf("motion %q: component %q gradient color channels must be in [0, 1]", id, component.ID)
			}
		}
		previousStop = stop.Position
	}
	return nil
}

// validatePremiumComponentMotion validates the animation the component carries:
// tracks, animatable effect parameters, path trim and effects.
func validatePremiumComponentMotion(id string, component ImageMotionComponent) (bool, error) {
	has3D, err := validateImageMotionTracks(id, "component "+component.ID, component.Tracks, false)
	if err != nil {
		return false, err
	}
	for _, paramTrack := range component.EffectParamTracks {
		if paramTrack.EffectID == "" || paramTrack.Param == "" {
			return false, fmt.Errorf("motion %q: component %q effect parameter tracks require effect_id and param", id, component.ID)
		}
		if err := validateImageMotionTrack(id, "component "+component.ID+" effect parameter", paramTrack.Track); err != nil {
			return false, err
		}
		if !imageRecipeHasEffect(component, paramTrack.EffectID) {
			return false, fmt.Errorf("motion %q: component %q effect parameter track targets undeclared effect %q", id, component.ID, paramTrack.EffectID)
		}
		if paramTrack.EffectID != "light.glow" || paramTrack.Param != "intensity" && paramTrack.Param != "radius" {
			return false, fmt.Errorf("motion %q: component %q uses unsupported animatable effect parameter %s.%s", id, component.ID, paramTrack.EffectID, paramTrack.Param)
		}
		for _, keyframe := range paramTrack.Track.Keyframes {
			value, ok := motionNumericValue(keyframe.Value)
			maximum := 4.0
			if paramTrack.Param == "radius" {
				maximum = 256
			}
			if !ok || value < 0 || value > maximum {
				return false, fmt.Errorf("motion %q: component %q %s.%s samples must be in [0, %g]", id, component.ID, paramTrack.EffectID, paramTrack.Param, maximum)
			}
		}
	}
	if component.Trim != nil {
		if !finiteMotionValue(component.Trim.Start) || !finiteMotionValue(component.Trim.End) || component.Trim.Start < 0 || component.Trim.Start > component.Trim.End || component.Trim.End > 1 {
			return false, fmt.Errorf("motion %q: component %q trim bounds must satisfy 0 <= start <= end <= 1", id, component.ID)
		}
		if component.Shape != "path" {
			return false, fmt.Errorf("motion %q: component %q trim requires a path shape", id, component.ID)
		}
		if err := validateImageMotionTrack(id, "component "+component.ID+" trim", component.Trim.Animation); err != nil {
			return false, err
		}
		for _, keyframe := range component.Trim.Animation.Keyframes {
			values, ok := motionNumericVector(keyframe.Value)
			if !ok || len(values) < 2 || len(values) > 3 || values[0] < 0 || values[0] > 1 || values[1] < values[0] || values[1] > 1 || (len(values) > 2 && math.Abs(values[2]) > 1000000) {
				return false, fmt.Errorf("motion %q: component %q trim animation requires bounded [start,end,offset?] values", id, component.ID)
			}
		}
	}
	for _, effect := range component.Effects {
		if effect.Type == "" || !imageRecipeEffectSupported(effect.Type) {
			return false, fmt.Errorf("motion %q: component %q has unsupported effect type %q", id, component.ID, effect.Type)
		}
		if err := validateImageRecipeEffect(id, component.ID, effect); err != nil {
			return false, err
		}
	}
	for _, track := range component.EffectParamTracks {
		has3D = has3D || IsCameraBacked3DProperty(track.Track.Property)
	}
	return has3D, nil
}

// validatePremiumMask validates the optional image mask: a bounded wipe_left
// recipe whose progress is a mask_morph track in [0, 1].
func validatePremiumMask(d MotionDefinition, recipe *ImageMotionRecipe) error {
	if recipe.Mask == nil {
		return nil
	}
	if recipe.Mask.Kind != "wipe_left" || !finiteMotionValue(recipe.Mask.Feather) || recipe.Mask.Feather < 0 || recipe.Mask.Feather > 1000000 || !containsMotionString(d.Targets, "image") {
		return fmt.Errorf("motion %q: image mask must be a bounded wipe_left recipe", d.ID)
	}
	if err := validateTrackDefinition(d.ID, "image mask progress", recipe.Mask.Progress); err != nil {
		return err
	}
	if recipe.Mask.Progress.Property != "mask_morph" || recipe.Mask.Progress.Easing != "" && !validMotionEasing(recipe.Mask.Progress.Easing) {
		return fmt.Errorf("motion %q: image mask progress must use mask_morph with a supported easing", d.ID)
	}
	for _, keyframe := range recipe.Mask.Progress.Keyframes {
		value, ok := motionNumericValue(keyframe.Value)
		if !ok || value < 0 || value > 1 {
			return fmt.Errorf("motion %q: image mask progress keyframes must be in [0, 1]", d.ID)
		}
	}
	return nil
}
