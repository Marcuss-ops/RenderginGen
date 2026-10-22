package overlay

const (
	sequenceTravelMS int64 = 2_000
	sequenceHoldMS   int64 = 1_500

	sequencePortraitHeightRatio = 0.94
)

// entitySequenceCameraTracks applies one continuous move between portrait
// positions. The same interpolation works for any direction; the target
// coordinates determine its angle.
func entitySequenceCameraTracks(item semanticItem, startFrame, endFrame int64, canvasWidth, _ int, fpsNum, fpsDen int) []CameraTrack {
	if len(item.ImageLayers) < 2 || endFrame <= startFrame || fpsNum <= 0 || fpsDen <= 0 {
		return nil
	}
	travelMS := int64(sequenceNumber(item.Params["camera_travel_ms"]))
	if travelMS <= 0 {
		travelMS = sequenceTravelMS
	}
	xKeys, yKeys := make([]AnimationKeyframe, 0, len(item.ImageLayers)*2), make([]AnimationKeyframe, 0, len(item.ImageLayers)*2)
	// Image layer positions are offsets from the canvas origin. Camera position
	// tracks use that same world coordinate space, so adding the canvas centre
	// here would miss every target by half the canvas dimensions.
	lastX, lastY := 0.0, 0.0
	for index, layer := range item.ImageLayers {
		x, y := 0.0, 0.0
		if raw, ok := layer.Params["position_x"]; ok {
			x = sequenceNumber(raw)
		}
		if raw, ok := layer.Params["position_y"]; ok {
			y = sequenceNumber(raw)
		}
		arrivalMS := layer.StartMS
		arrival, _ := msFrames(arrivalMS, arrivalMS, int64(fpsNum), int64(fpsDen))
		arrival += startFrame
		if index == 0 {
			if arrival < startFrame {
				arrival = startFrame
			}
			xKeys = append(xKeys, AnimationKeyframe{Frame: arrival, Value: x})
			yKeys = append(yKeys, AnimationKeyframe{Frame: arrival, Value: y})
		} else {
			previousFrame := xKeys[len(xKeys)-1].Frame
			// Crowded entities can begin before the previous camera move has
			// finished. Keep the keys ordered and shorten this move to the time
			// actually available instead of emitting a backwards/overlapping track.
			if arrival <= previousFrame {
				arrival = previousFrame + 1
			}
			_, desiredTravel := msFrames(0, travelMS, int64(fpsNum), int64(fpsDen))
			_, minimumHold := msFrames(0, sequenceHoldMS, int64(fpsNum), int64(fpsDen))
			available := arrival - previousFrame - minimumHold
			if available < 1 {
				// There is not enough time for both the requested hold and a
				// moving frame. Preserve a visible hold and let the next camera
				// target arrive on the next frame instead of emitting coincident
				// or backwards keys that can cause a render jump.
				arrival = previousFrame + minimumHold + 1
				available = 1
			}
			travel := desiredTravel
			if travel > available {
				travel = available
			}
			moveStart := arrival - travel
			xKeys = append(xKeys, AnimationKeyframe{Frame: moveStart, Value: lastX}, AnimationKeyframe{Frame: arrival, Value: x})
			yKeys = append(yKeys, AnimationKeyframe{Frame: moveStart, Value: lastY}, AnimationKeyframe{Frame: arrival, Value: y})
		}
		lastX, lastY = x, y
	}
	if len(xKeys) == 0 {
		return nil
	}
	if xKeys[len(xKeys)-1].Frame < endFrame {
		xKeys = append(xKeys, AnimationKeyframe{Frame: endFrame, Value: lastX})
		yKeys = append(yKeys, AnimationKeyframe{Frame: endFrame, Value: lastY})
	}
	return []CameraTrack{
		{Property: "camera_position_x", Easing: "in_out_cubic", Keyframes: dedupeKeys(xKeys)},
		{Property: "camera_position_y", Easing: "in_out_cubic", Keyframes: dedupeKeys(yKeys)},
	}
}

func sequenceNumber(value any) float64 {
	switch n := value.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
