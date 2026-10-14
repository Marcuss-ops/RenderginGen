package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// entityCameraTracks resolves the camera tracks of an entity camera motion from
// the ChrononTemplate catalog (entity_camera_tracks). Keyframes that declare a
// center axis are offsets from the canvas centre (cx, cy) at render time.
// Unknown motion ids resolve to no tracks, matching the previous behaviour.
func entityCameraTracks(id string, cx, cy float64) ([]CameraTrack, error) {
	if id == "" {
		return nil, nil
	}
	cat, err := motion.Canonical()
	if err != nil {
		return nil, err
	}
	defs, ok := cat.EntityCameraTracks[id]
	if !ok {
		return nil, nil
	}
	tracks := make([]CameraTrack, 0, len(defs))
	for _, def := range defs {
		keyframes := make([]AnimationKeyframe, 0, len(def.Keyframes))
		for _, kf := range def.Keyframes {
			value := kf.Value
			switch kf.Center {
			case "":
			case "x":
				value += cx
			case "y":
				value += cy
			default:
				return nil, fmt.Errorf("entity camera %q: unknown keyframe center %q", id, kf.Center)
			}
			keyframes = append(keyframes, AnimationKeyframe{Frame: kf.Frame, Value: value})
		}
		tracks = append(tracks, CameraTrack{Property: def.Property, Easing: def.Easing, Keyframes: keyframes})
	}
	return tracks, nil
}
