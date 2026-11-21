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

// CompileOptions carries the per-call dependencies of a compile. It is a value,
// not package state: two compiles with different bakers never race or leak
// into each other.
type CompileOptions struct {
	// AssetSizes maps a logical asset path to its probed pixel dimensions. nil
	// selects the blind compile (no materialized bytes yet).
	AssetSizes map[string]ImageSize
	// CameraBaker resolves camera tracks into per-frame poses. nil selects the
	// deterministic in-process evaluator; the worker injects the native CM3D
	// CameraRig runner explicitly.
	CameraBaker CameraSampleBaker
	// SkipCameraBake leaves the transient camera tracks unresolved. It is for
	// the preparatory pass whose plan is discarded after its asset refs and
	// probe inputs are read: baking there would duplicate the render pass.
	SkipCameraBake bool
}

// bakeResolvedCameraSamples converts the compiler's temporary scalar camera
// tracks into complete absolute-frame renderer poses. The wire plan contains no
// independent camera property tracks, so C3D receives a resolved pose at every
// presentation frame. The baker is an explicit dependency; a nil one falls back
// to the in-process reference evaluator instead of mutable package state.
func bakeResolvedCameraSamples(plan *Plan, baker CameraSampleBaker) error {
	if plan == nil || plan.CameraAnimation == nil || len(plan.CameraAnimation.Tracks) == 0 {
		return nil
	}
	if baker == nil {
		baker = referenceCameraBaker{}
	}
	input, err := cameraBakerInput(plan)
	if err != nil {
		return err
	}
	output, err := baker.Bake(input)
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
