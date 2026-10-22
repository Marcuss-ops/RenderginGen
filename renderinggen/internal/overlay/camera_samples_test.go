package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	// The Go sampler is a reference in unit-only builds. When the worker's
	// CHRONON_CAMERA_PLAN_BAKER is configured, these same tests exercise CM3D.
	if strings.TrimSpace(os.Getenv("CHRONON_CAMERA_PLAN_BAKER")) == "" {
		cameraSampleBake = bakeResolvedCameraSamplesGo
	}
}

// bakeResolvedCameraSamplesGo is a test-only reference for packages that are
// run without the native runtime. It is never linked into RenderingGen builds.
func bakeResolvedCameraSamplesGo(plan *Plan) error {
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

func TestBakeResolvedCameraSamples(t *testing.T) {
	plan := &Plan{
		Camera: &CameraPlan{Type: "orthographic", Position: [3]float64{10, 20, -1000}, Rotation: [3]float64{2, 3, 4}, FOVDeg: 50, Near: 1, Far: 10000, Zoom: 1},
		CameraAnimation: &CameraAnimation{Tracks: []CameraTrack{{
			Property: "camera_position_x", Easing: "linear",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: float64(10)}, {Frame: 2, Value: float64(30)}},
		}, {Property: "camera_zoom", Easing: "linear",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: float64(1)}, {Frame: 2, Value: float64(2)}},
		}}},
		Canvas: Canvas{Width: 640, Height: 360, FPSNum: 30, FPSDen: 1, DurationFrames: 3},
	}
	if err := bakeResolvedCameraSamples(plan); err != nil {
		t.Fatal(err)
	}
	if got := len(plan.CameraSamples); got != 3 {
		t.Fatalf("sample count = %d, want 3", got)
	}
	if got := plan.CameraSamples[1].Camera.Position[0]; got != 20 {
		t.Fatalf("middle x = %v, want 20", got)
	}
	if got := plan.CameraSamples[2].Camera.Zoom; got != 2 {
		t.Fatalf("last zoom = %v, want 2", got)
	}
	if got := plan.CameraSamples[0].Camera.Type; got != "orthographic" {
		t.Fatalf("camera type = %q", got)
	}
	wire, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "camera_animation") {
		t.Fatalf("retired camera_animation leaked to wire: %s", wire)
	}
	if !strings.Contains(string(wire), "camera_samples") {
		t.Fatalf("camera_samples missing from wire: %s", wire)
	}
	if schema, version := RenderPlanWireVersion(plan); schema != RenderPlanSchemaV3 || version != 3 {
		t.Fatalf("wire version = %s v%d, want v3", schema, version)
	}
}

// fakeCameraBaker materializes an executable stand-in for the native CM3D
// baker. The extensionless fake path keeps exec.LookPath out of the test.
func fakeCameraBaker(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake camera baker uses a POSIX shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-camera-baker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake camera baker: %v", err)
	}
	return path
}

func cameraBakerTestPlan() *Plan {
	return &Plan{
		Camera: &CameraPlan{Type: "perspective", Position: [3]float64{0, 0, -1000}, Rotation: [3]float64{0, 0, 0}, FOVDeg: 50, Near: 1, Far: 5000, Zoom: 1},
		CameraAnimation: &CameraAnimation{Tracks: []CameraTrack{{
			Property: "camera_position_x", Easing: "linear",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 2, Value: 30.0}},
		}}},
		Canvas: Canvas{Width: 64, Height: 36, FPSNum: 30, FPSDen: 1, DurationFrames: 3},
	}
}

// TestCameraBakerTimeoutFailsClosed proves a hung native baker cannot hold the
// compile (and therefore the worker job) past the configured deadline.
func TestCameraBakerTimeoutFailsClosed(t *testing.T) {
	baker := fakeCameraBaker(t, "#!/bin/sh\nexec sleep 30\n")
	t.Setenv("CHRONON_CAMERA_PLAN_BAKER", baker)
	t.Setenv("CHRONON_CAMERA_PLAN_BAKER_TIMEOUT", "100ms")
	plan := cameraBakerTestPlan()
	started := time.Now()
	err := bakeResolvedCameraSamplesWithCameraRig(plan)
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("hung baker error = %v, want a deadline failure", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("hung baker held the compile for %s, want the configured deadline", elapsed)
	}
	if len(plan.CameraSamples) != 0 || plan.Camera == nil || plan.Camera.Position != [3]float64{0, 0, -1000} {
		t.Fatalf("timed-out baker must leave the plan untouched: samples=%d camera=%+v", len(plan.CameraSamples), plan.Camera)
	}
}

// TestCameraBakerAcceptsResolvedSamples exercises the native path end to end
// with a deterministic stand-in: the emitted samples replace the transient
// track model before the plan crosses the wire.
func TestCameraBakerAcceptsResolvedSamples(t *testing.T) {
	camera := `{"type":"perspective","position":[0,0,-1000],"rotation_deg":[0,0,0],"fov_deg":50,"near":1,"far":5000,"zoom":1}`
	script := "#!/bin/sh\ncat > \"$1\" <<'JSON'\n" +
		`{"camera_samples":[` +
		`{"frame":0,"camera":` + camera + `},` +
		`{"frame":1,"camera":` + camera + `},` +
		`{"frame":2,"camera":` + camera + `}` +
		`],"camera_animation":null}` + "\nJSON\n"
	baker := fakeCameraBaker(t, script)
	t.Setenv("CHRONON_CAMERA_PLAN_BAKER", baker)
	plan := cameraBakerTestPlan()
	if err := bakeResolvedCameraSamplesWithCameraRig(plan); err != nil {
		t.Fatalf("native baker path: %v", err)
	}
	if len(plan.CameraSamples) != 3 {
		t.Fatalf("sample count = %d, want 3", len(plan.CameraSamples))
	}
	if plan.Camera == nil || plan.Camera.Type != "perspective" {
		t.Fatalf("base camera was not replaced by sample 0: %+v", plan.Camera)
	}
	for i, sample := range plan.CameraSamples {
		if sample.Frame != float64(i) {
			t.Fatalf("sample %d frame = %v, want %d", i, sample.Frame, i)
		}
	}
	wire, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal baked plan: %v", err)
	}
	if strings.Contains(string(wire), "camera_animation") {
		t.Fatalf("the transient camera track model leaked to the wire: %s", wire)
	}
}
