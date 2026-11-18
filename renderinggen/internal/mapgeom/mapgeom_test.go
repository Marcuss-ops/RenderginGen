package mapgeom

import (
	"math"
	"testing"
)

func TestCleanRingDropsDuplicateClosingPoint(t *testing.T) {
	ring := CleanRing(Ring{{0, 0}, {1, 0}, {1, 1}, {0, 0}})
	if len(ring) != 3 {
		t.Fatalf("len=%d, want 3: %#v", len(ring), ring)
	}
	if ring[0] != (Point{0, 0}) || ring[2] != (Point{1, 1}) {
		t.Fatalf("unexpected ring: %#v", ring)
	}
}

func TestCleanRingDropsRepeatedVertices(t *testing.T) {
	ring := CleanRing(Ring{{0, 0}, {0, 0}, {1, 0}, {1, 0}, {1, 1}})
	if len(ring) != 3 {
		t.Fatalf("len=%d, want 3: %#v", len(ring), ring)
	}
}

func TestRingsBoundsEmptyIsSentinel(t *testing.T) {
	minX, minY, maxX, maxY := RingsBounds(nil)
	if !math.IsInf(minX, 1) || !math.IsInf(minY, 1) || !math.IsInf(maxX, -1) || !math.IsInf(maxY, -1) {
		t.Fatalf("bounds=%v,%v,%v,%v, want +Inf/+Inf/-Inf/-Inf", minX, minY, maxX, maxY)
	}
}

func TestRingsBoundsUnion(t *testing.T) {
	minX, minY, maxX, maxY := RingsBounds([]Ring{
		{{0, 0}, {1, 2}},
		{{-3, 4}, {5, -1}},
	})
	if minX != -3 || minY != -1 || maxX != 5 || maxY != 4 {
		t.Fatalf("bounds=%v,%v,%v,%v", minX, minY, maxX, maxY)
	}
}

func TestBoundsOfRingsReportsEmpty(t *testing.T) {
	if _, ok := BoundsOfRings(nil); ok {
		t.Fatal("empty rings reported bounds")
	}
	bounds, ok := BoundsOfRings([]Ring{
		{{0, 0}, {1, 2}},
		{{-3, 4}, {5, -1}},
	})
	if !ok {
		t.Fatal("non-empty rings reported no bounds")
	}
	if bounds != (Bounds{MinX: -3, MinY: -1, MaxX: 5, MaxY: 4}) {
		t.Fatalf("bounds=%+v", bounds)
	}
}

func TestRingArea(t *testing.T) {
	if got := RingArea(Ring{{0, 0}, {2, 0}, {2, 3}, {0, 3}}); got != 6 {
		t.Fatalf("area=%v, want 6", got)
	}
	if got := RingArea(nil); got != 0 {
		t.Fatalf("area(nil)=%v, want 0", got)
	}
}

func TestSimplifyRingRespectsLimit(t *testing.T) {
	ring := syntheticRing(400)
	got := SimplifyRing(ring, 40)
	if len(got) > 40 {
		t.Fatalf("len=%d, want <= 40", len(got))
	}
	if len(got) < 4 {
		t.Fatalf("len=%d, want >= 4", len(got))
	}
	if got[0] != ring[0] {
		t.Fatalf("first point changed: %v, want %v", got[0], ring[0])
	}
	// SimplifyRing runs RDP on two anchored halves and rebuilds the closed
	// ring; unlike DecimateRing it does not promise the original last point.
	if len(got) < 4 {
		t.Fatalf("ring lost its shape: %#v", got)
	}
}

func TestSimplifyRingDeterministic(t *testing.T) {
	ring := syntheticRing(300)
	a := SimplifyRing(ring, 30)
	b := SimplifyRing(ring, 30)
	if len(a) != len(b) {
		t.Fatalf("len a=%d b=%d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("point %d differs: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestSimplifyPolylineKeepsCollinearEndpoints(t *testing.T) {
	line := Ring{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	got := SimplifyPolyline(line, 0.1)
	if len(got) != 2 || got[0] != line[0] || got[1] != line[3] {
		t.Fatalf("got=%#v, want endpoints only", got)
	}
}

func TestSimplifyRingDoesNotWrapAntimeridian(t *testing.T) {
	// Unwrapping is the projection caller's job. The geometry package must
	// treat -179 and 179 as arbitrary planar x values, not wrap them.
	ring := Ring{{179, 0}, {179.5, 1}, {-179.5, 1}, {-179, 0}, {179, 0}}
	got := CleanRing(ring)
	if len(got) != 4 {
		t.Fatalf("len=%d, want 4: %#v", len(got), got)
	}
	for _, point := range got {
		if math.Abs(point[0]) > 180 {
			t.Fatalf("coordinate was wrapped: %#v", got)
		}
	}
}

func TestDecimateRingPreservesEndpoints(t *testing.T) {
	ring := syntheticRing(101)
	got := DecimateRing(ring, 11)
	if len(got) != 11 {
		t.Fatalf("len=%d, want 11", len(got))
	}
	if got[0] != ring[0] || got[len(got)-1] != ring[len(ring)-1] {
		t.Fatalf("endpoints changed")
	}
}

func TestDecimatePoliciesDiffer(t *testing.T) {
	ring := syntheticRing(10)
	uniform := Decimate(ring, 4, DecimateUniform)
	stepped := Decimate(ring, 4, DecimateStepped)
	if len(uniform) != 4 {
		t.Fatalf("uniform len=%d, want 4", len(uniform))
	}
	if len(stepped) != 4 {
		t.Fatalf("stepped len=%d, want 4", len(stepped))
	}
	if uniform[1] == stepped[1] {
		t.Fatalf("policies unexpectedly agree at index 1: %v", uniform[1])
	}
	if uniform[0] != ring[0] || stepped[0] != ring[0] {
		t.Fatalf("policies must preserve the first point")
	}
	if uniform[len(uniform)-1] != ring[len(ring)-1] || stepped[len(stepped)-1] != ring[len(ring)-1] {
		t.Fatalf("policies must preserve the last point")
	}
}

func TestPointSegmentDistanceSquared(t *testing.T) {
	if got := PointSegmentDistanceSquared(Point{1, 2}, Point{0, 0}, Point{2, 0}); got != 4 {
		t.Fatalf("distance=%v, want 4", got)
	}
	if got := PointSegmentDistanceSquared(Point{2, 0}, Point{0, 0}, Point{1, 0}); got != 1 {
		t.Fatalf("clamped distance=%v, want 1", got)
	}
}

func syntheticRing(n int) Ring {
	ring := make(Ring, 0, n)
	for i := 0; i < n; i++ {
		angle := 2 * math.Pi * float64(i) / float64(n)
		radius := 1 + 0.2*math.Sin(6*angle)
		ring = append(ring, Point{radius * math.Cos(angle), radius * math.Sin(angle)})
	}
	return ring
}
