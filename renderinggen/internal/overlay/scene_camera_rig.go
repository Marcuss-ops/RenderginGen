package overlay

import (
	"fmt"
	"strings"
)

// Whole-scene camera: the AE one_node/two_node rig over Z-stacked scenes.
//
// After Effects authors never keyframe the camera itself for multi-scene
// moves. Each movement owns one 3D null, nulls parent in a chain, and each
// null carries exactly one position segment (F9 eased, keyframes overlapped
// so the next move starts mid-flight of the previous one for a smooth
// handoff). The renderer owns a single camera, so this file lowers the null
// chain to one CameraPlan + CameraAnimation: the camera pose is the running
// sum of the segment offsets, which is exactly what the parented nulls add
// up to in AE.

// SemanticSceneCameraDOF is the producer-owned depth-of-field block. It is the
// same wire shape CameraDOF carries, so the alias keeps one declaration.
type SemanticSceneCameraDOF = CameraDOF

// SemanticCameraRigSegment is one null of the chain: the camera-space offset
// the null contributes at the END of its window. The contribution ramps from
// zero at start_ms to Offset at end_ms, so consecutive nulls add up exactly
// like parented AE nulls. OverlapMS pulls the segment start earlier so the
// move begins mid-flight of the previous one (the AE overlap trick).
type SemanticCameraRigSegment struct {
	ID        string     `json:"id"`
	StartMS   int64      `json:"start_ms"`
	EndMS     int64      `json:"end_ms"`
	Offset    [3]float64 `json:"offset"`
	OverlapMS int64      `json:"overlap_ms,omitempty"`
}

// SemanticCameraDepthGroup fans named layers out in Z, Depth-script style:
// the i-th layer lands at BaseZ + i*Spacing. With ApparentScale the layer
// scale is compensated by |z|/|restZ| so pushed-back layers keep their
// authored apparent size (what Depth bakes automatically).
type SemanticCameraDepthGroup struct {
	LayerIDs      []string `json:"layer_ids"`
	BaseZ         float64  `json:"base_z"`
	Spacing       float64  `json:"spacing"`
	ApparentScale bool     `json:"apparent_scale,omitempty"`
}

// SemanticSceneCamera is the plan-level whole-scene camera block.
type SemanticSceneCamera struct {
	Node          string                     `json:"node,omitempty"`
	Target        *[3]float64                `json:"target,omitempty"`
	LensPresetMM  float64                    `json:"lens_preset_mm,omitempty"`
	FocalLengthMM float64                    `json:"focal_length_mm,omitempty"`
	FOVDeg        float64                    `json:"fov_deg,omitempty"`
	FStop         float64                    `json:"f_stop,omitempty"`
	IrisBlades    int                        `json:"iris_blades,omitempty"`
	DOF           *SemanticSceneCameraDOF    `json:"dof,omitempty"`
	Rig           []SemanticCameraRigSegment `json:"rig,omitempty"`
	DepthGroups   []SemanticCameraDepthGroup `json:"depth_groups,omitempty"`
	BasePosition  *[3]float64                `json:"base_position,omitempty"`
}

// aeRigEasing is the wire easing for lowered null segments. The render-plan
// contract has no per-key velocity (AE F9 75/75), so segments lower with
// smoothstep: zero velocity at both ends, which is the deterministic
// contract-level equivalent of an Easy-Eased AE null.
const aeRigEasing = "smoothstep"

// The resting field of view when no lens is authored is the named token
// CameraRestingFOVDeg (design_tokens.go).

func sceneMsToFrame(ms, fpsNum, fpsDen int64) int64 {
	if fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return (ms * fpsNum) / (fpsDen * 1000)
}

