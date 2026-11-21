package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/camerabake"
)

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
	// A nil baker selects the in-process reference evaluator, the deterministic
	// default for tools and direct library callers.
	if err := bakeResolvedCameraSamples(plan, nil); err != nil {
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

// recordingCameraBaker proves the compile uses the explicitly injected port
// instead of package state.
type recordingCameraBaker struct {
	calls int
}

func (r *recordingCameraBaker) Bake(planJSON []byte) ([]byte, error) {
	r.calls++
	return referenceCameraBaker{}.Bake(planJSON)
}

const cameraBakerOptionPlan = `{
	"schema_version":"renderinggen.overlay-plan.v1","plan_id":"baker-option","video_id":"baker-option",
	"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
	"background":{"kind":"color","color":[0,0,0,1]},
	"scene_camera":{"node":"one_node","rig":[{"id":"push","start_ms":0,"end_ms":1000,"offset":[0,0,100]}]}
}`

// TestCompileOptionsUseTheInjectedCameraBaker pins the dependency direction:
// the caller supplies the baker, and the compile never reads a mutable global.
func TestCompileOptionsUseTheInjectedCameraBaker(t *testing.T) {
	payload := []byte(cameraBakerOptionPlan)
	baker := &recordingCameraBaker{}
	result, err := CompileSemanticWithOptions(payload, CompileOptions{CameraBaker: baker})
	if err != nil {
		t.Fatalf("compile with injected baker: %v", err)
	}
	if baker.calls != 1 {
		t.Fatalf("injected baker calls = %d, want 1", baker.calls)
	}
	if got, want := int64(len(result.Plan.CameraSamples)), result.Plan.Canvas.DurationFrames; got != want {
		t.Fatalf("samples = %d, want %d", got, want)
	}
}

// TestCompileOptionsSkipCameraBakeDefersTheRenderPass pins the preparatory
// pass: the transient tracks survive for the render pass, but no baker is
// invoked, so an animated plan is not baked twice.
func TestCompileOptionsSkipCameraBakeDefersTheRenderPass(t *testing.T) {
	baker := &recordingCameraBaker{}
	result, err := CompileSemanticWithOptions([]byte(cameraBakerOptionPlan), CompileOptions{
		CameraBaker:    baker,
		SkipCameraBake: true,
	})
	if err != nil {
		t.Fatalf("compile with deferred bake: %v", err)
	}
	if baker.calls != 0 {
		t.Fatalf("preparatory compile invoked the baker %d times, want 0", baker.calls)
	}
	if result.Plan.CameraAnimation == nil || len(result.Plan.CameraAnimation.Tracks) == 0 {
		t.Fatalf("deferred bake dropped the camera tracks: %+v", result.Plan.CameraAnimation)
	}
	if len(result.Plan.CameraSamples) != 0 {
		t.Fatalf("deferred bake emitted %d camera samples", len(result.Plan.CameraSamples))
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
	binary := fakeCameraBaker(t, "#!/bin/sh\nexec sleep 30\n")
	plan := cameraBakerTestPlan()
	started := time.Now()
	err := bakeResolvedCameraSamples(plan, camerabake.New(binary, 100*time.Millisecond))
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
	binary := fakeCameraBaker(t, script)
	plan := cameraBakerTestPlan()
	if err := bakeResolvedCameraSamples(plan, camerabake.New(binary, 5*time.Second)); err != nil {
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
