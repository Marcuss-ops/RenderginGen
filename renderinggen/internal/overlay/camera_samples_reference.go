package overlay

import (
	"encoding/json"
	"fmt"
)

// referenceCameraBaker evaluates the compiler's scalar camera tracks in Go. It
// is the deterministic fallback for tools, tests and direct library callers
// that do not inject a native runner. The worker wires the CM3D CameraRig
// runner through CompileOptions.CameraBaker, because that runner resolves the
// two-node target geometry that a scalar sampler cannot reconstruct.
//
// The implementation is deliberately the same contract the native baker
// returns: the input is the wire plan plus its transient camera_animation
// block, the output is the same document with camera_samples populated and
// camera_animation removed.
type referenceCameraBaker struct{}

func (referenceCameraBaker) Bake(planJSON []byte) ([]byte, error) {
	var doc struct {
		Canvas          Canvas           `json:"canvas"`
		Camera          *CameraPlan      `json:"camera"`
		CameraAnimation *CameraAnimation `json:"camera_animation"`
	}
	if err := json.Unmarshal(planJSON, &doc); err != nil {
		return nil, err
	}
	plan := &Plan{Canvas: doc.Canvas, Camera: doc.Camera, CameraAnimation: doc.CameraAnimation}
	if err := evaluateCameraSamples(plan); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"camera_samples": plan.CameraSamples, "camera_animation": nil})
}

// evaluateCameraSamples resolves every presentation frame from the scalar
// camera tracks. It validates the track shape and finiteness; the caller keeps
// the sample-count and ordering invariants.
func evaluateCameraSamples(plan *Plan) error {
	if plan == nil || plan.CameraAnimation == nil || len(plan.CameraAnimation.Tracks) == 0 {
		return nil
	}
	if plan.Camera == nil {
		return fmt.Errorf("overlay: camera tracks require a base camera")
	}
	frames := plan.Canvas.DurationFrames
	if frames < 1 || frames > 1_000_000 {
		return fmt.Errorf("overlay: camera sample duration %d is outside [1, 1000000]", frames)
	}
	tracks := make(map[string]CameraTrack, len(plan.CameraAnimation.Tracks))
	for _, track := range plan.CameraAnimation.Tracks {
		if len(track.Keyframes) == 0 {
			return fmt.Errorf("overlay: camera track %q has no keyframes", track.Property)
		}
		tracks[track.Property] = track
	}
	sample := func(property string, frame int64, fallback float64) (float64, error) {
		track, ok := tracks[property]
		if !ok {
			return fallback, nil
		}
		keys := track.Keyframes
		first, ok := numericValue(keys[0].Value)
		if !ok {
			return 0, fmt.Errorf("overlay: camera track %q requires scalar values", property)
		}
		if frame <= keys[0].Frame {
			return first, nil
		}
		for i := 1; i < len(keys); i++ {
			if frame > keys[i].Frame {
				continue
			}
			left, leftOK := numericValue(keys[i-1].Value)
			right, rightOK := numericValue(keys[i].Value)
			if !leftOK || !rightOK {
				return 0, fmt.Errorf("overlay: camera track %q requires scalar values", property)
			}
			span := keys[i].Frame - keys[i-1].Frame
			if span <= 0 {
				return 0, fmt.Errorf("overlay: camera track %q has non-increasing keyframes", property)
			}
			t := float64(frame-keys[i-1].Frame) / float64(span)
			eased, easeErr := easingValue(track.Easing, t)
			if easeErr != nil {
				return 0, fmt.Errorf("overlay: camera track %q: %w", property, easeErr)
			}
			return left + (right-left)*eased, nil
		}
		last, ok := numericValue(keys[len(keys)-1].Value)
		if !ok {
			return 0, fmt.Errorf("overlay: camera track %q requires scalar values", property)
		}
		return last, nil
	}
	plan.CameraSamples = make([]CameraSample, 0, frames)
	for frame := int64(0); frame < frames; frame++ {
		camera := *plan.Camera
		if plan.Camera.DOF != nil {
			dof := *plan.Camera.DOF
			camera.DOF = &dof
		}
		for axis, suffix := range []string{"x", "y", "z"} {
			v, err := sample("camera_position_"+suffix, frame, camera.Position[axis])
			if err != nil {
				return err
			}
			camera.Position[axis] = v
			v, err = sample("camera_rotation_"+suffix, frame, camera.Rotation[axis])
			if err != nil {
				return err
			}
			camera.Rotation[axis] = v
		}
		var err error
		if camera.FOVDeg, err = sample("camera_fov_deg", frame, camera.FOVDeg); err != nil {
			return err
		}
		if camera.Zoom, err = sample("camera_zoom", frame, camera.Zoom); err != nil {
			return err
		}
		if !finiteCamera(camera) {
			return fmt.Errorf("overlay: resolved camera sample %d is non-finite", frame)
		}
		plan.CameraSamples = append(plan.CameraSamples, CameraSample{Frame: float64(frame), Camera: camera})
	}
	return nil
}
