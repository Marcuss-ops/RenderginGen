package overlay

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

const (
	maxMapRouteCount        = 128
	maxMapRouteStops        = 128
	maxMapRouteSamples      = 256
	maxMapRouteTotalSamples = 8192
	mapRouteMaxSegmentDeg   = 2.0
)

// mapRouteGeoSamples performs bounded spherical interpolation. Antipodal stops
// have no unique shortest great circle, so they are rejected rather than
// producing unstable coordinates.
func mapRouteGeoSamples(stops []SemanticMapPoint) ([]SemanticMapPoint, error) {
	if len(stops) < 2 || len(stops) > maxMapRouteStops {
		return nil, fmt.Errorf("route requires 2..%d stops", maxMapRouteStops)
	}
	for index, stop := range stops {
		if err := validateMapPoint(stop.Latitude, stop.Longitude); err != nil {
			return nil, fmt.Errorf("stop[%d]: %w", index, err)
		}
	}
	points := make([]SemanticMapPoint, 0, len(stops))
	points = append(points, stops[0])
	const maxSegmentRadians = mapRouteMaxSegmentDeg * math.Pi / 180
	for index := 1; index < len(stops); index++ {
		from, to := stops[index-1], stops[index]
		distance := greatCircleDistanceRadians(from, to)
		if math.Pi-distance < 1e-8 {
			return nil, fmt.Errorf("stops[%d:%d] are antipodal and do not define a unique great circle", index-1, index)
		}
		steps := int(math.Ceil(distance / maxSegmentRadians))
		if steps < 1 {
			steps = 1
		}
		if len(points)+steps > maxMapRouteSamples {
			return nil, fmt.Errorf("route requires more than %d great-circle samples", maxMapRouteSamples)
		}
		for step := 1; step <= steps; step++ {
			point := greatCircleInterpolate(from, to, float64(step)/float64(steps))
			if err := validateMapPoint(point.Latitude, point.Longitude); err != nil {
				return nil, fmt.Errorf("interpolated sample is invalid: %w", err)
			}
			points = append(points, point)
		}
	}
	return points, nil
}

// validateMapRouteLODCoverage certifies each sampled point against the actual
// supplied plate windows. It never substitutes a tile fetch or clipped route.
func validateMapRouteLODCoverage(routes []SemanticMapRoute, mapSpec *SemanticMap) (int, error) {
	totalSamples := 0
	windows := make([]geo.Window, 0, len(mapSpec.LODs))
	if mapSpec.CameraMove == nil {
		windows = append(windows, geo.CenteredOn(mapSpec.Center.Latitude, mapSpec.Center.Longitude,
			mapSpec.Zoom, mapSpec.Width, mapSpec.Height))
	} else {
		for _, lod := range mapSpec.LODs {
			windows = append(windows, geo.CenteredOn(lod.Center.Latitude, lod.Center.Longitude,
				lod.Zoom, lod.Width, lod.Height))
		}
	}
	for routeIndex, route := range routes {
		points, err := mapRouteGeoSamples(route.Stops)
		if err != nil {
			return totalSamples, fmt.Errorf("route[%d] %q: %w", routeIndex, route.ID, err)
		}
		totalSamples += len(points)
		if totalSamples > maxMapRouteTotalSamples {
			return totalSamples, fmt.Errorf("map routes exceed the aggregate limit of %d great-circle samples", maxMapRouteTotalSamples)
		}
		for lodIndex, window := range windows {
			for pointIndex, point := range points {
				if !window.Contains(point.Latitude, point.Longitude) {
					return totalSamples, fmt.Errorf("route[%d] %q sample[%d] falls outside certified raster LOD[%d]", routeIndex, route.ID, pointIndex, lodIndex)
				}
			}
		}
	}
	return totalSamples, nil
}

