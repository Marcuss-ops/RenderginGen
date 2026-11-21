package overlay

import (
	"math"
	"sort"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/mapgeom"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

// semantic_map_modern_geometry.go owns the pure ring and polyline math of the
// ModernMap1 plate: point budgets, simplification, bounds, area and the local
// path/bbox convention shared by fill, outline, halo and route. Nothing here
// touches a layer.

// trimMapPolyline clips a canvas-space polyline to the [start, end] fraction of
// its arc length, interpolating the two cut points. An empty result means the
// requested range is degenerate.
func trimMapPolyline(points [][2]float64, start, end float64) [][2]float64 {
	if len(points) < 2 {
		return points
	}
	start = math.Max(0, math.Min(1, start))
	end = math.Max(0, math.Min(1, end))
	if end <= start {
		return nil
	}
	if start == 0 && end == 1 {
		return points
	}
	lengths := make([]float64, len(points)-1)
	total := 0.0
	for index := range lengths {
		lengths[index] = math.Hypot(points[index+1][0]-points[index][0], points[index+1][1]-points[index][1])
		total += lengths[index]
	}
	if total <= 0 {
		return nil
	}
	startDistance, endDistance := start*total, end*total
	out := make([][2]float64, 0, len(points)+2)
	travelled := 0.0
	for index, length := range lengths {
		segmentStart, segmentEnd := travelled, travelled+length
		travelled = segmentEnd
		if segmentEnd <= startDistance || segmentStart >= endDistance {
			continue
		}
		from, to := points[index], points[index+1]
		fromFraction, toFraction := 0.0, 1.0
		if length > 0 {
			fromFraction = math.Max(0, (startDistance-segmentStart)/length)
			toFraction = math.Min(1, (endDistance-segmentStart)/length)
		}
		cutFrom := mapPolylinePoint(from, to, fromFraction)
		cutTo := mapPolylinePoint(from, to, toFraction)
		if len(out) == 0 || out[len(out)-1] != cutFrom {
			out = append(out, cutFrom)
		}
		out = append(out, cutTo)
	}
	return out
}

func mapPolylinePoint(from, to [2]float64, fraction float64) [2]float64 {
	return [2]float64{
		from[0] + (to[0]-from[0])*fraction,
		from[1] + (to[1]-from[1])*fraction,
	}
}

// modernMapTerritoryRings selects the country parts the plate can afford and
// sizes each silhouette by its share of the territory. Halo, outline and fill
// all consume these rings, because a second, coarser fill ring is exactly what
// made the accent cut inside its own border.
func modernMapTerritoryRings(regions []tacticalfill.Region) [][][2]float64 {
	type part struct {
		points [][2]float64
		area   float64
	}
	parts := make([]part, 0, len(regions))
	var totalArea float64
	for _, region := range regions {
		points := modernMapCleanRing(region.Points)
		if len(points) < 3 {
			continue
		}
		area := modernMapRingArea(points)
		if area <= 0 {
			continue
		}
		parts = append(parts, part{points: points, area: area})
		totalArea += area
	}
	if len(parts) == 0 {
		return nil
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].area > parts[j].area })
	kept := make([]part, 0, len(parts))
	kept = append(kept, parts[0])
	for _, candidate := range parts[1:] {
		// Specks below 0.4% of the country read as a dot of ink at plate
		// scale and would steal layers from the real coastline.
		if candidate.area/totalArea >= 0.004 {
			kept = append(kept, candidate)
		}
	}
	// Point budgets grow with coastline length, so a part's share follows the
	// square root of its area: Sardinia earns a third of Italy, not a twelfth.
	weights := make([]float64, len(kept))
	var totalWeight float64
	for index, candidate := range kept {
		weights[index] = math.Sqrt(candidate.area / kept[0].area)
		totalWeight += weights[index]
	}
	pointBudgets := make([]int, len(kept))
	totalPointBudget := 0
	for index := range kept {
		pointBudgets[index] = int(math.Round(float64(modernMapTerritoryPointBudget) * weights[index] / totalWeight))
		if pointBudgets[index] < modernMapMinRegionPoints {
			pointBudgets[index] = modernMapMinRegionPoints
		}
		totalPointBudget += pointBudgets[index]
	}
	for totalPointBudget > modernMapTerritoryPointBudget {
		largest := 0
		for index := range pointBudgets {
			if pointBudgets[index] > pointBudgets[largest] {
				largest = index
			}
		}
		if pointBudgets[largest] <= modernMapMinRegionPoints {
			break
		}
		pointBudgets[largest]--
		totalPointBudget--
	}
	rings := make([][][2]float64, 0, len(kept))
	for index, candidate := range kept {
		points := simplifyModernMapRing(candidate.points, pointBudgets[index]+2)
		if len(points) >= 3 {
			rings = append(rings, points)
		}
	}
	return rings
}

// modernMapPathGeometry turns canvas-space rings into a local path with a
// closed subpath per ring, plus the layer size and top-left position that place
// that bbox back on the canvas. The duplicated closing vertex is dropped:
// `close` owns it.
func modernMapPathGeometry(rings [][][2]float64, canvasWidth, canvasHeight int) ([]LayerPathCommand, []float64, []float64) {
	minX, minY, maxX, maxY := modernMapRingsBounds(rings)
	if math.IsInf(minX, 1) || maxX-minX < 1 || maxY-minY < 1 {
		return nil, nil, nil
	}
	commands := make([]LayerPathCommand, 0, 64)
	for _, ring := range rings {
		points := ring
		if len(points) > 1 && points[0] == points[len(points)-1] {
			points = points[:len(points)-1]
		}
		if len(points) < 3 {
			continue
		}
		commands = append(commands, LayerPathCommand{Type: "move_to", Point: []float64{points[0][0] - minX, points[0][1] - minY}})
		for _, point := range points[1:] {
			commands = append(commands, LayerPathCommand{Type: "line_to", Point: []float64{point[0] - minX, point[1] - minY}})
		}
		commands = append(commands, LayerPathCommand{Type: "close"})
	}
	if len(commands) == 0 {
		return nil, nil, nil
	}
	return commands, []float64{maxX - minX, maxY - minY}, []float64{
		minX - float64(canvasWidth)/2,
		minY - float64(canvasHeight)/2,
	}
}

func modernMapRingsBounds(rings [][][2]float64) (minX, minY, maxX, maxY float64) {
	bounds, ok := mapgeom.BoundsOfRings(rings)
	if !ok {
		return math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	}
	return bounds.MinX, bounds.MinY, bounds.MaxX, bounds.MaxY
}

func modernMapRingArea(points [][2]float64) float64 {
	return mapgeom.RingArea(points)
}

func modernMapRingBounds(points [][2]float64) (minX, minY, maxX, maxY float64) {
	return mapgeom.RingBounds(points)
}

func modernMapCleanRing(points [][2]float64) [][2]float64 {
	return mapgeom.CleanRing(points)
}

func simplifyModernMapRing(points [][2]float64, limit int) [][2]float64 {
	return mapgeom.SimplifyRing(points, limit)
}

func decimateModernMapRing(points [][2]float64, limit int) [][2]float64 {
	return mapgeom.DecimateRing(points, limit)
}
