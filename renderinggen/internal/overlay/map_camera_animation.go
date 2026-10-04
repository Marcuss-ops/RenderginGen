package overlay

import (
	"fmt"
	"math"
	"sort"
)

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

func mapFrameAtZoom(zoom, startZoom, endZoom float64, duration int64) int64 {
	if duration <= 0 || endZoom <= startZoom {
		return 0
	}
	factor := math.Pow(2, endZoom-startZoom)
	return int64(math.Round((math.Pow(2, zoom-startZoom) - 1) / (factor - 1) * float64(duration)))
}

func mapCameraScaleAnimation(move *SemanticMapCameraMove, duration int64) *LayerAnimation {
	factor := math.Pow(2, move.EndZoom-move.StartZoom)
	keys := []AnimationKeyframe{{Frame: 0, Value: 1.0}, {Frame: duration, Value: 1 / factor}}
	return &LayerAnimation{Tracks: []AnimationTrack{{Property: "scale_x", Keyframes: keys, Easing: "linear"}, {Property: "scale_y", Keyframes: keys, Easing: "linear"}}}
}

func mapLODAnimation(index int, lods []SemanticMapLOD, duration int64, move *SemanticMapCameraMove) *LayerAnimation {
	startZoom, endZoom := move.StartZoom, move.EndZoom
	thresholds := make([]float64, len(lods)-1)
	for i := range thresholds {
		thresholds[i] = (float64(lods[i].Zoom) + float64(lods[i+1].Zoom)) / 2
	}
	keys := []AnimationKeyframe{}
	add := func(frame int64, value float64) { keys = append(keys, AnimationKeyframe{Frame: frame, Value: value}) }
	if index == 0 {
		add(0, 1)
		add(duration, 1)
	} else {
		add(0, 0)
		start := math.Max(startZoom, thresholds[index-1]-mapLODFadeHalfBandZoom)
		add(mapFrameAtZoom(start, startZoom, endZoom, duration), 0)
		add(mapFrameAtZoom(math.Min(endZoom, thresholds[index-1]+mapLODFadeHalfBandZoom), startZoom, endZoom, duration), 1)
		if index < len(lods)-1 {
			end := thresholds[index]
			add(mapFrameAtZoom(math.Max(startZoom, end-mapLODFadeHalfBandZoom), startZoom, endZoom, duration), 1)
			add(mapFrameAtZoom(math.Min(endZoom, end+mapLODFadeHalfBandZoom), startZoom, endZoom, duration), 0)
		} else {
			add(mapFrameAtZoom(math.Max(startZoom, endZoom-mapLODFadeHalfBandZoom), startZoom, endZoom, duration), 1)
			add(duration, 1)
		}
	}
	if duration <= 1 {
		return nil
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Frame < keys[j].Frame })
	deduped := keys[:0]
	for _, key := range keys {
		if len(deduped) > 0 && key.Frame == deduped[len(deduped)-1].Frame {
			deduped[len(deduped)-1] = key
		} else {
			deduped = append(deduped, key)
		}
	}
	return &LayerAnimation{Tracks: []AnimationTrack{{Property: "opacity", Keyframes: deduped, Easing: "linear"}}}
}
