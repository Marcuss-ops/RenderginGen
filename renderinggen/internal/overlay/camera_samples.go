package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// bakeResolvedCameraSamples converts the compiler's temporary scalar camera
// tracks into complete absolute-frame renderer poses. The wire plan contains no
// independent camera property tracks, so C3D receives a resolved pose at every
// presentation frame.
var cameraSampleBake = bakeResolvedCameraSamplesWithCameraRig

// defaultCameraBakerTimeout bounds one baker invocation. A hung native binary
// must not hold a worker job (and its lease) indefinitely.
const defaultCameraBakerTimeout = 60 * time.Second

// cameraBakerTimeout reads the operator override, falling back to the default
// when unset or malformed. The override is a Go duration (for example "45s").
func cameraBakerTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CHRONON_CAMERA_PLAN_BAKER_TIMEOUT"))
	if raw == "" {
		return defaultCameraBakerTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultCameraBakerTimeout
	}
	return timeout
}

func bakeResolvedCameraSamples(plan *Plan) error {
	return cameraSampleBake(plan)
}

// bakeResolvedCameraSamplesWithCameraRig hands the transient compiler model to
// the installed C++ CameraRig baker. The worker sets this executable explicitly;
// if it is absent, compilation fails closed instead of silently switching the
// camera authority back to the Go reference implementation.
func bakeResolvedCameraSamplesWithCameraRig(plan *Plan) error {
	if plan == nil || plan.CameraAnimation == nil || len(plan.CameraAnimation.Tracks) == 0 {
		return nil
	}
	baker := strings.TrimSpace(os.Getenv("CHRONON_CAMERA_PLAN_BAKER"))
	if baker == "" {
		return fmt.Errorf("overlay: animated camera requires CHRONON_CAMERA_PLAN_BAKER (CM3D CameraRig)")
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("overlay: encode input for CameraRig baker: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(planJSON, &root); err != nil {
		return fmt.Errorf("overlay: decode temporary RenderPlan: %w", err)
	}
	tracksJSON, err := json.Marshal(plan.CameraAnimation)
	if err != nil {
		return fmt.Errorf("overlay: encode camera tracks: %w", err)
	}
	root["camera_animation"] = tracksJSON
	planJSON, err = json.Marshal(root)
	if err != nil {
		return fmt.Errorf("overlay: encode temporary camera plan: %w", err)
	}
	file, err := os.CreateTemp("", "renderinggen-camera-plan-*.json")
	if err != nil {
		return fmt.Errorf("overlay: create temporary camera plan: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(planJSON); err != nil {
		file.Close()
		return fmt.Errorf("overlay: write temporary camera plan: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("overlay: close temporary camera plan: %w", err)
	}
	timeout := cameraBakerTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, baker, path)
	// WaitDelay guarantees Wait returns even when a killed baker leaves a child
	// process holding the output pipe open.
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("overlay: CM3D CameraRig baker did not finish within %s: %w: %s", timeout, err, strings.TrimSpace(output.String()))
		}
		return fmt.Errorf("overlay: CM3D CameraRig baker failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	bakedJSON, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("overlay: reopen CameraRig plan: %w", err)
	}
	defer bakedJSON.Close()
	decoder := json.NewDecoder(bakedJSON)
	var result struct {
		CameraSamples   []CameraSample  `json:"camera_samples"`
		CameraAnimation json.RawMessage `json:"camera_animation"`
	}
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("overlay: decode CameraRig result: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
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
