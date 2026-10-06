package overlay

import "fmt"

func compileMapCamera(plan *Plan, move SemanticMapCameraMove, startFrame, endFrame int64, fpsNum, fpsDen int) error {
	if plan == nil || fpsNum <= 0 || fpsDen <= 0 {
		return fmt.Errorf("overlay: invalid camera map frame rate")
	}
	if startFrame < 0 || endFrame <= startFrame || endFrame > plan.Canvas.DurationFrames || endFrame > maxCameraMoveFrames {
		return fmt.Errorf("overlay: camera map frame window must be within the plan and at most %d frames", maxCameraMoveFrames)
	}
	plan.Camera, plan.CameraAnimation = mapCameraKeyframes(&move, startFrame, endFrame)
	return nil
}
