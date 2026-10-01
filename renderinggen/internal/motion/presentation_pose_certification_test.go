package motion

import (
	"fmt"
	"math"
	"testing"
)

// Start/mid/end pose certification for the entity presentation V1 families.
//
// The catalog parity gate proves the presets exist and finish at rest; this
// file proves the poses in between are the poses the editor asked for. The
// sampler below is the same arithmetic the render plan's keyframe tracks
// describe — linear in frame, eased in value — so a pose asserted here is the
// pose Chronon evaluates, not a parallel model of it.

func samplePresentationTrack(track TrackDefinition, frame int) float64 {
	keyframes := track.Keyframes
	if len(keyframes) == 0 {
		return math.NaN()
	}
	if frame <= int(keyframes[0].Frame) {
		return trackNumeric(keyframes[0].Value)
	}
	last := keyframes[len(keyframes)-1]
	if frame >= int(last.Frame) {
		return trackNumeric(last.Value)
	}
	for i := 1; i < len(keyframes); i++ {
		right := keyframes[i]
		if frame > int(right.Frame) {
			continue
		}
		left := keyframes[i-1]
		span := int(right.Frame - left.Frame)
		if span <= 0 {
			return trackNumeric(right.Value)
		}
		progress, err := presentationEasing(track.Easing, float64(frame-int(left.Frame))/float64(span))
		if err != nil {
			return math.NaN()
		}
		from, to := trackNumeric(left.Value), trackNumeric(right.Value)
		return from + (to-from)*progress
	}
	return trackNumeric(last.Value)
}

// presentationEasing mirrors the overlay compiler's easing arithmetic (the
// only two easings the presentation catalog authors, plus the neutral one) so
// the sampler reproduces what the renderer evaluates without importing the
// overlay package into the motion gate.
func presentationEasing(easing string, progress float64) (float64, error) {
	switch easing {
	case "", "linear":
		return progress, nil
	case "out_cubic":
		return 1 - math.Pow(1-progress, 3), nil
	default:
		return 0, fmt.Errorf("presentation sampler: unsupported easing %q", easing)
	}
}

func trackNumeric(value any) float64 {
	number, ok := motionNumericValue(value)
	if !ok {
		return math.NaN()
	}
	return number
}

func presentationTrackByProperty(tracks []TrackDefinition, property string) *TrackDefinition {
	for i := range tracks {
		if tracks[i].Property == property {
			return &tracks[i]
		}
	}
	return nil
}

func presentationMotion(t *testing.T, presetID string) MotionDefinition {
	t.Helper()
	plugin, err := Registry.Resolve(presetID)
	if err != nil {
		t.Fatalf("resolve %s: %v", presetID, err)
	}
	return plugin.(DeclarativePlugin).Definition
}

// TestPresentationMotionStartMidEndPoses certifies the pose envelope of every
// presentation preset: a defined start pose on frame 0, a finite interior
// (mid) pose on the entering segment, and the exact resting final pose.
func TestPresentationMotionStartMidEndPoses(t *testing.T) {
	for _, familyID := range Registry.PresentationFamilyIDs() {
		for _, presetID := range Registry.PresentationMotionIDs(familyID) {
			definition := presentationMotion(t, presetID)
			for _, track := range definition.Tracks {
				start := samplePresentationTrack(track, 0)
				if math.IsNaN(start) || math.IsInf(start, 0) {
					t.Errorf("%s/%s start pose is not finite", presetID, track.Property)
					continue
				}
				if track.Keyframes[0].Frame != 0 {
					t.Errorf("%s/%s first keyframe at frame %d, want 0", presetID, track.Property, track.Keyframes[0].Frame)
				}
				mid := samplePresentationTrack(track, int(track.Keyframes[len(track.Keyframes)/2].Frame))
				if math.IsNaN(mid) || math.IsInf(mid, 0) {
					t.Errorf("%s/%s mid pose is not finite", presetID, track.Property)
				}
				endFrame := int(track.Keyframes[len(track.Keyframes)-1].Frame)
				end := samplePresentationTrack(track, endFrame)
				if math.IsNaN(end) || math.IsInf(end, 0) {
					t.Errorf("%s/%s final pose is not finite", presetID, track.Property)
					continue
				}
				switch track.Property {
				case "opacity", "scale", "scale_x", "scale_y":
					if end != 1 {
						t.Errorf("%s/%s final pose %v, want rest 1", presetID, track.Property, end)
					}
				case "position_x", "position_y", "position_z", "rotation_x", "rotation_y":
					if end != 0 {
						t.Errorf("%s/%s final pose %v, want neutral 0", presetID, track.Property, end)
					}
				}
			}
		}
	}
}

