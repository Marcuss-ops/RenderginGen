package motion

import (
	"fmt"
	"math"
)

// validateImageMotionTracks validates a recipe track collection and accumulates
// whether any property requires camera-backed 3D routing. Collection order and
// the first validation error are preserved by this shared helper.
func validateImageMotionTracks(id, owner string, tracks []TrackDefinition, has3D bool) (bool, error) {
	for _, track := range tracks {
		if err := validateImageMotionTrack(id, owner, track); err != nil {
			return has3D, err
		}
		has3D = has3D || IsCameraBacked3DProperty(track.Property)
	}
	return has3D, nil
}

func validateImageMotionTrack(id, owner string, track TrackDefinition) error {
	if err := validateTrackDefinition(id, owner, track); err != nil {
		return err
	}
	propertyAllowed := false
	for _, property := range []string{"position", "position_x", "position_y", "position_z", "scale", "scale_x", "scale_y", "rotation", "rotation_x", "rotation_y", "rotation_z", "opacity", "blur", "stroke_width", "stroke_color", "fill_color", "trim", "intensity", "radius"} {
		propertyAllowed = propertyAllowed || track.Property == property
	}
	if !propertyAllowed {
		return fmt.Errorf("motion %q: %s uses unsupported image-renderer property %q", id, owner, track.Property)
	}
	if track.Easing != "" && !validMotionEasing(track.Easing) {
		return fmt.Errorf("motion %q: %s has unsupported easing %q", id, owner, track.Easing)
	}
	for _, keyframe := range track.Keyframes {
		switch track.Property {
		case "position", "rotation", "stroke_color", "fill_color", "trim":
			vector, ok := motionNumericVector(keyframe.Value)
			if !ok {
				return fmt.Errorf("motion %q: %s %s keyframe values must be numeric vectors", id, owner, track.Property)
			}
			switch track.Property {
			case "position":
				if len(vector) != 2 && len(vector) != 3 {
					return fmt.Errorf("motion %q: %s position vectors must have two or three values", id, owner)
				}
			case "rotation":
				if len(vector) != 3 {
					return fmt.Errorf("motion %q: %s rotation vectors must have three values", id, owner)
				}
			case "stroke_color", "fill_color":
				if len(vector) != 4 {
					return fmt.Errorf("motion %q: %s %s values must be RGBA vectors", id, owner, track.Property)
				}
				for _, value := range vector {
					if value < 0 || value > 1 {
						return fmt.Errorf("motion %q: %s %s channels must be in [0, 1]", id, owner, track.Property)
					}
				}
			case "trim":
				if len(vector) < 2 || len(vector) > 3 || vector[0] < 0 || vector[0] > 1 || vector[1] < vector[0] || vector[1] > 1 {
					return fmt.Errorf("motion %q: %s trim values require [start,end,offset?] with 0 <= start <= end <= 1", id, owner)
				}
			}
			for _, value := range vector {
				if value < -1000000 || value > 1000000 {
					return fmt.Errorf("motion %q: %s vector is outside supported bounds", id, owner)
				}
			}
		default:
			number, ok := motionNumericValue(keyframe.Value)
			if !ok || number < -1000000 || number > 1000000 {
				return fmt.Errorf("motion %q: %s %s keyframe values must be finite bounded numbers", id, owner, track.Property)
			}
		}
	}
	return nil
}

func imageRecipeHasEffect(component ImageMotionComponent, id string) bool {
	for _, effect := range component.Effects {
		if imageRecipeEffectID(effect.Type) == id {
			return true
		}
	}
	return false
}

func imageRecipeEffectID(kind string) string {
	switch kind {
	case "glow":
		return "light.glow"
	case "bloom":
		return "light.bloom"
	case "drop_shadow":
		return "light.drop_shadow"
	case "gaussian_blur":
		return "blur.gaussian"
	default:
		return ""
	}
}

func imageRecipeEffectSupported(kind string) bool { return imageRecipeEffectID(kind) != "" }

func validateImageRecipeEffect(motionID, componentID string, effect ImageMotionEffect) error {
	allowed := map[string]map[string]bool{
		"glow":          {"radius": true, "intensity": true, "color": true},
		"bloom":         {"radius": true, "intensity": true, "threshold": true},
		"drop_shadow":   {"radius": true, "offset": true, "color": true},
		"gaussian_blur": {"radius": true},
	}[effect.Type]
	for key, raw := range effect.Params {
		if !allowed[key] || !finiteMotionValueTree(raw) {
			return fmt.Errorf("motion %q: component %q has unsupported or non-finite %s parameter %q", motionID, componentID, effect.Type, key)
		}
		switch key {
		case "radius":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 256 {
				return fmt.Errorf("motion %q: component %q effect radius must be in [0, 256]", motionID, componentID)
			}
		case "intensity":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 4 {
				return fmt.Errorf("motion %q: component %q effect intensity must be in [0, 4]", motionID, componentID)
			}
		case "threshold":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 1 {
				return fmt.Errorf("motion %q: component %q effect threshold must be in [0, 1]", motionID, componentID)
			}
		case "color":
			values, ok := motionNumericVector(raw)
			if !ok || len(values) != 4 {
				return fmt.Errorf("motion %q: component %q effect color must be RGBA", motionID, componentID)
			}
			for _, value := range values {
				if value < 0 || value > 1 {
					return fmt.Errorf("motion %q: component %q effect color must be in [0, 1]", motionID, componentID)
				}
			}
		case "offset":
			values, ok := motionNumericVector(raw)
			if !ok || len(values) != 2 {
				return fmt.Errorf("motion %q: component %q shadow offset must be [x,y]", motionID, componentID)
			}
			for _, value := range values {
				if math.Abs(value) > 10000 {
					return fmt.Errorf("motion %q: component %q shadow offset exceeds supported bounds", motionID, componentID)
				}
			}
		}
	}
	return nil
}
