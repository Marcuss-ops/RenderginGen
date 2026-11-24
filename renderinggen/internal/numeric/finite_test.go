package numeric

import (
	"math"
	"testing"
)

func TestFinite(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  bool
	}{
		{"zero", 0, true},
		{"negative", -12.5, true},
		{"nan", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Finite(tc.value); got != tc.want {
				t.Fatalf("Finite(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
