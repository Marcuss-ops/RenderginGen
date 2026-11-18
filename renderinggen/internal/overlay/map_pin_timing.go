// map_pin_timing.go lowers the per-pin schedule the map contract already
// validates. Before this file the block was admitted and then ignored, so a
// producer could ask for a staggered reveal and get every marker at frame 0.
//
// The schedule is producer-owned: map_pin_timing[pin_id] = {start_us,
// duration_us}. The worker only re-times the pin and its label inside the map
// window and adds a short deterministic fade so a marker does not pop.
package overlay

import "fmt"

// mapPinFadeInFrames is the worker-owned entrance ramp. It is a readability
// constant, not a producer input: the payload decides WHEN a place appears,
// never how long a marker takes to become visible.
const mapPinFadeInFrames = 6

// pinTiming is the resolved frame window of one pin inside its map item.
type pinTiming struct {
	Start     int64
	Duration  int64
	Scheduled bool
}

// resolvePinTiming reads map_pin_timing for one pin. An absent entry keeps the
// established full-lifetime behaviour.
func resolvePinTiming(params map[string]any, pinID string, fpsNum, fpsDen, duration int64) (pinTiming, error) {
	timing := pinTiming{Start: 0, Duration: duration}
	if params == nil {
		return timing, nil
	}
	raw, present := params["map_pin_timing"]
	if !present || raw == nil {
		return timing, nil
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		return timing, fmt.Errorf("map_pin_timing must be an object keyed by pin id")
	}
	entryRaw, has := entries[pinID]
	if !has {
		return timing, nil
	}
	entry, ok := entryRaw.(map[string]any)
	if !ok {
		return timing, fmt.Errorf("map_pin_timing[%q] must be an object", pinID)
	}
	startUS, startOK := mapParamNumber(entry, "start_us")
	durationUS, durationOK := mapParamNumber(entry, "duration_us")
	if !startOK || !durationOK || startUS < 0 || durationUS <= 0 {
		return timing, fmt.Errorf("map_pin_timing[%q] requires start_us >= 0 and duration_us > 0", pinID)
	}
	start, end := usFrames(int64(startUS), int64(startUS+durationUS), fpsNum, fpsDen)
	if end <= start {
		end = start + 1
	}
	if duration > 0 && end > duration {
		end = duration
	}
	timing.Start, timing.Duration, timing.Scheduled = start, end-start, true
	return timing, nil
}

// applyPinTiming re-times a pin marker and its label to the declared window and
// prepends a short opacity ramp. Layers without a schedule are untouched, so
// every existing map plan compiles byte-identically.
func applyPinTiming(marker, label *Layer, timing pinTiming) {
	if !timing.Scheduled {
		return
	}
	for _, layer := range []*Layer{marker, label} {
		layer.StartFrame = timing.Start
		layer.DurationFrames = timing.Duration
		ramp := min(int64(mapPinFadeInFrames), timing.Duration)
		track := AnimationTrack{
			Property: "opacity",
			Easing:   "out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: ramp, Value: 1.0},
			},
		}
		if layer.Animation == nil {
			layer.Animation = &LayerAnimation{}
		}
		layer.Animation.Tracks = append([]AnimationTrack{track}, layer.Animation.Tracks...)
	}
}
