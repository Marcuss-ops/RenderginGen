package mapgeom

import (
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

// WrapDelta keeps a plane delta inside [-worldSize/2, worldSize/2]. It is the
// antimeridian wrap used by camera moves and projected pins.
func WrapDelta(value, worldSize float64) float64 {
	if value > worldSize/2 {
		value -= worldSize
	}
	if value < -worldSize/2 {
		value += worldSize
	}
	return value
}

// WorldPoint projects a latitude/longitude into plane coordinates relative to
// an origin at the same zoom, wrapping the horizontal delta across the
// antimeridian. The vertical axis is inverted for the canvas.
func WorldPoint(latitude, longitude float64, zoom int, originLatitude, originLongitude float64) (float64, float64) {
	x, y := geo.LatLonToGlobalPixel(latitude, longitude, zoom)
	originX, originY := geo.LatLonToGlobalPixel(originLatitude, originLongitude, zoom)
	return WrapDelta(x-originX, geo.MercatorTileSize*math.Pow(2, float64(zoom))), -(y - originY)
}

// RectOverlap reports whether two [minX,minY,maxX,maxY] rectangles overlap
// after expanding both by gap.
func RectOverlap(a, b [4]float64, gap float64) bool {
	return a[0] < b[2]+gap && a[2]+gap > b[0] &&
		a[1] < b[3]+gap && a[3]+gap > b[1]
}

// TypographyScale maps a canvas area onto the 1280x720 reference, clamped to a
// readable range.
func TypographyScale(canvasWidth, canvasHeight int) float64 {
	scale := math.Sqrt(float64(canvasWidth*canvasHeight) / float64(1280*720))
	return math.Max(0.65, math.Min(2.2, scale))
}
