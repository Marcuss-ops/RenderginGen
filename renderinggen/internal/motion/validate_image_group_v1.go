package motion

import "fmt"

func validateImageGroupV1(d MotionDefinition) error {
	if d.Category != "image_group_v1" {
		if d.GroupRecipe != nil {
			return fmt.Errorf("motion %q: group_recipe is only valid in image_group_v1", d.ID)
		}
		return nil
	}
	if d.GroupRecipe == nil || d.Unit != "group" || d.SupportedTemplate != "image_popup" ||
		d.RenderSafe == nil || !*d.RenderSafe || d.Requires3D == nil || d.RequiresCamera == nil ||
		d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 ||
		d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames || d.Enter <= 0 ||
		int64(d.Enter) > d.DurationBounds.MaximumFrames || d.Exit < 0 ||
		int64(d.Exit) >= d.DurationBounds.MinimumFrames || len(d.Targets) != 1 ||
		d.Targets[0] != "image_group" || len(d.SupportedContent) != 1 ||
		d.SupportedContent[0] != "image" {
		return fmt.Errorf("motion %q: image_group_v1 requires a render-safe group recipe, target and duration metadata", d.ID)
	}
	if err := d.GroupRecipe.Validate(); err != nil {
		return fmt.Errorf("motion %q: %w", d.ID, err)
	}
	if d.GroupRecipe.Has3DTracks() != *d.Requires3D || d.GroupRecipe.Has3DTracks() != *d.RequiresCamera {
		return fmt.Errorf("motion %q: group recipe 3D/camera metadata does not match member tracks", d.ID)
	}
	if !containsMotionString(d.RequiredProperties, "position_x") ||
		!containsMotionString(d.RequiredProperties, "position_y") ||
		!containsMotionString(d.RequiredProperties, "scale") ||
		!containsMotionString(d.RequiredProperties, "opacity") {
		return fmt.Errorf("motion %q: group required_properties must include position_x, position_y, scale and opacity", d.ID)
	}
	for _, track := range d.GroupRecipe.MemberTracks {
		for _, key := range track.Tracks {
			for _, frame := range key.Keyframes {
				value, ok := motionNumericValue(frame.Value)
				if !ok {
					return fmt.Errorf("motion %q: member %d track %q has a non-numeric keyframe", d.ID, track.MemberIndex, key.Property)
				}
				if key.Property == "scale" && (value < 0.5 || value > 1.05) {
					return fmt.Errorf("motion %q: member %d scale %g is outside certified safe bounds [0.5, 1.05]", d.ID, track.MemberIndex, value)
				}
				if key.Property == "opacity" && (value < 0 || value > 1) {
					return fmt.Errorf("motion %q: member %d opacity %g is outside [0,1]", d.ID, track.MemberIndex, value)
				}
			}
		}
	}
	return d.GroupRecipe.ValidateRestingPose()
}
