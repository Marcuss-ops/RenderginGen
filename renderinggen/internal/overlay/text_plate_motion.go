package overlay

// text_plate_motion.go is the single owner of the plate-entrance vocabulary.
// A plate is a native shape layer, so its entrance is a set of ordinary layer
// tracks. Keeping the recipes here (instead of inlining keyframes in the lift
// pass) makes the catalog testable and prevents the payload validator from
// drifting away from what the renderer can actually play.
//
// The historical behaviour remains the default: when the payload selects no
// motion, the plate copies its caption layer animation. A selected pool takes
// over the plate entrance and is rotated in plan order, so one job with many
// entities can show several different rectangles.

import (
	"math"
	"sort"
	"strings"
)

const (
	plateMotionDefaultEnterFrames = 12
	plateMotionDefaultDistance    = 40.0
)

type plateMotionRecipe func(width float64, enter int64, distance float64) []AnimationTrack

var plateMotionRecipes = map[string]plateMotionRecipe{
	"fade":        plateFadeTracks,
	"slide_left":  plateSlideLeftTracks,
	"slide_right": plateSlideRightTracks,
	"slide_up":    plateSlideUpTracks,
	"scale_in":    plateScaleInTracks,
	"wipe_right":  plateWipeRightTracks,
}

func isPlateMotion(motion string) bool {
	_, ok := plateMotionRecipes[motion]
	return ok
}

func plateMotionVocabulary() string {
	ids := make([]string, 0, len(plateMotionRecipes))
	for id := range plateMotionRecipes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// plateEntranceAnimation builds the chosen entrance. The caller has already
// validated the id; an unknown id returns false rather than inventing a
// fallback that would hide a payload/renderer drift.
func plateEntranceAnimation(motion string, width float64, enter, startFrame int64) (*LayerAnimation, bool) {
	recipe, ok := plateMotionRecipes[motion]
	if !ok {
		return nil, false
	}
	if enter < 1 {
		enter = plateMotionDefaultEnterFrames
	}
	if width <= 0 {
		width = 120
	}
	tracks := recipe(width, enter, plateMotionDefaultDistance)
	if len(tracks) == 0 {
		return nil, false
	}
	// Layer tracks are evaluated on the composition clock. A chain caption can
	// start at frame 71 or 140, so a 0..enter track would already be finished
	// when the layer appears. Shift the whole entrance to the layer start.
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			tracks[i].Keyframes[j].Frame += startFrame
		}
	}
	return &LayerAnimation{Tracks: tracks}, true
}

// plateMotionForIndex rotates the selected pool in plan order. A one-element
// pool pins that motion for every plate; an empty pool keeps the legacy
// caption-copy behaviour.
func plateMotionForIndex(background *LayerBackground, index int) string {
	if background == nil || len(background.Motions) == 0 {
		return ""
	}
	if index < 0 {
		index = 0
	}
	return background.Motions[index%len(background.Motions)]
}

// revealPlateTextAfterEntrance removes the caption's own card tracks and adds a
// short local fade. The caller has already moved the caption start to the frame
// the plate settles, so the word cannot hang outside a half-drawn or half-scaled
// rectangle. Per-glyph animators are a different lane and stay untouched.
func revealPlateTextAfterEntrance(layer *Layer) {
	if layer.Animation != nil {
		kept := layer.Animation.Tracks[:0]
		for _, track := range layer.Animation.Tracks {
			if isCardEntranceProperty(track.Property) {
				continue
			}
			kept = append(kept, track)
		}
		layer.Animation.Tracks = kept
		if len(layer.Animation.Tracks) == 0 {
			layer.Animation = nil
		}
	}
	fade := int64(4)
	if fade > layer.DurationFrames {
		fade = layer.DurationFrames
	}
	if fade <= 1 {
		return
	}
	if layer.Animation == nil {
		layer.Animation = &LayerAnimation{}
	}
	layer.Animation.Tracks = append(layer.Animation.Tracks, AnimationTrack{
		Property: "opacity",
		Easing:   "out_cubic",
		Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 0.0},
			{Frame: fade, Value: 1.0},
		},
	})
}

func isCardEntranceProperty(property string) bool {
	switch property {
	case "opacity", "position", "position_x", "position_y", "scale", "scale_x", "scale_y":
		return true
	default:
		return false
	}
}

func plateOpacityTrack(enter int64) AnimationTrack {
	return AnimationTrack{
		Property:  "opacity",
		Easing:    "out_cubic",
		Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: enter, Value: 1.0}},
	}
}

func plateFadeTracks(_ float64, enter int64, _ float64) []AnimationTrack {
	return []AnimationTrack{plateOpacityTrack(enter)}
}

func plateSlideLeftTracks(_ float64, enter int64, distance float64) []AnimationTrack {
	return []AnimationTrack{
		{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -distance}, {Frame: enter, Value: 0.0}}},
		plateOpacityTrack(enter),
	}
}

func plateSlideRightTracks(_ float64, enter int64, distance float64) []AnimationTrack {
	return []AnimationTrack{
		{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: distance}, {Frame: enter, Value: 0.0}}},
		plateOpacityTrack(enter),
	}
}

func plateSlideUpTracks(_ float64, enter int64, distance float64) []AnimationTrack {
	// Chronon layer translation is y-down: a positive offset places the plate
	// below its resting position, so it rises into place.
	return []AnimationTrack{
		{Property: "position_y", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: distance}, {Frame: enter, Value: 0.0}}},
		plateOpacityTrack(enter),
	}
}

func plateScaleInTracks(_ float64, enter int64, _ float64) []AnimationTrack {
	return []AnimationTrack{
		{Property: "scale_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.86}, {Frame: enter, Value: 1.0}}},
		{Property: "scale_y", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.86}, {Frame: enter, Value: 1.0}}},
		plateOpacityTrack(enter),
	}
}

func plateWipeRightTracks(width float64, enter int64, _ float64) []AnimationTrack {
	// Scaling around the shape centre would open the wipe from both sides.
	// Shifting the centre by width*(scale-1)/2 keeps the left edge fixed, so
	// the plate reveals left-to-right like a lower-third bar.
	offset := -math.Abs(width) * 0.495
	return []AnimationTrack{
		{Property: "scale_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.01}, {Frame: enter, Value: 1.0}}},
		{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: offset}, {Frame: enter, Value: 0.0}}},
		plateOpacityTrack(enter),
	}
}