// validateSceneCamera checks the semantic block without compiling it.
func validateSceneCamera(sc *SemanticSceneCamera) error {
	if sc == nil {
		return nil
	}
	node := strings.TrimSpace(sc.Node)
	if node == "" {
		node = "one_node"
	}
	if node != "one_node" && node != "two_node" {
		return fmt.Errorf("overlay: scene_camera.node must be one_node or two_node, got %q", sc.Node)
	}
	if node == "two_node" && sc.Target == nil {
		return fmt.Errorf("overlay: scene_camera.node two_node requires target (point of interest)")
	}
	if sc.LensPresetMM != 0 && !IsAELensPreset(sc.LensPresetMM) {
		return fmt.Errorf("overlay: scene_camera.lens_preset_mm %v is not an AE preset (24/28/35/50/85)", sc.LensPresetMM)
	}
	if sc.LensPresetMM != 0 && sc.FocalLengthMM != 0 && sc.LensPresetMM != sc.FocalLengthMM {
		return fmt.Errorf("overlay: scene_camera sets both lens_preset_mm and focal_length_mm — author one")
	}
	focal := sc.FocalLengthMM
	if focal == 0 {
		focal = sc.LensPresetMM
	}
	if focal != 0 && (focal < 12 || focal > 200) {
		return fmt.Errorf("overlay: scene_camera focal length %v is outside [12, 200]", focal)
	}
	if sc.FOVDeg != 0 && (sc.FOVDeg < 10 || sc.FOVDeg > 120) {
		return fmt.Errorf("overlay: scene_camera.fov_deg %v is outside [10, 120]", sc.FOVDeg)
	}
	if sc.FStop != 0 && (sc.FStop < 0.7 || sc.FStop > 32) {
		return fmt.Errorf("overlay: scene_camera.f_stop %v is outside [0.7, 32]", sc.FStop)
	}
	if sc.IrisBlades < 0 || sc.IrisBlades > 12 {
		return fmt.Errorf("overlay: scene_camera.iris_blades %d is outside [0, 12]", sc.IrisBlades)
	}
	if dof := sc.DOF; dof != nil {
		if dof.FocusDistance != 0 && (dof.FocusDistance <= 0 || dof.FocusDistance > 10000000) {
			return fmt.Errorf("overlay: scene_camera.dof.focus_distance %v is outside (0, 10000000]", dof.FocusDistance)
		}
		if dof.Aperture < 0 || dof.Aperture > 1 {
			return fmt.Errorf("overlay: scene_camera.dof.aperture %v is outside [0, 1]", dof.Aperture)
		}
		if dof.MaxBlur < 0 || dof.MaxBlur > 256 {
			return fmt.Errorf("overlay: scene_camera.dof.max_blur %v is outside [0, 256]", dof.MaxBlur)
		}
		if dof.FStop != 0 && (dof.FStop < 0.7 || dof.FStop > 32) {
			return fmt.Errorf("overlay: scene_camera.dof.f_stop %v is outside [0.7, 32]", dof.FStop)
		}
		if dof.IrisBlades < 0 || dof.IrisBlades > 12 {
			return fmt.Errorf("overlay: scene_camera.dof.iris_blades %d is outside [0, 12]", dof.IrisBlades)
		}
		if dof.HighlightGain < 0 || dof.HighlightGain > 8 {
			return fmt.Errorf("overlay: scene_camera.dof.highlight_gain %v is outside [0, 8]", dof.HighlightGain)
		}
	}
	seen := map[string]bool{}
	for i, seg := range sc.Rig {
		if strings.TrimSpace(seg.ID) == "" {
			return fmt.Errorf("overlay: scene_camera.rig[%d] needs an id", i)
		}
		if seen[seg.ID] {
			return fmt.Errorf("overlay: scene_camera.rig has a duplicate id %q", seg.ID)
		}
		seen[seg.ID] = true
		if seg.EndMS <= seg.StartMS {
			return fmt.Errorf("overlay: scene_camera.rig %q needs end_ms > start_ms", seg.ID)
		}
		if seg.StartMS < 0 {
			return fmt.Errorf("overlay: scene_camera.rig %q needs start_ms >= 0", seg.ID)
		}
		if seg.OverlapMS < 0 || seg.OverlapMS >= seg.EndMS-seg.StartMS {
			return fmt.Errorf("overlay: scene_camera.rig %q needs 0 <= overlap_ms < duration", seg.ID)
		}
		for _, v := range seg.Offset {
			if !finite(v) {
				return fmt.Errorf("overlay: scene_camera.rig %q offset must be finite", seg.ID)
			}
		}
	}
	for i, g := range sc.DepthGroups {
		if len(g.LayerIDs) == 0 {
			return fmt.Errorf("overlay: scene_camera.depth_groups[%d] needs layer_ids", i)
		}
		if !finite(g.BaseZ) || !finite(g.Spacing) {
			return fmt.Errorf("overlay: scene_camera.depth_groups[%d] base_z/spacing must be finite", i)
		}
	}
	return nil
}

