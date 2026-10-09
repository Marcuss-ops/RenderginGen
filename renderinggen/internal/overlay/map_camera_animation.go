package overlay

import "fmt"

func compileMapCamera(plan *Plan, move SemanticMapCameraMove, startFrame, endFrame int64, fpsNum, fpsDen int) error {
	if plan == nil || fpsNum <= 0 || fpsDen <= 0 {
		return fmt.Errorf("overlay: invalid camera map frame rate")
	}
	if startFrame < 0 || endFrame <= startFrame || endFrame > plan.Canvas.DurationFrames || endFrame > maxCameraMoveFrames {
		return fmt.Errorf("overlay: camera map frame window must be within the plan and at most %d frames", maxCameraMoveFrames)
	}
	// Keep the fly-to in a dedicated opening beat. The final camera keyframe
	// then holds while each grounded point gets its own readable reveal slot.
	cameraEnd := startFrame + (endFrame-startFrame)*2/5
	twoSeconds := int64(fpsNum) * 2 / int64(fpsDen)
	if twoSeconds > 0 && cameraEnd > startFrame+twoSeconds {
		cameraEnd = startFrame + twoSeconds
	}
	if cameraEnd <= startFrame {
		cameraEnd = endFrame
	}
	plan.Camera, plan.CameraAnimation = mapCameraKeyframes(&move, startFrame, cameraEnd)
	return nil
}
