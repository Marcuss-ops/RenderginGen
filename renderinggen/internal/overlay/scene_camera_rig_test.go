package overlay

import (
	"strings"
	"testing"
)

func rigSeg(id string, startMS, endMS int64, ox, oy, oz float64) SemanticCameraRigSegment {
	return SemanticCameraRigSegment{ID: id, StartMS: startMS, EndMS: endMS, Offset: [3]float64{ox, oy, oz}}
}

func TestValidateSceneCameraNode(t *testing.T) {
	if err := validateSceneCamera(nil); err != nil {
		t.Errorf("nil scene camera must validate, got %v", err)
	}
	if err := validateSceneCamera(&SemanticSceneCamera{Node: "three_node"}); err == nil {
		t.Error("three_node must be rejected")
	}
	if err := validateSceneCamera(&SemanticSceneCamera{Node: "two_node"}); err == nil {
		t.Error("two_node without target must be rejected")
	}
	if err := validateSceneCamera(&SemanticSceneCamera{LensPresetMM: 42}); err == nil {
		t.Error("42mm preset must be rejected")
	}
	if err := validateSceneCamera(&SemanticSceneCamera{LensPresetMM: 50, FocalLengthMM: 35}); err == nil {
		t.Error("preset plus focal must be rejected")
	}
}

func TestLowerNullRigSumsOffsets(t *testing.T) {
	rig := []SemanticCameraRigSegment{
		rigSeg("push", 0, 2000, 0, 0, 400),
		rigSeg("slide", 2000, 4000, 300, 0, 0),
	}
	tracks, err := lowerNullRig(rig, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(tracks))
	}
	// Z track at boundaries {0, 60, 120}: 0 -> 400 -> 400 (summed pose
	// evaluated per boundary, absolute pose before base shift).
	var zvals []float64
	for _, k := range tracks[2].Keyframes {
		zvals = append(zvals, k.Value.(float64))
	}
	want := []float64{0, 400, 400}
	if len(zvals) != len(want) {
		t.Fatalf("z keys = %v, want %v", zvals, want)
	}
	for i := range want {
		if zvals[i] != want[i] {
			t.Errorf("z key[%d] = %v, want %v", i, zvals[i], want[i])
		}
	}
	if tracks[0].Easing != aeRigEasing {
		t.Errorf("easing = %q, want %q", tracks[0].Easing, aeRigEasing)
	}
}

func TestLowerNullRigOverlapKeepsOrder(t *testing.T) {
	rig := []SemanticCameraRigSegment{
		{ID: "a", StartMS: 0, EndMS: 2000, Offset: [3]float64{100, 0, 0}},
		{ID: "b", StartMS: 2000, EndMS: 4000, Offset: [3]float64{100, 0, 0}, OverlapMS: 500},
	}
	tracks, err := lowerNullRig(rig, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Boundaries {0, 45, 60, 120} stay ordered; the mid-handoff pose is the
	// summed smoothstep evaluation, and the total is exact.
	var prev int64 = -1
	for _, k := range tracks[0].Keyframes {
		if k.Frame < prev {
			t.Errorf("x keys go backwards at frame %d", k.Frame)
		}
		prev = k.Frame
	}
	if len(tracks[0].Keyframes) != 4 {
		t.Fatalf("x keys = %d, want 4 boundary keys", len(tracks[0].Keyframes))
	}
	mid := tracks[0].Keyframes[1].Value.(float64)
	if diff := mid - 100*0.75*0.75*(3-2*0.75); diff < -1e-9 || diff > 1e-9 {
		t.Errorf("handoff pose = %v, want smoothstep(0.75)*100", mid)
	}
	if last := tracks[0].Keyframes[3].Value.(float64); last != 200 {
		t.Errorf("final x = %v, want 200", last)
	}
}

func TestCompileSceneCameraTwoNode(t *testing.T) {
	target := [3]float64{0, 0, 0}
	sc := &SemanticSceneCamera{
		Node:         "two_node",
		Target:       &target,
		LensPresetMM: 50,
		Rig:          []SemanticCameraRigSegment{rigSeg("push", 0, 2000, 0, 0, 400)},
		DOF:          &SemanticSceneCameraDOF{Enabled: true, FocusDistance: 1400, FStop: 2.8, IrisBlades: 6},
	}
	cam, anim, err := compileSceneCamera(sc, 1920, 1080, 30, 1, -1400)
	if err != nil {
		t.Fatal(err)
	}
	if cam.Node != "two_node" {
		t.Errorf("node = %q, want two_node", cam.Node)
	}
	if cam.Target == nil {
		t.Fatal("two_node camera needs a target")
	}
	if cam.FocalMM != 50 {
		t.Errorf("focal = %v, want 50", cam.FocalMM)
	}
	if cam.DOF == nil || !cam.DOF.Enabled || cam.DOF.FStop != 2.8 || cam.DOF.IrisBlades != 6 {
		t.Errorf("dof = %+v, want enabled f/2.8 6-blade", cam.DOF)
	}
	if anim == nil || len(anim.Tracks) != 3 {
		t.Fatalf("animation = %+v, want 3 tracks", anim)
	}
	// Absolute pose: base -1400 plus rig offset 400 ends at -1000.
	last := anim.Tracks[2].Keyframes[len(anim.Tracks[2].Keyframes)-1].Value.(float64)
	if last != -1000 {
		t.Errorf("final z = %v, want -1000", last)
	}
}

func TestApplyDepthGroupsUnknownLayerFailsClosed(t *testing.T) {
	layers := []Layer{{ID: "a", Type: "image"}}
	err := applyDepthGroups(layers, []SemanticCameraDepthGroup{{LayerIDs: []string{"ghost"}, BaseZ: 3000, Spacing: 250}}, -1400)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("unknown layer must fail closed, got %v", err)
	}
}

func TestApplyDepthGroupsDisperseAndScale(t *testing.T) {
	layers := []Layer{{ID: "a", Type: "image"}, {ID: "b", Type: "image"}}
	err := applyDepthGroups(layers,
		[]SemanticCameraDepthGroup{{LayerIDs: []string{"a", "b"}, BaseZ: 3000, Spacing: 250, ApparentScale: true}},
		3000)
	if err != nil {
		t.Fatal(err)
	}
	if layers[0].Position[2] != 3000 || layers[1].Position[2] != 3250 {
		t.Errorf("depths = %v, %v", layers[0].Position, layers[1].Position)
	}
	if !layers[0].Enable3D || !layers[1].Enable3D {
		t.Error("dispersed layers must enable 3D")
	}
	if layers[1].Scale[0] != 3250.0/3000.0 {
		t.Errorf("scale compensation = %v", layers[1].Scale)
	}
}
