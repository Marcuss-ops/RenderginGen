package overlay

// camera_plan.go owns the construction of the wire CameraPlan and the shared
// camera-track keyframe mechanics. Three lowering paths emit a camera (the
// whole-scene rig, the map move, the entity/sequence controllers); each owns
// its own choreography, but the projection optics come from design_tokens.go
// and the resting cameras and keyframe mechanics are built here, so the paths
// cannot drift apart on a value or a track rule none of them chose.
//
// The values are unchanged: this file declares what the lowering already did.
// Before it, the resting perspective camera was two identical struct literals
// in semantic_camera_controllers.go and the same-frame keyframe collapse was
// implemented twice (dedupeConsecutiveKeys in scene_camera_rig.go,
// dedupeSequenceKeys in entity_sequence_camera.go).

// defaultPerspectiveCamera builds the resting perspective camera at planar
// position (x, y): the default optics at the resting distance
// CameraDefaultDistanceZ. It is the camera the entity-style and sequence
// controllers start from before their tracks are attached.
func defaultPerspectiveCamera(x, y float64) *CameraPlan {
	return &CameraPlan{
		Type:     "perspective",
		Position: [3]float64{x, y, CameraDefaultDistanceZ},
		Rotation: [3]float64{0, 0, 0},
		FOVDeg:   CameraDefaultFOVDeg,
		Near:     CameraDefaultNear,
		Far:      CameraDefaultFar,
		Zoom:     1,
	}
}

// mapCameraPlan builds the orthographic camera over the georeferenced map
// plane: the world-scale far plane and the resting field of view, tilted and
// aimed by the move's authored angles.
func mapCameraPlan(tiltDeg, bearingDeg float64) *CameraPlan {
	return &CameraPlan{
		Type:     "orthographic",
		Position: [3]float64{0, 0, MapCameraDistanceZ},
		Rotation: [3]float64{tiltDeg, 0, bearingDeg},
		FOVDeg:   CameraRestingFOVDeg,
		Near:     CameraDefaultNear,
		Far:      CameraWideFar,
		Zoom:     1,
	}
}

// heldLinearKeys returns the two keys of a linear segment with a held lead-in
// key at frame zero when the segment does not start there: the renderer sees
// the value the segment starts from for the whole pre-roll instead of an
// implicit jump at the segment's first frame.
func heldLinearKeys(startFrame, endFrame int64, from, to float64) []AnimationKeyframe {
	keys := []AnimationKeyframe{{Frame: startFrame, Value: from}, {Frame: endFrame, Value: to}}
	if startFrame > 0 {
		keys = append([]AnimationKeyframe{{Frame: 0, Value: from}}, keys...)
	}
	return keys
}

// dedupeKeys collapses same-frame keys keeping the LAST of each run, so
// overlapped handoffs do not emit zero-length holds. Every camera keyframe
// builder uses it: the whole-scene rig dedupes summed poses at shared segment
// boundaries, the sequence controller dedupes crowded arrivals.
func dedupeKeys(keys []AnimationKeyframe) []AnimationKeyframe {
	if len(keys) < 2 {
		return keys
	}
	out := make([]AnimationKeyframe, 0, len(keys))
	for _, key := range keys {
		if len(out) > 0 && out[len(out)-1].Frame == key.Frame {
			out[len(out)-1] = key
			continue
		}
		out = append(out, key)
	}
	return out
}
