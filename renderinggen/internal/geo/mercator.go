// Package geo — mercator.go is RenderingGen's Web Mercator (EPSG:3857)
// georeferencing: the ~30 lines that put a pin, a route point and a plate
// window on the same pixel the raster painted.
//
// The worker is the LAST line of the overlay contract: it validates the map
// item's params.map fail-closed, so it must be able to recompute the plate
// window itself instead of trusting the producer's word about where the
// geography lands. The formula is the slippy-tile convention of
// Chronon3d/tools/cartography/map_cartography.py (the tool that renders the
// plate) and ChrononMotion3D's WebMercator.hpp (the engine that flies over
// it): a global pixel grid of 256 * 2^zoom tiles, +x east, +y south,
// latitudes clamped to the Mercator limit — the same operations in the same
// order, so the float64 results agree bit for bit and the shared reference
// pixels hold on all three sides.
package geo

import "math"

const (
	// MercatorLatitudeLimit is the latitude beyond which Mercator has no
	// finite image (the web map convention; atan(sinh(pi)) in degrees).
	MercatorLatitudeLimit = 85.05112877980659
	// MercatorTileSize is one tile edge in pixels at every zoom level.
	MercatorTileSize = 256.0
)

// LatLonToGlobalPixel converts WGS84 (lat, lon) to global Mercator pixel
// coordinates at the given zoom. Latitudes clamp to the Mercator limit and
// longitudes clamp to the antimeridian: a window may run past the pole and
// the date line, it never wraps.
func LatLonToGlobalPixel(lat, lon float64, zoom int) (x, y float64) {
	scale := MercatorTileSize * math.Pow(2, float64(zoom))
	if lon < -180 {
		lon = -180
	} else if lon > 180 {
		lon = 180
	}
	if lat < -MercatorLatitudeLimit {
		lat = -MercatorLatitudeLimit
	} else if lat > MercatorLatitudeLimit {
		lat = MercatorLatitudeLimit
	}
	x = (lon + 180.0) / 360.0 * scale
	latRad := lat * math.Pi / 180.0
	y = (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) * 0.5 * scale
	return x, y
}

// GlobalPixelToLatLon is the inverse of LatLonToGlobalPixel.
func GlobalPixelToLatLon(x, y float64, zoom int) (lat, lon float64) {
	scale := MercatorTileSize * math.Pow(2, float64(zoom))
	lon = (x/scale)*360.0 - 180.0
	n := math.Pi - 2.0*math.Pi*(y/scale)
	lat = math.Atan(math.Sinh(n)) * 180.0 / math.Pi
	return lat, lon
}

// Window is the geography a raster basemap covers: the global pixel of the
// plate's (0,0) corner plus its pixel size. It mirrors
// ChrononMotion3D's MapWindow, so the worker's fail-closed validation and the
// producer's window describe one window in one vocabulary.
type Window struct {
	Zoom               int
	TopLeftX, TopLeftY float64
	Width, Height      float64
}

// CenteredOn returns the window of the given raster size centred on a
// geographic point — map_cartography.py's window_origin (center - size/2).
func CenteredOn(lat, lon float64, zoom, width, height int) Window {
	cx, cy := LatLonToGlobalPixel(lat, lon, zoom)
	return Window{
		Zoom:     zoom,
		TopLeftX: cx - float64(width)/2.0,
		TopLeftY: cy - float64(height)/2.0,
		Width:    float64(width),
		Height:   float64(height),
	}
}

// ToRaster positions a geographic point inside the plate, in plate pixels
// (+x east, +y south, origin at the top-left corner) —
// map_cartography.py's GeoreferencedMap.to_screen.
func (w Window) ToRaster(lat, lon float64) (x, y float64) {
	gx, gy := LatLonToGlobalPixel(lat, lon, w.Zoom)
	return gx - w.TopLeftX, gy - w.TopLeftY
}

// Contains reports whether a geographic point falls INSIDE the plate window
// (inclusive of the edges). The map contract is fail-closed on it: a pin or
// route point outside the plate window is a producer bug and rejects the
// plan — never a marker drawn off the plate.
func (w Window) Contains(lat, lon float64) bool {
	x, y := w.ToRaster(lat, lon)
	return x >= 0 && y >= 0 && x <= w.Width && y <= w.Height
}