// rigSmoothstep is the deterministic easing inside one null segment: zero
// velocity at both ends, the contract-level equivalent of an Easy-Eased AE
// null (F9 75/75).
func rigSmoothstep(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return x * x * (3 - 2*x)
}

// lowerNullRig lowers the chained nulls to camera_position tracks. Each
// segment contributes its offset ramped 0 -> Offset over its (possibly
// overlapped) window; the camera pose is the sum of the contributions,
// exactly the AE parented-null total. Keys are emitted at every segment
// boundary with the summed pose evaluated there, so overlapped handoffs
// stay frame-ordered. One position channel per null is preserved: a segment
// only ever moves the position axes.
func lowerNullRig(rig []SemanticCameraRigSegment, fpsNum, fpsDen int64) ([]CameraTrack, error) {
	if len(rig) == 0 {
		return nil, nil
	}
	props := [3]string{"camera_position_x", "camera_position_y", "camera_position_z"}
	starts := make([]int64, len(rig))
	ends := make([]int64, len(rig))
	for i, seg := range rig {
		starts[i] = sceneMsToFrame(seg.StartMS, fpsNum, fpsDen)
		ends[i] = sceneMsToFrame(seg.EndMS, fpsNum, fpsDen)
		if i > 0 && seg.OverlapMS > 0 {
			if overlap := sceneMsToFrame(seg.OverlapMS, fpsNum, fpsDen); overlap > 0 {
				starts[i] -= overlap
				// The handoff stays ordered: a start never moves past the
				// previous segment start plus one frame.
				if minStart := starts[i-1] + 1; starts[i] < minStart {
					starts[i] = minStart
				}
			}
		}
		if ends[i] <= starts[i] {
			return nil, fmt.Errorf("overlay: scene_camera.rig %q collapses to zero frames at %dx%d fps", seg.ID, fpsNum, fpsDen)
		}
	}
	// Every boundary keys the summed pose, so the wire track is the absolute
	// pose: the renderer never sums nulls itself.
	bounds := map[int64]bool{}
	for i := range rig {
		bounds[starts[i]] = true
		bounds[ends[i]] = true
	}
	frames := make([]int64, 0, len(bounds))
	for f := range bounds {
		frames = append(frames, f)
	}
	for i := 1; i < len(frames); i++ {
		for j := i; j > 0 && frames[j] < frames[j-1]; j-- {
			frames[j], frames[j-1] = frames[j-1], frames[j]
		}
	}
	poseAt := func(axis int, frame int64) float64 {
		pose := 0.0
		for i, seg := range rig {
			if frame <= starts[i] {
				continue
			}
			if frame >= ends[i] {
				pose += seg.Offset[axis]
				continue
			}
			pose += seg.Offset[axis] * rigSmoothstep(float64(frame-starts[i])/float64(ends[i]-starts[i]))
		}
		return pose
	}
	tracks := make([]CameraTrack, 0, 3)
	for axis := 0; axis < 3; axis++ {
		keys := make([]AnimationKeyframe, 0, len(frames))
		for _, f := range frames {
			keys = append(keys, AnimationKeyframe{Frame: f, Value: poseAt(axis, f)})
		}
		tracks = append(tracks, CameraTrack{
			Property:  props[axis],
			Easing:    aeRigEasing,
			Keyframes: dedupeKeys(keys),
		})
	}
	return tracks, nil
}

// sceneCameraFOV resolves the resting field of view: explicit fov_deg wins,
// then focal/preset mm converted on the full-frame gate, then the 50 default.
func sceneCameraFOV(sc *SemanticSceneCamera) (fovDeg, focalMM float64, err error) {
	focal := sc.FocalLengthMM
	if focal == 0 {
		focal = sc.LensPresetMM
	}
	if sc.FOVDeg != 0 {
		return sc.FOVDeg, focal, nil
	}
	if focal != 0 {
		fov, err := FOVDegFromFocalMM(focal)
		if err != nil {
			return 0, 0, err
		}
		return fov, focal, nil
	}
	return CameraRestingFOVDeg, 0, nil
}

