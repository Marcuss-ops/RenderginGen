package overlay

import (
	"strings"
)

// chain_pan.go owns the chain side of the strip: the camera-pan spec, the
// chain's presentation of the shared track, animation rebasing and track
// sampling. The travel arithmetic and residency windows live in pan_strip.go,
// shared with the image-group recipe — no layer construction and no semantic
// decoding here.

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

// chainPanTrack is the chain's presentation of the shared staircase. Arrivals
// are the segment starts relative to the chain, so each image reaches the
// centre exactly when its chain segment starts; the last image holds until the
// end. Chronon's typed sampler interpolates component tracks linearly and does
// not apply `easing`, so the chain bakes the sine curve into the keys. A
// declared easing would be silently ignored and every move would start and stop
// at full speed, which reads as a glitch.
func chainPanTrack(strip panStrip, spacing float64, spec cameraPanSpec) AnimationTrack {
	property := "position_x"
	if spec.axis == "y" {
		property = "position_y"
	}
	return strip.track(property, spacing, spec.travelSign)
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
	return panAnimation(pan, duration, axis, nil)
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
