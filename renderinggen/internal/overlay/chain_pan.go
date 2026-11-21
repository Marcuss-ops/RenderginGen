package overlay

import (
	"math"
	"strings"
)

// chain_pan.go owns the chain timeline math: the camera pan spec and track, the
// eased move, per-segment windows, animation rebasing and track sampling. It is
// arithmetic over frames and tracks — no layer construction and no semantic
// decoding.

// chainPanSpecFor resolves the producer's chain-level camera choice. Only the
// four certified camera-pan directions switch the chain into strip mode; every
// other value (including the legacy fly_under_fade) keeps the segment handoff.
func chainPanSpecFor(params map[string]any) (cameraPanSpec, bool) {
	raw, ok := params["chain_camera"].(string)
	if !ok {
		return cameraPanSpec{}, false
	}
	spec, ok := cameraPanSpecs[strings.TrimSpace(raw)]
	return spec, ok
}

// chainPanTrack is the shared staircase for a chain camera pan. Arrivals are
// the segment starts relative to the chain, so each image reaches the centre
// exactly when its chain segment starts; the last image holds until the end.
func chainPanTrack(arrivals []int64, count int, spacing float64, duration int64, spec cameraPanSpec) AnimationTrack {
	property := "position_x"
	if spec.axis == "y" {
		property = "position_y"
	}
	// Chronon's typed sampler interpolates component tracks linearly and does
	// not apply `easing`, so the chain bakes the sine curve into the keys. A
	// declared easing would be silently ignored and every move would start and
	// stop at full speed, which reads as a glitch.
	track := AnimationTrack{Property: property, Easing: "linear"}
	if duration <= 0 || count < 2 {
		track.Keyframes = []AnimationKeyframe{{Frame: 0, Value: 0.0}}
		return track
	}
	keys := []AnimationKeyframe{{Frame: 0, Value: 0.0}}
	lastFrame := int64(0)
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	for index := 1; index < count && index < len(arrivals); index++ {
		arrival := arrivals[index]
		gap := arrival - lastFrame
		if gap < 2 {
			gap = 2
		}
		stepTravel := travel
		if stepTravel > gap*3/5 {
			stepTravel = gap * 3 / 5
		}
		if stepTravel < 1 {
			stepTravel = 1
		}
		moveStart := arrival - stepTravel
		if moveStart <= lastFrame {
			moveStart = lastFrame + 1
		}
		if moveStart >= arrival {
			arrival = moveStart + 1
		}
		if arrival >= duration {
			arrival = duration - 1
		}
		if arrival <= moveStart {
			break
		}
		appendEasedPanMove(
			&keys,
			moveStart,
			arrival,
			spec.travelSign*float64(index-1)*spacing,
			spec.travelSign*float64(index)*spacing,
		)
		lastFrame = arrival
	}
	end := lastValidFrame(duration)
	if end > lastFrame {
		keys = append(keys, AnimationKeyframe{Frame: end, Value: spec.travelSign * float64(count-1) * spacing})
	}
	track.Keyframes = keys
	return track
}

// appendEasedPanMove writes one constant-duration move with in_out_sine baked
// into per-frame keys. Baking is required because Chronon's typed sampler
// ignores the track easing for component tracks; a linear two-key move would
// accelerate instantly and stop dead at the next arrival.
func appendEasedPanMove(keys *[]AnimationKeyframe, start, end int64, from, to float64) {
	if end <= start {
		return
	}
	*keys = append(*keys, AnimationKeyframe{Frame: start, Value: from})
	span := float64(end - start)
	for frame := start + 1; frame < end; frame++ {
		t := easeInOutSine(float64(frame-start) / span)
		*keys = append(*keys, AnimationKeyframe{Frame: frame, Value: from + (to-from)*t})
	}
	*keys = append(*keys, AnimationKeyframe{Frame: end, Value: to})
}

func easeInOutSine(t float64) float64 {
	return -(math.Cos(math.Pi*t) - 1) / 2
}

func layerAnimationTrack(anim *LayerAnimation, property string) *AnimationTrack {
	if anim == nil {
		return nil
	}
	for index := range anim.Tracks {
		if anim.Tracks[index].Property == property {
			return &anim.Tracks[index]
		}
	}
	return nil
}

