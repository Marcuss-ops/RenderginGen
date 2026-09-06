package media

import "testing"

func TestClosedGOPCadence(t *testing.T) {
	cases := []struct {
		name      string
		keyframes []bool
		want      bool
	}{
		{name: "uniform cadence from first packet", keyframes: flagsAt(120, 0, 30, 60, 90), want: true},
		{name: "single gop boundary is not a cadence", keyframes: flagsAt(120, 0, 30), want: false},
		{name: "does not start with keyframe", keyframes: flagsAt(120, 5, 35, 65), want: false},
		{name: "irregular scene-cut boundaries", keyframes: flagsAt(120, 0, 30, 61, 90), want: false},
		{name: "empty stream", keyframes: nil, want: false},
		{name: "jittered cadence", keyframes: flagsAt(120, 0, 29, 60, 91), want: false},
		{name: "every frame keyframe", keyframes: func() []bool {
			f := make([]bool, 30)
			for i := range f {
				f[i] = true
			}
			return f
		}(), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := closedGOPCadence(tc.keyframes); got != tc.want {
				t.Fatalf("closedGOPCadence = %v, want %v", got, tc.want)
			}
		})
	}
}

// flagsAt builds a keyframe flag slice of length n with keyframes at the
// given positions.
func flagsAt(n int, positions ...int) []bool {
	flags := make([]bool, n)
	for _, p := range positions {
		if p >= 0 && p < n {
			flags[p] = true
		}
	}
	return flags
}
