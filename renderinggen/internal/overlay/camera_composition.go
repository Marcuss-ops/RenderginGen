package overlay

// Camera policy facts shared by compiler enforcement and the runtime picker.
const (
	cameraSceneControllerLimit = 1
	cameraPrecedenceRule       = "at most one scene camera controller per plan; a map camera_move and an entity camera style are the same controller slot and cannot both claim it"
)

// Scene camera status vocabulary. The whole-scene camera is implemented via
// the plan-level scene_camera block: an AE-style one_node/two_node rig over
// Z-stacked scenes, lowered by compileSceneCamera (null-chain rig) plus
// Depth-style disperse groups. Unknown rig/move/layer references still fail
// closed so no consumer can invent a choice.
const (
	cameraDestinationImplemented = "implemented"
	cameraDestinationUnsupported = "unsupported"
	cameraSceneTarget            = "scene"
	cameraSceneSupport           = "plan-level scene_camera: AE one_node/two_node rig (null-chain lowering) with lens presets, DOF and depth disperse groups"
)

// RuntimeCameraMovement is one declared movement of the camera roll contract:
// what the movement does, which parameters it takes and which frame of the pose
// it owns. Supported is false until a canonical family implements it, and the
// unavailable reason says why, so a consumer never sees an invented choice.
type RuntimeCameraMovement struct {
	ID                string `json:"id"`
	Description       string `json:"description"`
	Direction         string `json:"direction_parameter"`
	DurationBounds    string `json:"duration_bounds"`
	CropBounds        string `json:"crop_bounds"`
	InitialPose       string `json:"initial_pose"`
	FinalPose         string `json:"final_pose"`
	Supported         bool   `json:"supported"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// RuntimeCameraDestination is separated per supported destination. Status
// distinguishes what the runtime already implements from what still needs
// renderer lowering.
type RuntimeCameraDestination struct {
	ID                      string                  `json:"id"`
	Description             string                  `json:"description"`
	Status                  string                  `json:"status"`
	RuntimeSupport          string                  `json:"runtime_support"`
	MotionTarget            string                  `json:"motion_target,omitempty"`
	RequiresProductDecision bool                    `json:"requires_product_decision"`
	Movements               []RuntimeCameraMovement `json:"movements,omitempty"`
}

// runtimeCameraDestinations separates the destinations the runtime implements.
// The scene destination is implemented by the scene_camera rig contract.
func runtimeCameraDestinations() []RuntimeCameraDestination {
	return []RuntimeCameraDestination{
		{
			ID: "scene", Description: "Camera over the whole scene or source clip.",
			Status:                  cameraDestinationImplemented,
			RuntimeSupport:          cameraSceneSupport,
			MotionTarget:            cameraSceneTarget,
			RequiresProductDecision: false,
			Movements: []RuntimeCameraMovement{
				{
					ID: "scene_dolly_through", Description: "Dolly along Z through disperse-stacked scenes with POI lock (two_node).",
					Direction: "depth_offset_parameter", DurationBounds: "rig start_ms/end_ms per null",
					CropBounds: "canvas", InitialPose: "rig base position", FinalPose: "base plus summed null offsets",
					Supported: true,
				},
				{
					ID: "scene_lateral_handoff", Description: "Lateral X sweep across parked off-frame scenes, overlapped handoff between nulls.",
					Direction: "lateral_offset_parameter", DurationBounds: "rig start_ms/end_ms per null",
					CropBounds: "canvas", InitialPose: "rig base position", FinalPose: "base plus summed null offsets",
					Supported: true,
				},
				{
					ID: "scene_crane_reveal", Description: "Vertical Y crane move revealing the scene stack.",
					Direction: "vertical_offset_parameter", DurationBounds: "rig start_ms/end_ms per null",
					CropBounds: "canvas", InitialPose: "rig base position", FinalPose: "base plus summed null offsets",
					Supported: true,
				},
			},
		},
		{
			ID: "image", Description: "Camera over a single image layer.",
			Status:         cameraDestinationImplemented,
			RuntimeSupport: "five entity card camera styles selected by entity_style_id",
			MotionTarget:   "image",
		},
		{
			ID: "map", Description: "Camera over a georeferenced map viewport.",
			Status:         cameraDestinationImplemented,
			RuntimeSupport: "map camera_move fly-to with keyframed center and zoom",
			MotionTarget:   "map_view",
		},
	}
}

func runtimeCameraPolicy() RuntimeCameraPolicy {
	return RuntimeCameraPolicy{MaxSceneControllers: cameraSceneControllerLimit, Rule: cameraPrecedenceRule}
}