// compileSceneCamera lowers the semantic whole-scene camera to the wire
// CameraPlan + CameraAnimation. baseZ is the resting camera distance used
// when the producer does not author base_position.
func compileSceneCamera(sc *SemanticSceneCamera, width, height int, fpsNum, fpsDen int64, baseZ float64) (*CameraPlan, *CameraAnimation, error) {
	if err := validateSceneCamera(sc); err != nil {
		return nil, nil, err
	}
	fov, focal, err := sceneCameraFOV(sc)
	if err != nil {
		return nil, nil, err
	}
	node := strings.TrimSpace(sc.Node)
	if node == "" {
		node = "one_node"
	}
	pos := [3]float64{float64(width) / 2, float64(height) / 2, baseZ}
	if sc.BasePosition != nil {
		pos = *sc.BasePosition
	}
	cam := &CameraPlan{
		Type:     "perspective",
		Node:     node,
		Position: pos,
		Rotation: [3]float64{0, 0, 0},
		FOVDeg:   fov,
		Near:     CameraDefaultNear,
		Far:      CameraWideFar,
		Zoom:     1.0,
	}
	if focal != 0 {
		cam.FocalMM = focal
	}
	if sc.FStop != 0 {
		cam.FStop = sc.FStop
	}
	if sc.IrisBlades != 0 {
		cam.IrisBlades = sc.IrisBlades
	}
	if sc.Target != nil {
		t := *sc.Target
		cam.Target = &t
	}
	if dof := sc.DOF; dof != nil && (dof.Enabled || dof.FocusDistance != 0 || dof.Aperture != 0 ||
		dof.MaxBlur != 0 || dof.FStop != 0 || dof.IrisBlades != 0 || dof.HighlightGain != 0) {
		dofCopy := *dof
		cam.DOF = &dofCopy
	}
	tracks, err := lowerNullRig(sc.Rig, fpsNum, fpsDen)
	if err != nil {
		return nil, nil, err
	}
	// Shift position tracks by the base pose: the rig lowers offsets, the
	// wire carries absolute poses.
	if len(tracks) > 0 {
		for ti := range tracks {
			axis := ti
			for ki := range tracks[ti].Keyframes {
				if v, ok := tracks[ti].Keyframes[ki].Value.(float64); ok {
					tracks[ti].Keyframes[ki].Value = v + pos[axis]
				}
			}
		}
		return cam, &CameraAnimation{Tracks: tracks}, nil
	}
	return cam, nil, nil
}

// applyDepthGroups fans the named layers out in Z (Depth-script disperse).
// Unknown layer ids fail closed: a silent drop would desync the camera move
// from the scene it was authored against.
func applyDepthGroups(layers []Layer, groups []SemanticCameraDepthGroup, restZ float64) error {
	if len(groups) == 0 {
		return nil
	}
	byID := make(map[string]int, len(layers))
	for i := range layers {
		byID[layers[i].ID] = i
	}
	for gi, g := range groups {
		depths, err := DisperseDepths(len(g.LayerIDs), g.BaseZ, g.Spacing)
		if err != nil {
			return err
		}
		for li, id := range g.LayerIDs {
			idx, ok := byID[id]
			if !ok {
				return fmt.Errorf("overlay: scene_camera.depth_groups[%d] names unknown layer %q", gi, id)
			}
			z := depths[li]
			layer := &layers[idx]
			x, y := 0.0, 0.0
			if len(layer.Position) >= 2 {
				x, y = layer.Position[0], layer.Position[1]
			}
			layer.Position = []float64{x, y, z}
			layer.Enable3D = true
			if g.ApparentScale && restZ != 0 {
				scale, err := ScaleToPreserveApparentSize(restZ, z)
				if err != nil {
					return err
				}
				if len(layer.Scale) == 0 {
					layer.Scale = []float64{scale, scale}
				} else {
					for si := range layer.Scale {
						layer.Scale[si] *= scale
					}
				}
			}
		}
	}
	return nil
}
