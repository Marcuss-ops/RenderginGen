package overlay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// CameraSampleBaker resolves the compiler's temporary scalar camera tracks into
// complete absolute-frame renderer poses. It is a port: the implementation is
// owned by the composition root (the worker wires the native CM3D CameraRig
// runner), so this package never starts a process, reads the environment or
// touches the filesystem while lowering a plan.
//
// Bake receives the wire plan with its transient camera_animation block and
// returns the same document with camera_samples populated. The compiler keeps
// the invariants (sample count, per-frame ordering, finiteness, no leaked
// camera_animation): a baker is trusted for geometry, never for contract shape.
type CameraSampleBaker interface {
	Bake(planJSON []byte) ([]byte, error)
}

// cameraSampleBaker is installed once at startup, before any job is claimed.
// nil fails closed: an animated camera without a configured baker is a
// deployment error, never a silent switch of camera authority.
var cameraSampleBaker CameraSampleBaker

// SetCameraSampleBaker installs the process-wide baker. It must be called
// during construction, before workers claim jobs; it is not synchronized with
// in-flight compiles.
func SetCameraSampleBaker(baker CameraSampleBaker) {
	cameraSampleBaker = baker
}

// bakeResolvedCameraSamples converts the compiler's temporary scalar camera
// tracks into complete absolute-frame renderer poses. The wire plan contains no
// independent camera property tracks, so C3D receives a resolved pose at every
// presentation frame.
func bakeResolvedCameraSamples(plan *Plan) error {
	if plan == nil || plan.CameraAnimation == nil || len(plan.CameraAnimation.Tracks) == 0 {
		return nil
	}
	if cameraSampleBaker == nil {
		return fmt.Errorf("overlay: animated camera requires CHRONON_CAMERA_PLAN_BAKER (CM3D CameraRig)")
	}
	input, err := cameraBakerInput(plan)
	if err != nil {
		return err
	}
	output, err := cameraSampleBaker.Bake(input)
	if err != nil {
		return fmt.Errorf("overlay: animated camera: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var result struct {
		CameraSamples   []CameraSample  `json:"camera_samples"`
		CameraAnimation json.RawMessage `json:"camera_animation"`
	}
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("overlay: decode CameraRig result: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("overlay: CameraRig result has trailing JSON")
	}
	if len(result.CameraSamples) != int(plan.Canvas.DurationFrames) {
		return fmt.Errorf("overlay: CameraRig returned %d samples for %d frames", len(result.CameraSamples), plan.Canvas.DurationFrames)
	}
	for i, sample := range result.CameraSamples {
		if sample.Frame != float64(i) || !finiteCamera(sample.Camera) {
			return fmt.Errorf("overlay: CameraRig returned invalid sample at index %d", i)
		}
	}
	if len(result.CameraAnimation) > 0 && string(result.CameraAnimation) != "null" {
		return fmt.Errorf("overlay: CM3D CameraRig baker left camera_animation in its output")
	}
	plan.Camera = &result.CameraSamples[0].Camera
	plan.CameraSamples = result.CameraSamples
	return nil
}

// cameraBakerInput encodes the wire plan plus the transient camera_animation
// block the baker consumes. It is the only place that knows the baker's input
// document shape.
func cameraBakerInput(plan *Plan) ([]byte, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("overlay: encode input for CameraRig baker: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(planJSON, &root); err != nil {
		return nil, fmt.Errorf("overlay: decode temporary RenderPlan: %w", err)
	}
	tracksJSON, err := json.Marshal(plan.CameraAnimation)
	if err != nil {
		return nil, fmt.Errorf("overlay: encode camera tracks: %w", err)
	}
	root["camera_animation"] = tracksJSON
	planJSON, err = json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("overlay: encode temporary camera plan: %w", err)
	}
	return planJSON, nil
}

func finiteCamera(camera CameraPlan) bool {
	values := []float64{camera.FOVDeg, camera.Near, camera.Far, camera.Zoom, camera.FocalMM, camera.FStop}
	values = append(values, camera.Position[:]...)
	values = append(values, camera.Rotation[:]...)
	for _, value := range values {
		if !finite(value) {
			return false
		}
	}
	return true
}
