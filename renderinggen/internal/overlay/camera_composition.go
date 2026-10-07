package overlay

// Camera policy facts shared by compiler enforcement and the runtime picker.
const (
	cameraSceneControllerLimit = 1
	cameraPrecedenceRule       = "at most one scene camera controller per plan; a map camera_move and an entity camera style are the same controller slot and cannot both claim it"
)

// Scene camera status vocabulary. Closure decision (GOAL-2): the whole-scene
// camera is explicitly OUT OF SCOPE for RenderingGen — no canonical family,
// no lowering, no selectable IDs, no movement contract. The destination is
// published as unsupported/fail-closed so no consumer can invent a choice,
// and the speculative 5-movement plan was removed as unimplemented complexity.
const (
	cameraDestinationImplemented = "implemented"
	cameraDestinationUnsupported = "unsupported"
	cameraSceneTarget            = "scene"
	cameraSceneUnavailableReason = "whole-scene camera is out of scope: no canonical motion family is planned and no lowering exists, so no scene camera ID is selectable"
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

// runtimeCameraDestinations separates the destinations the runtime implements
// from the scene camera, which is closed as unsupported (see const block).
func runtimeCameraDestinations() []RuntimeCameraDestination {
	return []RuntimeCameraDestination{
		{
			ID: "scene", Description: "Camera over the whole scene or source clip.",
			Status:                  cameraDestinationUnsupported,
			RuntimeSupport:          cameraSceneUnavailableReason,
			MotionTarget:            cameraSceneTarget,
			RequiresProductDecision: false,
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
