package overlay

// Camera policy facts shared by compiler enforcement and the runtime picker.
const (
	cameraSceneControllerLimit = 1
	cameraPrecedenceRule       = "at most one scene camera controller per plan; a map camera_move and an entity camera style are the same controller slot and cannot both claim it"
)

// Scene camera status vocabulary. The owner decided on 7 October 2026 that the
// camera roll covers the whole scene or source clip, not only an image or a map
// viewport. The decision is made, so the destination no longer reports
// blocked_product_decision; what it still lacks is a canonical motion family with
// the scene target and its renderer certification, and it reports exactly that.
const (
	cameraDestinationImplemented    = "implemented"
	cameraDestinationNeedsLowering  = "requires_renderer_lowering"
	cameraSceneTarget               = "scene"
	cameraSceneUnavailableReason    = "no canonical motion family with the scene target has been authored and certified yet: ChrononTemplate owns the scene-camera pack and RenderingGen has no lowering for it, so no scene camera ID is selectable"
	cameraMovementUnavailableReason = "declared contract only: the scene camera family that would implement this movement is not authored or certified yet"
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

// sceneCameraMovements is the declared camera-roll contract for the whole scene.
// It is the authored plan for the family the owner approved, not a set of
// selectable IDs: the duration bounds reuse the canonical motion windows, and
// every movement states the pose it owns so the future lowering has one place to
// implement and one place to certify.
func sceneCameraMovements() []RuntimeCameraMovement {
	return []RuntimeCameraMovement{
		{
			ID: "pan", Description: "Slides the whole scene horizontally or vertically around the declared focus point.",
			Direction: "left|right|up|down", DurationBounds: "canonical entrance window, 3s..5s recommended",
			CropBounds: "max 15% of the canvas per axis", InitialPose: "camera centred on the declared focus point",
			FinalPose: "camera offset by the declared pan fraction", UnavailableReason: cameraMovementUnavailableReason,
		},
		{
			ID: "push_pull", Description: "Pushes into or pulls out of the declared focus point without changing the crop ratio.",
			Direction: "in|out", DurationBounds: "canonical entrance window, 3s..5s recommended",
			CropBounds: "scale 1.0..1.35, never beyond the source crop", InitialPose: "scale 1.0",
			FinalPose: "declared final scale", UnavailableReason: cameraMovementUnavailableReason,
		},
		{
			ID: "zoom", Description: "Changes the focal length around the declared focus point.",
			Direction: "in|out", DurationBounds: "canonical entrance window, 3s..5s recommended",
			CropBounds: "fov 35deg..70deg", InitialPose: "declared initial fov",
			FinalPose: "declared final fov", UnavailableReason: cameraMovementUnavailableReason,
		},
		{
			ID: "tilt_roll", Description: "Tilts or rolls the scene camera around the canvas centre.",
			Direction: "tilt_up|tilt_down|roll_cw|roll_ccw", DurationBounds: "canonical entrance window, 3s..5s recommended",
			CropBounds: "max 8deg rotation, corners stay inside the safe area", InitialPose: "0deg",
			FinalPose: "declared final angle", UnavailableReason: cameraMovementUnavailableReason,
		},
		{
			ID: "parallax", Description: "Moves camera and foreground layers by different amounts so depth reads as motion.",
			Direction: "left|right|up|down", DurationBounds: "canonical entrance window, 3s..5s recommended",
			CropBounds:  "max 15% background travel, foreground at 0.4x",
			InitialPose: "layers aligned as declared",
			FinalPose:   "background fully travelled, foreground at the declared factor", UnavailableReason: cameraMovementUnavailableReason,
		},
	}
}

// runtimeCameraDestinations separates the destinations the runtime implements
// from the scene camera, which the owner approved but whose family is still
// unauthored. The scene entry publishes the movement contract so the missing
// authoring is a named work item instead of an unnamed gap.
func runtimeCameraDestinations() []RuntimeCameraDestination {
	return []RuntimeCameraDestination{
		{
			ID: "scene", Description: "Camera over the whole scene or source clip.",
			Status:                  cameraDestinationNeedsLowering,
			RuntimeSupport:          cameraSceneUnavailableReason,
			MotionTarget:            cameraSceneTarget,
			RequiresProductDecision: false,
			Movements:               sceneCameraMovements(),
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