// compileMapRoutes lowers each route to a native stroked path in the same
// Mercator camera plane used by map pins and the basemap. Chronon's trim
// operator reveals the authored interval over the map item's local lifetime.
func compileMapRoutes(itemID string, routes []SemanticMapRoute, origin SemanticMapPoint, zoom int, canvasWidth, canvasHeight int, startFrame, durationFrames int64) ([]Layer, error) {
	if len(routes) > maxMapRouteCount {
		return nil, fmt.Errorf("map routes exceed %d entries", maxMapRouteCount)
	}
	layers := make([]Layer, 0, len(routes))
	seen := make(map[string]bool, len(routes))
	for index, route := range routes {
		if route.ID == "" || len(route.ID) > 128 || seen[route.ID] {
			return nil, fmt.Errorf("route[%d] requires a unique non-empty id of at most 128 bytes", index)
		}
		seen[route.ID] = true
		if !isHexColor(route.Color) || !finite(route.WidthPX) || route.WidthPX <= 0 || route.WidthPX > 64 {
			return nil, fmt.Errorf("route %q requires a #RRGGBB color and width_px in (0,64]", route.ID)
		}
		trimStart, trimEnd := 0.0, 1.0
		if route.TrimStart != nil {
			trimStart = *route.TrimStart
		}
		if route.TrimEnd != nil {
			trimEnd = *route.TrimEnd
		}
		if !finite(trimStart) || !finite(trimEnd) || trimStart < 0 || trimEnd > 1 || trimEnd <= trimStart {
			return nil, fmt.Errorf("route %q trim range must satisfy 0 <= trim_start < trim_end <= 1", route.ID)
		}
		points, err := mapRouteGeoSamples(route.Stops)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", route.ID, err)
		}
		commands := make([]LayerPathCommand, 0, len(points))
		for pointIndex, point := range points {
			x, y := mapWorldPoint(point.Latitude, point.Longitude, zoom, origin)
			x += float64(canvasWidth) / 2
			y += float64(canvasHeight) / 2
			commandType := "line_to"
			if pointIndex == 0 {
				commandType = "move_to"
			}
			commands = append(commands, LayerPathCommand{Type: commandType, Point: []float64{x, y}})
		}
		tx, ty, tz := 0.0, 0.0, 0.0
		op := LayerPathOperator{Kind: "trim", Params: LayerTrimParams{Start: trimStart, End: trimStart}}
		op.Params.Animation = &LayerPathAnimation{Easing: "linear", Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: []float64{trimStart, trimStart, 0}},
			{Frame: durationFrames, Value: []float64{trimStart, trimEnd, 0}},
		}}
		layers = append(layers, Layer{
			ID: itemID + ":map_route:" + route.ID, Type: "shape",
			Position: []float64{tx, ty, tz}, Size: []float64{float64(canvasWidth), float64(canvasHeight)},
			StartFrame: startFrame, DurationFrames: durationFrames, Enable3D: true,
			Shape: &LayerShape{Type: "path", Path: commands, Operators: []LayerPathOperator{op}, Stroke: &LayerStroke{Color: route.Color, Width: route.WidthPX}},
		})
	}
	return layers, nil
}

func greatCircleDistanceRadians(from, to SemanticMapPoint) float64 {
	lat1, lat2 := from.Latitude*math.Pi/180, to.Latitude*math.Pi/180
	dLat := lat2 - lat1
	dLon := (to.Longitude - from.Longitude) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * math.Atan2(math.Sqrt(a), math.Sqrt(math.Max(0, 1-a)))
}

func greatCircleInterpolate(from, to SemanticMapPoint, t float64) SemanticMapPoint {
	lat1, lon1 := from.Latitude*math.Pi/180, from.Longitude*math.Pi/180
	lat2, lon2 := to.Latitude*math.Pi/180, to.Longitude*math.Pi/180
	a := [3]float64{math.Cos(lat1) * math.Cos(lon1), math.Cos(lat1) * math.Sin(lon1), math.Sin(lat1)}
	b := [3]float64{math.Cos(lat2) * math.Cos(lon2), math.Cos(lat2) * math.Sin(lon2), math.Sin(lat2)}
	omega := math.Acos(math.Max(-1, math.Min(1, a[0]*b[0]+a[1]*b[1]+a[2]*b[2])))
	if omega < 1e-12 {
		deltaLongitude := math.Mod(to.Longitude-from.Longitude+540, 360) - 180
		return SemanticMapPoint{Latitude: from.Latitude + (to.Latitude-from.Latitude)*t, Longitude: from.Longitude + deltaLongitude*t}
	}
	sinOmega := math.Sin(omega)
	left, right := math.Sin((1-t)*omega)/sinOmega, math.Sin(t*omega)/sinOmega
	x, y, z := left*a[0]+right*b[0], left*a[1]+right*b[1], left*a[2]+right*b[2]
	return SemanticMapPoint{Latitude: math.Atan2(z, math.Hypot(x, y)) * 180 / math.Pi, Longitude: math.Atan2(y, x) * 180 / math.Pi}
}
