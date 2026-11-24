package mapgeom

import "testing"

func TestWrapDelta(t *testing.T) {
	cases := []struct {
		value, world, want float64
	}{
		{1, 360, 1},
		{350, 360, -10},
		{-350, 360, 10},
		{180, 360, 180},
	}
	for _, tc := range cases {
		if got := WrapDelta(tc.value, tc.world); got != tc.want {
			t.Errorf("WrapDelta(%v,%v) = %v, want %v", tc.value, tc.world, got, tc.want)
		}
	}
}

func TestWorldPointAtOriginIsZero(t *testing.T) {
	x, y := WorldPoint(0, 0, 0, 0, 0)
	if x != 0 || y != 0 {
		t.Fatalf("WorldPoint(origin) = (%v,%v), want (0,0)", x, y)
	}
	x, y = WorldPoint(0, 10, 0, 0, 0)
	if x <= 0 || y != 0 {
		t.Fatalf("WorldPoint(east) = (%v,%v), want positive x and zero y", x, y)
	}
}

func TestRectOverlap(t *testing.T) {
	a := [4]float64{0, 0, 10, 10}
	if !RectOverlap(a, [4]float64{5, 5, 15, 15}, 0) {
		t.Fatal("overlapping rectangles were reported disjoint")
	}
	if RectOverlap(a, [4]float64{20, 20, 30, 30}, 0) {
		t.Fatal("disjoint rectangles were reported overlapping")
	}
	if !RectOverlap(a, [4]float64{17, 0, 20, 10}, 8) {
		t.Fatal("gap expansion was not applied")
	}
}

func TestTypographyScaleClamps(t *testing.T) {
	if got := TypographyScale(1280, 720); got != 1 {
		t.Fatalf("reference scale = %v, want 1", got)
	}
	if got := TypographyScale(1, 1); got != 0.65 {
		t.Fatalf("small canvas scale = %v, want 0.65", got)
	}
	if got := TypographyScale(8000, 8000); got != 2.2 {
		t.Fatalf("large canvas scale = %v, want 2.2", got)
	}
}
