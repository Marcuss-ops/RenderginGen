package overlay

import (
	"math"
)

// pan_strip.go owns THE camera-pan strip choreography. Both consumers — the
// chain camera-pan (chain_pan.go) and the reusable image-group camera-pan
// recipe (image_group_camera_pan.go) — build the same staircase: member i holds
// while member i-1 is centred, then the strip travels 60% of the step gap so
// the next member reaches the centre exactly at its arrival. The arithmetic
// AND the baked-linear track shape live here once; callers keep only their
// presentation differences (the group's push-in scale track, chain caption
// policy).
//
// Before this file existed the two callers each computed their own
// step/travel/clamp loop and their own move windows. The duplicated math was
// already drifting (one clamped an arrival at the item end, the other did not),
// so a strip could park at a different frame depending on which compiler
// lowered it.

// panStripStep is one travel of the strip: member `index` moves from its own
// slot to the previous member's slot over [start, end).
type panStripStep struct {
	index int
	start int64
	end   int64
}

// panStrip is the resolved choreography for `count` members inside a `duration`
// frame window. Arrivals are member start frames relative to the window start.
type panStrip struct {
	count    int
	duration int64
	arrivals []int64
	steps    []panStripStep
}

// buildPanStrip resolves arrivals into monotonic move windows. Every arrival is
// clamped so a step can never invert or escape the window; an arrival that
// leaves no room for travel simply ends the staircase early (the remaining
// members stay parked), which is the only safe behaviour for a hand-authored
// chain whose segment starts collide.
func buildPanStrip(arrivals []int64, count int, duration int64) panStrip {
	strip := panStrip{count: count, duration: duration}
	strip.arrivals = make([]int64, count)
	copy(strip.arrivals, arrivals)
	if duration <= 0 || count < 2 {
		return strip
	}
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	lastFrame := int64(0)
	for index := 1; index < count; index++ {
		arrival := int64(0)
		if index < len(arrivals) {
			arrival = arrivals[index]
		}
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
		strip.arrivals[index] = arrival
		strip.steps = append(strip.steps, panStripStep{index: index, start: moveStart, end: arrival})
		lastFrame = arrival
	}
	return strip
}

// track builds the shared position track. The sine curve is always written as
// per-frame keys and the track always declares linear easing: Chronon's typed
// sampler interpolates component tracks linearly and ignores `easing`, so a
// declared curve would be silently dropped and every move would start and stop
// at full speed. Both consumers must publish exactly this shape.
func (strip panStrip) track(property string, spacing, travelSign float64) AnimationTrack {
	track := AnimationTrack{Property: property, Easing: "linear"}
	if strip.duration <= 0 {
		track.Keyframes = []AnimationKeyframe{{Frame: 0, Value: 0.0}}
		return track
	}
	keys := []AnimationKeyframe{{Frame: 0, Value: 0.0}}
	lastFrame := int64(0)
	for _, step := range strip.steps {
		from := travelSign * float64(step.index-1) * spacing
		to := travelSign * float64(step.index) * spacing
		appendEasedPanMove(&keys, step.start, step.end, from, to)
		lastFrame = step.end
	}
	if end := lastValidFrame(strip.duration); end > lastFrame {
		keys = append(keys, AnimationKeyframe{Frame: end, Value: travelSign * float64(strip.count-1) * spacing})
	}
	track.Keyframes = keys
	return track
}

// windows returns the per-member [start,end) residency windows shared by the
// chain strip. Member i becomes visible when it starts travelling and member
// i-1 is retired when member i arrives, so the two overlap exactly for the
// handoff and the canvas is never empty. The last member holds to the item end.
func (strip panStrip) windows() ([]int64, []int64) {
	starts := make([]int64, strip.count)
	ends := make([]int64, strip.count)
	if strip.count == 0 {
		return starts, ends
	}
	lastEnd := int64(0)
	for _, step := range strip.steps {
		starts[step.index] = step.start
		ends[step.index-1] = step.end
		lastEnd = step.end
	}
	ends[strip.count-1] = strip.duration
	if lastEnd > strip.duration {
		ends[strip.count-1] = lastEnd
	}
	return starts, ends
}

// panAnimation assembles the layer animation every strip member carries: the
// travelling position track on the pan axis, a neutral track on the other axis,
// the optional push-in scale track and constant opacity. A nil scale keeps the
// chain's three-track shape; the group recipe passes its 1.02→1.06 push-in.
func panAnimation(pan AnimationTrack, duration int64, axis string, scale *AnimationTrack) *LayerAnimation {
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
	tracks := []AnimationTrack{positionX, positionY}
	if scale != nil {
		tracks = append(tracks, *scale)
	}
	return &LayerAnimation{Tracks: append(tracks, neutral("opacity", 1))}
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