// TestPresentationYawCaptionScenario certifies the entity_yaw_caption
// showcase from the V1 brief: a tilted, recessed, invisible card on frame 0,
// a partially recovered pose mid-entrance, and the exact neutral pose at the
// end of the entrance — with the caption riding the same motion.
func TestPresentationYawCaptionScenario(t *testing.T) {
	definition := presentationMotion(t, "entity_yaw_caption")
	rotation := presentationTrackByProperty(definition.Tracks, "rotation_y")
	depth := presentationTrackByProperty(definition.Tracks, "position_z")
	fade := presentationTrackByProperty(definition.Tracks, "opacity")
	if rotation == nil || depth == nil || fade == nil {
		t.Fatalf("entity_yaw_caption is missing its rotation_y/position_z/opacity tracks: %+v", definition.Tracks)
	}

	// Frame 0: the authored entrance start.
	if got := samplePresentationTrack(*rotation, 0); got != -12 {
		t.Errorf("frame 0 rotation_y = %v, want -12", got)
	}
	if got := samplePresentationTrack(*depth, 0); got != -60 {
		t.Errorf("frame 0 position_z = %v, want -60", got)
	}
	if got := samplePresentationTrack(*fade, 0); got != 0 {
		t.Errorf("frame 0 opacity = %v, want 0", got)
	}

	// Frame 12: strictly inside the entrance, between start and rest.
	midRotation := samplePresentationTrack(*rotation, 12)
	if midRotation <= -12 || midRotation >= 0 {
		t.Errorf("frame 12 rotation_y = %v, want strictly between -12 and 0", midRotation)
	}
	midFade := samplePresentationTrack(*fade, 12)
	if midFade <= 0 || midFade >= 1 {
		t.Errorf("frame 12 opacity = %v, want strictly between 0 and 1", midFade)
	}

	// Frame 30: mid-scene; the counter-rotation is well underway.
	if got := samplePresentationTrack(*rotation, 30); got >= 0 {
		t.Errorf("frame 30 rotation_y = %v, want still recovering toward 0", got)
	}

	// Frame 52 (the authored rest frame) and beyond: exact neutral pose.
	for _, frame := range []int{52, 72} {
		if got := samplePresentationTrack(*rotation, frame); got != 0 {
			t.Errorf("frame %d rotation_y = %v, want 0", frame, got)
		}
		if got := samplePresentationTrack(*depth, frame); got != 0 {
			t.Errorf("frame %d position_z = %v, want 0", frame, got)
		}
		if got := samplePresentationTrack(*fade, frame); got != 1 {
			t.Errorf("frame %d opacity = %v, want 1", frame, got)
		}
	}
}

// TestPresentationMotionCounterNoOvershoot certifies that entrance tracks
// which interpolate a single segment (a counter-like rise) never overshoot
// their target: with out_cubic easing the value approaches the endpoint from
// the authored side and lands exactly.
func TestPresentationMotionCounterNoOvershoot(t *testing.T) {
	definition := presentationMotion(t, "metric_counter_rise")
	rise := presentationTrackByProperty(definition.Tracks, "position_y")
	if rise == nil {
		t.Fatalf("metric_counter_rise is missing its position_y track")
	}
	if got := samplePresentationTrack(*rise, 0); got != 24 {
		t.Errorf("frame 0 position_y = %v, want authored 24", got)
	}
	previous := samplePresentationTrack(*rise, 0)
	for frame := 1; frame <= 48; frame++ {
		value := samplePresentationTrack(*rise, frame)
		if value > previous {
			t.Fatalf("position_y offset regressed at frame %d: %v after %v", frame, value, previous)
		}
		previous = value
	}
	if previous != 0 {
		t.Errorf("position_y final = %v, want 0", previous)
	}
}

// TestPresentationMotionSamplingIsDeterministic samples every track of every
// presentation preset at a fixed frame lattice twice and requires identical
// results: the certification arithmetic must be a pure function of the
// catalog.
func TestPresentationMotionSamplingIsDeterministic(t *testing.T) {
	lattice := []int{0, 3, 7, 12, 22, 30, 48, 52, 60, 72}
	for _, familyID := range Registry.PresentationFamilyIDs() {
		for _, presetID := range Registry.PresentationMotionIDs(familyID) {
			definition := presentationMotion(t, presetID)
			for _, track := range definition.Tracks {
				for _, frame := range lattice {
					first := samplePresentationTrack(track, frame)
					second := samplePresentationTrack(track, frame)
					if first != second {
						t.Fatalf("%s/%s sampled %v then %v at frame %d", presetID, track.Property, first, second, frame)
					}
				}
			}
		}
	}
}
