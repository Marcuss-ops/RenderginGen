package motionmath

import (
	"math"
	"testing"
)

func TestLastValidFrame(t *testing.T) {
	cases := []struct{ duration, want int64 }{
		{0, 0}, {1, 0}, {2, 1}, {10, 9}, {95, 94},
	}
	for _, tc := range cases {
		if got := LastValidFrame(tc.duration); got != tc.want {
			t.Fatalf("LastValidFrame(%d)=%d, want %d", tc.duration, got, tc.want)
		}
	}
}

func TestClampKeepsHalfOpenWindowAndLastValue(t *testing.T) {
	track := Track{Keyframes: []Keyframe{
		{Frame: -5, Value: 0.0},
		{Frame: 0, Value: 1.0},
		{Frame: 9, Value: 2.0},
		{Frame: 10, Value: 3.0},
	}}
	got := Clamp([]Track{track}, 10)
	if len(got[0].Keyframes) != 2 {
		t.Fatalf("keyframes=%#v, want 2 after coalescing", got[0].Keyframes)
	}
	if got[0].Keyframes[0].Frame != 0 || got[0].Keyframes[0].Value != 1.0 {
		t.Fatalf("first keyframe=%#v", got[0].Keyframes[0])
	}
	if got[0].Keyframes[1].Frame != 9 || got[0].Keyframes[1].Value != 3.0 {
		t.Fatalf("last keyframe=%#v, want frame 9 value 3 (last wins)", got[0].Keyframes[1])
	}
}

func TestCoalesceLastWins(t *testing.T) {
	track := Track{Keyframes: []Keyframe{
		{Frame: 1, Value: 10.0},
		{Frame: 1, Value: 20.0},
		{Frame: 2, Value: 30.0},
	}}
	Coalesce(&track)
	if len(track.Keyframes) != 2 {
		t.Fatalf("len=%d, want 2", len(track.Keyframes))
	}
	if track.Keyframes[0].Value != 20.0 {
		t.Fatalf("collapsed value=%v, want 20 (last)", track.Keyframes[0].Value)
	}
}

func TestRetimeScalesEntrance(t *testing.T) {
	track := Track{Keyframes: []Keyframe{
		{Frame: 0, Value: 0.0},
		{Frame: 2, Value: 0.5},
		{Frame: 4, Value: 1.0},
		{Frame: 10, Value: 1.0},
	}}
	got := Retime([]Track{track}, 10, 4)
	wantFrames := []int64{0, 4, 9}
	if len(got[0].Keyframes) != len(wantFrames) {
		t.Fatalf("keyframes=%#v, want frames %v", got[0].Keyframes, wantFrames)
	}
	for i, want := range wantFrames {
		if got[0].Keyframes[i].Frame != want {
			t.Fatalf("frame[%d]=%d, want %d", i, got[0].Keyframes[i].Frame, want)
		}
	}
}

func TestRetimeCollapsesLongHold(t *testing.T) {
	track := Track{Keyframes: []Keyframe{
		{Frame: 0, Value: 0.0},
		{Frame: 5, Value: 1.0},
		{Frame: 8, Value: 2.0},
	}}
	got := Retime([]Track{track}, 10, 5)
	if len(got[0].Keyframes) != 2 {
		t.Fatalf("keyframes=%#v, want 2", got[0].Keyframes)
	}
	if got[0].Keyframes[1].Frame != 9 || got[0].Keyframes[1].Value != 2.0 {
		t.Fatalf("settled keyframe=%#v, want frame 9 value 2", got[0].Keyframes[1])
	}
}

func TestSamplePropertyTrackLinear(t *testing.T) {
	got, err := SamplePropertyTrack("opacity", "linear", []Keyframe{
		{Frame: 0, Value: 0.0},
		{Frame: 10, Value: 1.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Easing != "linear" || len(got.Keyframes) != 11 {
		t.Fatalf("easing=%q len=%d", got.Easing, len(got.Keyframes))
	}
	for i, keyframe := range got.Keyframes {
		want := float64(i) / 10
		if math.Abs(keyframe.Value.(float64)-want) > 1e-9 {
			t.Fatalf("frame %d value=%v, want %v", i, keyframe.Value, want)
		}
	}
}

func TestSamplePropertyTrackRejectsWindowOverLimit(t *testing.T) {
	_, err := SamplePropertyTrack("opacity", "linear", []Keyframe{
		{Frame: 0, Value: 0.0},
		{Frame: 65536, Value: 1.0},
	})
	if err == nil {
		t.Fatal("expected the 65536-keyframe limit error")
	}
}

func TestSamplePropertyTrackRejectsDuplicateFrames(t *testing.T) {
	_, err := SamplePropertyTrack("opacity", "linear", []Keyframe{
		{Frame: 1, Value: 0.0},
		{Frame: 1, Value: 1.0},
	})
	if err == nil {
		t.Fatal("expected duplicate-frame error")
	}
}

func TestEasingEndpoints(t *testing.T) {
	for _, easing := range []string{"", "linear", "out_cubic", "in_out_cubic", "smoothstep", "in_sine", "out_sine", "in_out_sine", "out_expo", "out_back", "per_word_land"} {
		start, err := EasingValue(easing, 0)
		if err != nil {
			t.Fatalf("%s: %v", easing, err)
		}
		end, err := EasingValue(easing, 1)
		if err != nil {
			t.Fatalf("%s: %v", easing, err)
		}
		if math.Abs(start) > 1e-9 {
			t.Fatalf("%s(0)=%v, want 0", easing, start)
		}
		if math.Abs(end-1) > 1e-9 {
			t.Fatalf("%s(1)=%v, want 1", easing, end)
		}
	}
	if _, err := EasingValue("nope", 0.5); err == nil {
		t.Fatal("expected unsupported easing error")
	}
}

func TestClampSelectorMutatesTracks(t *testing.T) {
	start := &Track{Keyframes: []Keyframe{{Frame: 0, Value: 0.0}, {Frame: 20, Value: 1.0}}}
	selector := &Selector{Start: start}
	ClampSelector(selector, 10)
	if selector.Start == nil || len(selector.Start.Keyframes) != 2 {
		t.Fatalf("start=%#v", selector.Start)
	}
	for _, keyframe := range selector.Start.Keyframes {
		if keyframe.Frame > 9 {
			t.Fatalf("frame %d exceeds last valid frame 9", keyframe.Frame)
		}
	}
}

func TestNumericComponents(t *testing.T) {
	if got, err := NumericComponents(3.5); err != nil || len(got) != 1 || got[0] != 3.5 {
		t.Fatalf("scalar: %v %v", got, err)
	}
	if got, err := NumericComponents([]any{1.0, 2.0}); err != nil || len(got) != 2 || got[1] != 2 {
		t.Fatalf("vector: %v %v", got, err)
	}
	if _, err := NumericComponents([]any{"x"}); err == nil {
		t.Fatal("expected type error")
	}
}
