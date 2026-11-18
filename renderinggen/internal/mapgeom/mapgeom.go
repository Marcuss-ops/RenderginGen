// Package mapgeom owns deterministic, presentation-free geometry for map
// overlays: ring cleanup, bounds, area, Ramer–Douglas–Peucker simplification
// and decimation.
//
// The package deliberately knows nothing about overlays, render plans, Chronon
// layers, jobs or storage. Coordinates are plain [2]float64 points. Antimeridian
// unwrapping is a projection concern and stays in the caller that projects the
// feature; these functions treat coordinates as a Cartesian plane and never
// wrap them implicitly.
package mapgeom

import "math"

// Point is one planar coordinate. The alias keeps the package zero-cost at the
// boundary: callers already use [2]float64 and do not need a conversion.
type Point = [2]float64

// Ring is a sequence of planar points. It may be open or closed; CleanRing
// removes a duplicated closing point when present.
type Ring = []Point

// Bounds is an axis-aligned bounding box.
type Bounds struct {
	MinX, MinY, MaxX, MaxY float64
}

// DistanceSquared returns the squared Euclidean distance between a and b.
func DistanceSquared(a, b Point) float64 {
	dx, dy := a[0]-b[0], a[1]-b[1]
	return dx*dx + dy*dy
}

// PointSegmentDistanceSquared returns the squared distance from point to the
// segment a-b. It is the primitive used by the RDP simplification.
func PointSegmentDistanceSquared(point, a, b Point) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	length := dx*dx + dy*dy
	if length <= 0 {
		return DistanceSquared(point, a)
	}
	t := ((point[0]-a[0])*dx + (point[1]-a[1])*dy) / length
	t = math.Max(0, math.Min(1, t))
	px, py := a[0]+t*dx, a[1]+t*dy
	return (point[0]-px)*(point[0]-px) + (point[1]-py)*(point[1]-py)
}

// CleanRing drops repeated vertices (including a duplicated closing point) so
// no later step has to divide by a zero-length edge.
func CleanRing(points Ring) Ring {
	ring := make(Ring, 0, len(points))
	for _, point := range points {
		if len(ring) > 0 && DistanceSquared(ring[len(ring)-1], point) <= 1e-6 {
			continue
		}
		ring = append(ring, point)
	}
	if len(ring) > 1 && DistanceSquared(ring[0], ring[len(ring)-1]) <= 1e-6 {
		ring = ring[:len(ring)-1]
	}
	return ring
}

// RingsBounds returns the union bounds of all rings. For no points it returns
// the same sentinel as the historical implementation: +Inf minima and -Inf
// maxima, which callers test with math.IsInf.
func RingsBounds(rings []Ring) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, ring := range rings {
		for _, point := range ring {
			minX = math.Min(minX, point[0])
			minY = math.Min(minY, point[1])
			maxX = math.Max(maxX, point[0])
			maxY = math.Max(maxY, point[1])
		}
	}
	return minX, minY, maxX, maxY
}

// BoundsOfRings returns the union bounds of all rings and reports whether any
// ring contains a point. It is the typed form of RingsBounds for callers that
// want to distinguish an empty geometry from a real origin-centred bbox.
func BoundsOfRings(rings []Ring) (Bounds, bool) {
	minX, minY, maxX, maxY := RingsBounds(rings)
	if math.IsInf(minX, 1) || math.IsInf(maxX, -1) {
		return Bounds{}, false
	}
	return Bounds{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}, true
}

// RingBounds returns the bounds of one ring.
func RingBounds(points Ring) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		minX = math.Min(minX, point[0])
		minY = math.Min(minY, point[1])
		maxX = math.Max(maxX, point[0])
		maxY = math.Max(maxY, point[1])
	}
	return minX, minY, maxX, maxY
}

// RingArea returns the absolute shoelace area of a ring. A duplicated closing
// point is harmless; the modulo wrap makes open and closed rings equivalent.
func RingArea(points Ring) float64 {
	if len(points) < 3 {
		return 0
	}
	var twice float64
	for index, point := range points {
		next := points[(index+1)%len(points)]
		twice += point[0]*next[1] - next[0]*point[1]
	}
	return math.Abs(twice) / 2
}

