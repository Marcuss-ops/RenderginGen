// Package motionmath owns the deterministic part of overlay motion: retiming,
// frame-window clamping, duplicate-frame coalescing, keyframe sampling and
// easing.
//
// The package deliberately knows nothing about Chronon layers, catalogs,
// presets or render plans. It works on neutral tracks; the overlay package
// converts its AnimationTrack values at the boundary.
//
// Frame windows are half-open: a duration is a count of frames, so the valid
// interval is [0, duration) and the last valid frame is duration-1.
package motionmath

import (
	"fmt"
	"math"
	"sort"
)

// Keyframe is one value at an absolute frame.
type Keyframe struct {
	Frame int64
	Value any
}

// Track is a property track with its easing name. Component is transient
// compiler metadata; it is carried through conversion but never interpreted
// here.
type Track struct {
	Property  string
	Easing    string
	Keyframes []Keyframe
	Component string
}

// Selector is the four optional property tracks of a text selector.
type Selector struct {
	Start  *Track
	End    *Track
	Offset *Track
	Amount *Track
}

// LastValidFrame is the single frame-boundary authority for animation tracks.
// A duration is a count of frames, so the valid interval is [0, duration).
func LastValidFrame(duration int64) int64 {
	if duration <= 1 {
		return 0
	}
	return duration - 1
}

// Retime scales authored keyframes into the concrete layer duration. Keyframes
// after the authored entrance collapse to the last valid frame before the exit
// is appended; catalogs use those long holds as a settled state, not as a
// longer entrance.
func Retime(tracks []Track, duration, authoredEnter int64) []Track {
	if duration <= 0 {
		return tracks
	}
	if authoredEnter <= 0 {
		for _, track := range tracks {
			for _, keyframe := range track.Keyframes {
				if keyframe.Frame > authoredEnter {
					authoredEnter = keyframe.Frame
				}
			}
		}
	}
	if authoredEnter <= 0 || authoredEnter == duration {
		return Clamp(tracks, duration)
	}
	targetLast := LastValidFrame(duration)
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			frame := tracks[i].Keyframes[j].Frame
			if frame > authoredEnter {
				tracks[i].Keyframes[j].Frame = targetLast
				continue
			}
			tracks[i].Keyframes[j].Frame = frame * targetLast / authoredEnter
		}
	}
	return Clamp(tracks, duration)
}

// Clamp keeps preset-authored keyframes inside the concrete layer duration.
func Clamp(tracks []Track, duration int64) []Track {
	if duration <= 0 {
		return tracks
	}
	for i := range tracks {
		ClampTrack(&tracks[i], duration)
	}
	return tracks
}

// ClampTrack clamps one track and coalesces duplicate frames.
func ClampTrack(track *Track, duration int64) {
	if track == nil || duration <= 0 {
		return
	}
	last := LastValidFrame(duration)
	for i := range track.Keyframes {
		if track.Keyframes[i].Frame < 0 {
			track.Keyframes[i].Frame = 0
		} else if track.Keyframes[i].Frame > last {
			track.Keyframes[i].Frame = last
		}
	}
	// Retiming and clamping can collapse authored hold keyframes onto the same
	// frame. Chronon requires unique frames; the last authored value wins so
	// the settled state survives the collapsed boundary.
	Coalesce(track)
}

// Coalesce collapses keyframes that land on the same frame, keeping the LAST
// authored value. Chronon rejects duplicate frames.
func Coalesce(track *Track) {
	if track == nil || len(track.Keyframes) < 2 {
		return
	}
	unique := track.Keyframes[:0]
	for _, keyframe := range track.Keyframes {
		if len(unique) > 0 && unique[len(unique)-1].Frame == keyframe.Frame {
			unique[len(unique)-1] = keyframe
			continue
		}
		unique = append(unique, keyframe)
	}
	track.Keyframes = unique
}

// ClampSelector clamps all four selector tracks and coalesces them once more.
func ClampSelector(selector *Selector, duration int64) {
	if selector == nil || duration <= 0 {
		return
	}
	ClampTrack(selector.Start, duration)
	ClampTrack(selector.End, duration)
	ClampTrack(selector.Offset, duration)
	ClampTrack(selector.Amount, duration)
	// Clamping a sweep whose authored extent equals the last valid frame can
	// collapse its keyframes onto that frame. Coalesce to the LAST value so the
	// sweep keeps its settled semantics instead of restarting.
	for _, track := range []*Track{selector.Start, selector.End, selector.Offset, selector.Amount} {
		Coalesce(track)
	}
}

