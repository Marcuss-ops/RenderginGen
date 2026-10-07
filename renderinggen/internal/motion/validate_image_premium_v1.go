package motion

import (
	"fmt"
	"math"
)

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
	has3D := false
	trackProperties := make(map[string]bool, len(d.Tracks))
	for _, track := range d.Tracks {
		if err := validateImageMotionTrack(d.ID, "image track", track); err != nil {
			return err
		}
		trackProperties[track.Property] = true
		has3D = has3D || IsCameraBacked3DProperty(track.Property)
	}
	required := make(map[string]bool, len(d.RequiredProperties))
	for _, property := range d.RequiredProperties {
		if !trackProperties[property] || required[property] {
			return fmt.Errorf("motion %q: required property %q is missing or duplicated", d.ID, property)
		}
		required[property] = true
	}
	if len(required) != len(trackProperties) {
		return fmt.Errorf("motion %q: required_properties must list every image track property", d.ID)
	}
	recipe := d.ImageRecipe
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
	var err error
	has3D, err = validateImageMotionTracks(d.ID, "inactive image track", recipe.InactiveTracks, has3D)
	if err != nil {
		return err
	}
	has3D, err = validateImageMotionTracks(d.ID, "caption track", recipe.CaptionTracks, has3D)
	if err != nil {
		return err
	}
	componentIDs := make(map[string]bool, len(recipe.Components))
	for _, component := range recipe.Components {
		if component.ID == "" || componentIDs[component.ID] {
			return fmt.Errorf("motion %q: image recipe component ids must be non-empty and unique", d.ID)
		}
		componentIDs[component.ID] = true
		if component.Type != "shape" && component.Type != "color" {
			return fmt.Errorf("motion %q: component %q has unsupported type %q", d.ID, component.ID, component.Type)
		}
		if component.Type == "shape" && component.Shape != "rounded_rect" && component.Shape != "ellipse" && component.Shape != "path" && component.Shape != "rect" {
			return fmt.Errorf("motion %q: component %q has unsupported shape %q", d.ID, component.ID, component.Shape)
		}
		if component.Placement != "" && component.Placement != "before_image" && component.Placement != "after_image" {
			return fmt.Errorf("motion %q: component %q placement must be before_image or after_image", d.ID, component.ID)
		}
		if len(component.SizeScale) != 0 && len(component.SizeScale) != 2 {
			return fmt.Errorf("motion %q: component %q size_scale must contain two values", d.ID, component.ID)
		}
		for _, value := range component.SizeScale {
			if !finiteMotionValue(value) || value <= 0 {
				return fmt.Errorf("motion %q: component %q size_scale values must be finite and positive", d.ID, component.ID)
			}
		}
		if len(component.PositionOffset) != 0 && len(component.PositionOffset) != 2 {
			return fmt.Errorf("motion %q: component %q position_offset must contain two values", d.ID, component.ID)
		}
		for _, value := range component.PositionOffset {
			if !finiteMotionValue(value) {
				return fmt.Errorf("motion %q: component %q position_offset values must be finite", d.ID, component.ID)
			}
		}
		if !finiteMotionValue(component.ZOffset) || !finiteMotionValue(component.RadiusScale) || component.RadiusScale < 0 {
			return fmt.Errorf("motion %q: component %q offsets and radius scale must be finite; radius scale cannot be negative", d.ID, component.ID)
		}
		if len(component.Fill) != 0 {
			if len(component.Fill) != 4 {
				return fmt.Errorf("motion %q: component %q fill must be RGBA", d.ID, component.ID)
			}
			for _, value := range component.Fill {
				if !finiteMotionValue(value) || value < 0 || value > 1 {
					return fmt.Errorf("motion %q: component %q fill channels must be in [0, 1]", d.ID, component.ID)
				}
			}
		}
		if component.Opacity != nil && (!finiteMotionValue(*component.Opacity) || *component.Opacity < 0 || *component.Opacity > 1) {
			return fmt.Errorf("motion %q: component %q opacity must be in [0, 1]", d.ID, component.ID)
		}
		if component.SyncTransform && component.Type != "shape" {
			return fmt.Errorf("motion %q: only shape components can synchronize an image transform", d.ID)
		}
		if component.SyncTransform && len(component.Tracks) > 0 {
			return fmt.Errorf("motion %q: synchronized frame %q cannot override the source transform with component tracks", d.ID, component.ID)
		}
		if component.Type == "color" && component.CanvasSize && component.Shape != "" {
			return fmt.Errorf("motion %q: canvas color component %q cannot declare a shape", d.ID, component.ID)
		}
		if component.Type == "color" && len(component.Fill) != 4 {
			return fmt.Errorf("motion %q: color component %q requires RGBA fill", d.ID, component.ID)
		}
		if component.Type == "shape" && len(component.Fill) == 0 && component.Gradient == nil && component.Stroke == nil {
			return fmt.Errorf("motion %q: shape component %q needs a fill, gradient or stroke", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape == "path" && component.Stroke == nil {
			return fmt.Errorf("motion %q: path component %q requires a stroke", d.ID, component.ID)
		}
		if component.CanvasSize && component.Type != "color" {
			return fmt.Errorf("motion %q: only color components may cover the full canvas", d.ID)
		}
		if component.Type == "shape" && component.Shape == "path" && component.PathKind != "rounded_rect" && component.PathKind != "line" {
			return fmt.Errorf("motion %q: component %q path requires a supported path_kind", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape != "path" && component.PathKind != "" {
			return fmt.Errorf("motion %q: component %q path_kind is only valid on path shapes", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape == "rect" && component.RadiusScale != 0 {
			return fmt.Errorf("motion %q: rect component %q cannot set a corner radius scale", d.ID, component.ID)
		}
		if component.ZOffset != 0 {
			has3D = true
		}
		if component.Stroke != nil {
			if component.Stroke.Width < 0 || !finiteMotionValue(component.Stroke.Width) || !validMotionHexColor(component.Stroke.Color) {
				return fmt.Errorf("motion %q: component %q stroke needs a hex color and finite non-negative width", d.ID, component.ID)
			}
		}
		if component.Gradient != nil {
			if component.Gradient.Type != "linear" {
				return fmt.Errorf("motion %q: component %q has unsupported gradient type %q (only linear is currently lowered)", d.ID, component.ID, component.Gradient.Type)
			}
			if len(component.Gradient.Start) != 2 || len(component.Gradient.End) != 2 {
				return fmt.Errorf("motion %q: component %q linear gradient requires start and end points", d.ID, component.ID)
			}
			for _, point := range [][]float64{component.Gradient.Start, component.Gradient.End} {
				for _, value := range point {
					if !finiteMotionValue(value) || math.Abs(value) > 1000000 {
						return fmt.Errorf("motion %q: component %q gradient geometry is invalid", d.ID, component.ID)
					}
				}
			}
			if len(component.Gradient.Stops) < 2 {
				return fmt.Errorf("motion %q: component %q gradient needs at least two color stops", d.ID, component.ID)
			}
			previousStop := -1.0
			for _, stop := range component.Gradient.Stops {
				if !finiteMotionValue(stop.Position) || stop.Position < 0 || stop.Position > 1 || stop.Position < previousStop || len(stop.Color) != 4 {
					return fmt.Errorf("motion %q: component %q gradient stop is invalid", d.ID, component.ID)
				}
				for _, value := range stop.Color {
					if !finiteMotionValue(value) || value < 0 || value > 1 {
						return fmt.Errorf("motion %q: component %q gradient color channels must be in [0, 1]", d.ID, component.ID)
					}
				}
				previousStop = stop.Position
			}
		}
		has3D, err = validateImageMotionTracks(d.ID, "component "+component.ID, component.Tracks, has3D)
		if err != nil {
			return err
		}
		for _, paramTrack := range component.EffectParamTracks {
			if paramTrack.EffectID == "" || paramTrack.Param == "" {
				return fmt.Errorf("motion %q: component %q effect parameter tracks require effect_id and param", d.ID, component.ID)
			}
			if err := validateImageMotionTrack(d.ID, "component "+component.ID+" effect parameter", paramTrack.Track); err != nil {
				return err
			}
			if !imageRecipeHasEffect(component, paramTrack.EffectID) {
				return fmt.Errorf("motion %q: component %q effect parameter track targets undeclared effect %q", d.ID, component.ID, paramTrack.EffectID)
			}
			if paramTrack.EffectID != "light.glow" || paramTrack.Param != "intensity" && paramTrack.Param != "radius" {
				return fmt.Errorf("motion %q: component %q uses unsupported animatable effect parameter %s.%s", d.ID, component.ID, paramTrack.EffectID, paramTrack.Param)
			}
			for _, keyframe := range paramTrack.Track.Keyframes {
				value, ok := motionNumericValue(keyframe.Value)
				maximum := 4.0
				if paramTrack.Param == "radius" {
					maximum = 256
				}
				if !ok || value < 0 || value > maximum {
					return fmt.Errorf("motion %q: component %q %s.%s samples must be in [0, %g]", d.ID, component.ID, paramTrack.EffectID, paramTrack.Param, maximum)
				}
			}
		}
		if component.Trim != nil {
			if !finiteMotionValue(component.Trim.Start) || !finiteMotionValue(component.Trim.End) || component.Trim.Start < 0 || component.Trim.Start > component.Trim.End || component.Trim.End > 1 {
				return fmt.Errorf("motion %q: component %q trim bounds must satisfy 0 <= start <= end <= 1", d.ID, component.ID)
			}
			if component.Shape != "path" {
				return fmt.Errorf("motion %q: component %q trim requires a path shape", d.ID, component.ID)
			}
			if err := validateImageMotionTrack(d.ID, "component "+component.ID+" trim", component.Trim.Animation); err != nil {
				return err
			}
			for _, keyframe := range component.Trim.Animation.Keyframes {
				values, ok := motionNumericVector(keyframe.Value)
				if !ok || len(values) < 2 || len(values) > 3 || values[0] < 0 || values[0] > 1 || values[1] < values[0] || values[1] > 1 || (len(values) > 2 && math.Abs(values[2]) > 1000000) {
					return fmt.Errorf("motion %q: component %q trim animation requires bounded [start,end,offset?] values", d.ID, component.ID)
				}
			}
		}
		for _, effect := range component.Effects {
			if effect.Type == "" || !imageRecipeEffectSupported(effect.Type) {
				return fmt.Errorf("motion %q: component %q has unsupported effect type %q", d.ID, component.ID, effect.Type)
			}
			if err := validateImageRecipeEffect(d.ID, component.ID, effect); err != nil {
				return err
			}
		}
		for _, track := range component.EffectParamTracks {
			has3D = has3D || IsCameraBacked3DProperty(track.Track.Property)
		}
	}
	if recipe.Mask != nil {
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
	}
	if *d.Requires3D != has3D {
		return fmt.Errorf("motion %q: requires_3d=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.Requires3D, has3D)
	}
	if *d.RequiresCamera != has3D {
		return fmt.Errorf("motion %q: requires_camera=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.RequiresCamera, has3D)
	}
	return nil
}

// validateImageMotionTracks validates a recipe track collection and accumulates
// whether any property requires camera-backed 3D routing. Collection order and
// the first validation error are preserved by this shared helper.