// SimplifyRing decimates a closed ring with Ramer–Douglas–Peucker, using two
// far-apart anchors so the ring stays closed. It keeps capes and bays instead
// of spending points uniformly on straight borders, and never returns more
// than limit points (except for degenerate inputs shorter than the limit).
func SimplifyRing(points Ring, limit int) Ring {
	ring := CleanRing(points)
	if len(ring) <= limit || limit < 4 {
		return ring
	}
	// Two far-apart anchors let RDP run on open polylines while the ring stays
	// closed: the second half ends back at ring[0], which is dropped again.
	far, farthest := 0, -1.0
	for index, point := range ring {
		if distance := DistanceSquared(ring[0], point); distance > farthest {
			far, farthest = index, distance
		}
	}
	if far == 0 {
		far = len(ring) / 2
	}
	closed := make(Ring, 0, len(ring)-far+1)
	closed = append(closed, ring[far:]...)
	closed = append(closed, ring[0])
	minX, minY, maxX, maxY := RingBounds(ring)
	low, high := 0.0, math.Hypot(maxX-minX, maxY-minY)
	if high <= 0 {
		return ring
	}
	best := ring
	for try := 0; try < 20; try++ {
		tolerance := (low + high) / 2
		simplified := SimplifyPolyline(ring[:far+1], tolerance)
		tail := SimplifyPolyline(closed, tolerance)
		if len(tail) > 1 {
			simplified = append(simplified, tail[1:len(tail)-1]...)
		}
		if len(simplified) > limit {
			low = tolerance
			continue
		}
		best = simplified
		high = tolerance
	}
	return best
}

// SimplifyPolyline applies Ramer–Douglas–Peucker to an open polyline.
func SimplifyPolyline(points Ring, tolerance float64) Ring {
	if len(points) < 3 {
		return append(Ring(nil), points...)
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	stack := [][2]int{{0, len(points) - 1}}
	threshold := tolerance * tolerance
	for len(stack) > 0 {
		segment := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		farthest, farthestIndex := -1.0, -1
		for index := segment[0] + 1; index < segment[1]; index++ {
			if distance := PointSegmentDistanceSquared(points[index], points[segment[0]], points[segment[1]]); distance > farthest {
				farthest, farthestIndex = distance, index
			}
		}
		if farthestIndex >= 0 && farthest > threshold {
			keep[farthestIndex] = true
			stack = append(stack, [2]int{segment[0], farthestIndex}, [2]int{farthestIndex, segment[1]})
		}
	}
	out := make(Ring, 0, len(points))
	for index, point := range points {
		if keep[index] {
			out = append(out, point)
		}
	}
	return out
}

// DecimationMode names the policy used when a ring exceeds its point budget.
type DecimationMode uint8

const (
	// DecimateUniform samples the exact budget evenly and preserves both
	// endpoints. It is the ModernMap1 silhouette policy.
	DecimateUniform DecimationMode = iota
	// DecimateStepped advances by ceil(len/(limit-1)) and appends the last
	// point if needed. It is the tactical-fill policy; it preserves the first
	// and last points but may emit fewer than limit points.
	DecimateStepped
)

// Decimate applies the requested budget policy. The two modes intentionally
// differ: unifying them would change one of the two surfaces.
func Decimate(points Ring, limit int, mode DecimationMode) Ring {
	if mode == DecimateStepped {
		return decimateStepped(points, limit)
	}
	return DecimateRing(points, limit)
}

// DecimateRing keeps the silhouette while bounding the command count a single
// shape can contribute. The first and last point are preserved so the ring
// stays closed.
func DecimateRing(points Ring, limit int) Ring {
	if len(points) <= limit {
		return points
	}
	if limit < 4 {
		limit = 4
	}
	out := make(Ring, 0, limit)
	step := float64(len(points)-1) / float64(limit-1)
	for index := 0; index < limit; index++ {
		position := int(float64(index) * step)
		if position >= len(points) {
			position = len(points) - 1
		}
		out = append(out, points[position])
	}
	out[0] = points[0]
	out[len(out)-1] = points[len(points)-1]
	return out
}

// decimateStepped is the tactical-fill criterion: a ceiling step, no forced
// exact budget, last point appended only when the loop did not reach it.
func decimateStepped(points Ring, limit int) Ring {
	if len(points) <= limit {
		return points
	}
	if limit < 2 {
		limit = 2
	}
	step := int(math.Ceil(float64(len(points)) / float64(limit-1)))
	out := make(Ring, 0, limit)
	for i := 0; i < len(points); i += step {
		out = append(out, points[i])
	}
	if last := points[len(points)-1]; len(out) == 0 || out[len(out)-1] != last {
		out = append(out, last)
	}
	return out
}