// SamplePropertyTrack bakes catalog easing into frame-sampled linear
// keyframes. Chronon intentionally accepts no easing metadata on per-glyph
// property tracks.
func SamplePropertyTrack(property, easing string, keys []Keyframe) (Track, error) {
	if len(keys) == 0 {
		return Track{}, fmt.Errorf("no keyframes")
	}
	ordered := append([]Keyframe(nil), keys...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Frame < ordered[j].Frame })
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Frame <= ordered[i-1].Frame {
			return Track{}, fmt.Errorf("keyframe frames must be unique and increasing")
		}
	}
	if len(ordered) == 1 {
		return Track{Property: property, Easing: "linear", Keyframes: []Keyframe{{Frame: ordered[0].Frame, Value: ordered[0].Value}}}, nil
	}
	first, last := ordered[0].Frame, ordered[len(ordered)-1].Frame
	if first < 0 || last-first > 65535 {
		return Track{}, fmt.Errorf("sample window [%d,%d] exceeds Chronon's 65536-keyframe limit", first, last)
	}
	out := Track{Property: property, Easing: "linear", Keyframes: make([]Keyframe, 0, last-first+1)}
	segment := 0
	for frame := first; frame <= last; frame++ {
		for segment+1 < len(ordered)-1 && frame > ordered[segment+1].Frame {
			segment++
		}
		a, err := NumericComponents(ordered[segment].Value)
		if err != nil {
			return Track{}, err
		}
		b, err := NumericComponents(ordered[segment+1].Value)
		if err != nil {
			return Track{}, err
		}
		if len(a) != len(b) {
			return Track{}, fmt.Errorf("keyframe value arity changed")
		}
		rawT := float64(frame-ordered[segment].Frame) / float64(ordered[segment+1].Frame-ordered[segment].Frame)
		t, err := EasingValue(easing, rawT)
		if err != nil {
			return Track{}, err
		}
		values := make([]float64, len(a))
		for i := range values {
			values[i] = a[i] + (b[i]-a[i])*t
		}
		var value any = values
		if len(values) == 1 {
			value = values[0]
		}
		out.Keyframes = append(out.Keyframes, Keyframe{Frame: frame, Value: value})
	}
	return out, nil
}

// NumericComponents normalizes a scalar or vector keyframe value.
func NumericComponents(value any) ([]float64, error) {
	switch v := value.(type) {
	case float64:
		return []float64{v}, nil
	case float32:
		return []float64{float64(v)}, nil
	case int:
		return []float64{float64(v)}, nil
	case int64:
		return []float64{float64(v)}, nil
	case []float64:
		return append([]float64(nil), v...), nil
	case []any:
		out := make([]float64, len(v))
		for i, component := range v {
			n, ok := component.(float64)
			if !ok {
				return nil, fmt.Errorf("keyframe component %d is %T, want number", i, component)
			}
			out[i] = n
		}
		return out, nil
	default:
		return nil, fmt.Errorf("keyframe value is %T, want numeric scalar or vector", value)
	}
}

// EasingValue evaluates one supported easing at normalized time t.
func EasingValue(easing string, t float64) (float64, error) {
	switch easing {
	case "", "linear":
		return t, nil
	case "out_cubic":
		return 1 - math.Pow(1-t, 3), nil
	case "in_out_cubic":
		if t < 0.5 {
			return 4 * t * t * t, nil
		}
		return 1 - math.Pow(-2*t+2, 3)/2, nil
	case "smoothstep":
		return t * t * (3 - 2*t), nil
	case "in_sine":
		return 1 - math.Cos(t*math.Pi/2), nil
	case "out_sine":
		return math.Sin(t * math.Pi / 2), nil
	case "in_out_sine":
		return -(math.Cos(math.Pi*t) - 1) / 2, nil
	case "out_expo":
		if t == 1 {
			return 1, nil
		}
		return 1 - math.Pow(2, -10*t), nil
	case "out_back":
		const c1, c3 = 1.70158, 2.70158
		return 1 + c3*math.Pow(t-1, 3) + c1*math.Pow(t-1, 2), nil
	case "per_word_land":
		const split, residual = 0.78, 0.07
		const slope = ((1 - residual) / split) * 0.5
		if t < split {
			x := t / split
			return (1 - residual) * (0.5*(1-math.Pow(1-x, 3)) + 0.5*x), nil
		}
		u := (t - split) / (1 - split)
		b := slope * (1 - split)
		c := 3*residual - 2*b
		d := b - 2*residual
		return 1 - residual + b*u + c*u*u + d*u*u*u, nil
	default:
		return 0, fmt.Errorf("unsupported easing %q", easing)
	}
}