// chainPanAnimation is the single animation every layer of a strip segment
// shares: the travelling position track on the pan axis, a neutral track on
// the other axis, and constant opacity. No per-image entrance survives.
func chainPanAnimation(pan AnimationTrack, duration int64, axis string) *LayerAnimation {
	last := lastValidFrame(duration)
	neutral := func(property string, value float64) AnimationTrack {
		return AnimationTrack{Property: property, Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: value},
			{Frame: last, Value: value},
		}}
	}
	positionX := neutral("position_x", 0)
	positionY := neutral("position_y", 0)
	if axis == "y" {
		positionY = pan
	} else {
		positionX = pan
	}
	return &LayerAnimation{Tracks: []AnimationTrack{
		positionX,
		positionY,
		neutral("opacity", 1),
	}}
}

// chainPanWindows keeps only the active image and its incoming neighbour on
// the timeline. `starts[i]` is when member i begins its travel; `ends[i-1]` is
// the arrival of member i, so the two overlap exactly for the handoff.
func chainPanWindows(arrivals []int64, count int, duration int64) ([]int64, []int64) {
	starts := make([]int64, count)
	ends := make([]int64, count)
	if count == 0 {
		return starts, ends
	}
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	lastArrival := int64(0)
	for index := 1; index < count; index++ {
		arrival := arrivals[index]
		gap := arrival - lastArrival
		if gap < 2 {
			gap = 2
		}
		stepTravel := travel
		if stepTravel > gap*3/5 {
			stepTravel = gap * 3 / 5
		}
		if stepTravel < 1 {
			stepTravel = 1
		}
		start := arrival - stepTravel
		if start <= lastArrival {
			start = lastArrival + 1
		}
		starts[index] = start
		ends[index-1] = arrival
		lastArrival = arrival
	}
	ends[count-1] = duration
	return starts, ends
}

// rebaseLayerAnimation moves a chain-timeline track onto a layer that starts
// at `start`. Chronon evaluates tracks in layer-local frames and rejects
// negative keyframes, so the first key is the sampled value at the layer's
// start and every later key is shifted back by the same amount.
func rebaseLayerAnimation(anim *LayerAnimation, start int64) *LayerAnimation {
	if anim == nil || start <= 0 {
		return anim
	}
	for t := range anim.Tracks {
		track := &anim.Tracks[t]
		lead := sampleTrackAt(*track, start)
		keys := []AnimationKeyframe{{Frame: 0, Value: lead}}
		for _, key := range track.Keyframes {
			frame := key.Frame - start
			if frame <= 0 || frame == keys[len(keys)-1].Frame {
				continue
			}
			key.Frame = frame
			keys = append(keys, key)
		}
		track.Keyframes = keys
	}
	return anim
}

// sampleTrackAt linearly interpolates a track at an absolute chain frame.
// The chain track uses the same easing for every segment, so sampling the
// endpoints through the eased curve is enough for the layer's first key.
func sampleTrackAt(track AnimationTrack, frame int64) float64 {
	keys := track.Keyframes
	if len(keys) == 0 {
		return 0
	}
	if frame <= keys[0].Frame {
		return asFloatValue(keys[0].Value)
	}
	for i := 1; i < len(keys); i++ {
		if frame > keys[i].Frame {
			continue
		}
		span := keys[i].Frame - keys[i-1].Frame
		if span <= 0 {
			return asFloatValue(keys[i].Value)
		}
		progress := float64(frame-keys[i-1].Frame) / float64(span)
		from, to := asFloatValue(keys[i-1].Value), asFloatValue(keys[i].Value)
		return from + (to-from)*progress
	}
	return asFloatValue(keys[len(keys)-1].Value)
}

func asFloatValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint64:
		return float64(v)
	default:
		return 0
	}
}

func offsetChainPosition(position []float64, axis string, offset float64) []float64 {
	out := append([]float64(nil), position...)
	for len(out) < 2 {
		out = append(out, 0)
	}
	if axis == "y" {
		out[1] += offset
	} else {
		out[0] += offset
	}
	return out
}
