package overlay

import (
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
	"math"
)

func mapCameraKeyframes(move *SemanticMapCameraMove, startFrame, endFrame int64) (*CameraPlan, *CameraAnimation) {
	originX, originY := geo.LatLonToGlobalPixel(move.From.Latitude, move.From.Longitude, int(move.StartZoom))
	destinationX, destinationY := geo.LatLonToGlobalPixel(move.To.Latitude, move.To.Longitude, int(move.StartZoom))
	worldSize := geo.MercatorTileSize * math.Pow(2, move.StartZoom)
	deltaX := wrapMapPlaneDelta(destinationX-originX, worldSize)
	deltaY := -(destinationY - originY)
	zoomFactor := math.Pow(2, move.EndZoom-move.StartZoom)
	base := &CameraPlan{Type: "orthographic", Position: [3]float64{0, 0, -1000}, Rotation: [3]float64{move.StartTiltDeg, 0, move.BearingDeg}, FOVDeg: 50, Near: 1, Far: 10000000, Zoom: 1}
	keyframes := func(from, to float64) []AnimationKeyframe {
		keys := []AnimationKeyframe{{Frame: startFrame, Value: from}, {Frame: endFrame, Value: to}}
		if startFrame > 0 {
			keys = append([]AnimationKeyframe{{Frame: 0, Value: from}}, keys...)
		}
		return keys
	}
	tracks := []CameraTrack{
		{Property: "camera_position_x", Keyframes: keyframes(0, deltaX), Easing: "linear"},
		{Property: "camera_position_y", Keyframes: keyframes(0, deltaY), Easing: "linear"},
		{Property: "camera_rotation_x", Keyframes: keyframes(move.StartTiltDeg, move.EndTiltDeg), Easing: "linear"},
		{Property: "camera_zoom", Keyframes: keyframes(1, zoomFactor), Easing: "linear"},
	}
	return base, &CameraAnimation{Tracks: tracks}
}
