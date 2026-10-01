package geo

import (
	"math"
	"testing"
)

// mercatorReference is the contract shared with map_cartography.py's
// MERCATOR_REFERENCE and ChrononMotion3D's tests/motion_geospatial.cpp (and,
// on the PipelineGen side, kernel/geodesy). Each row is
// (lat, lon, zoom, global pixel X, global pixel Y).
var mercatorReference = []struct {
	lat, lon float64
	zoom     int
	x, y     float64
	name     string
}{
	{41.890210, 12.492231, 14, 2242697.0401450666, 1558716.8764844453, "Colosseo"},
	{29.86, -92.70, 8, 15892.48, 27067.93732941762, "Gulf coast"},
	{33.9598, -83.3768, 16, 4502967.491697778, 6704228.367772281, "Athens, GA"},
	{0.0, 0.0, 0, 128.0, 128.0, "the grid origin"},
}

// TestMercatorMatchesCertifiedReferencePixels pins the worker's projection
// against the pixels the Python cartography tool and the C++ motion engine
// assert. If this drifts, every pin the producer validated lands somewhere
// else at render time.
func TestMercatorMatchesCertifiedReferencePixels(t *testing.T) {
	for _, row := range mercatorReference {
		x, y := LatLonToGlobalPixel(row.lat, row.lon, row.zoom)
		if math.Abs(x-row.x) > 1e-6 {
			t.Errorf("%s: global pixel X = %.12f, want %.12f", row.name, x, row.x)
		}
		if math.Abs(y-row.y) > 1e-6 {
			t.Errorf("%s: global pixel Y = %.12f, want %.12f", row.name, y, row.y)
		}
	}
}

// TestMercatorRoundTrip pins invertibility to 1e-9 degrees.
func TestMercatorRoundTrip(t *testing.T) {
	for _, row := range mercatorReference {
		x, y := LatLonToGlobalPixel(row.lat, row.lon, row.zoom)
		lat, lon := GlobalPixelToLatLon(x, y, row.zoom)
		if math.Abs(lat-row.lat) > 1e-9 {
			t.Errorf("%s: round-trip latitude = %.12f, want %.12f", row.name, lat, row.lat)
		}
		if math.Abs(lon-row.lon) > 1e-9 {
			t.Errorf("%s: round-trip longitude = %.12f, want %.12f", row.name, lon, row.lon)
		}
	}
}

// TestCertifiedGulfCorridorWindow pins the screen-space pixels of the
// certified I-10 corridor window (center 29.86/-92.70, zoom 8, 1920×1080):
// generate_geospatial_tests.py asserts Houston ~[474.0, 560.9] and
// New Orleans ~[1445.7, 517.9]; the map contract documents them rounded as
// Houston→[474,561], New Orleans→[1446,518]. The worker must place the same
// pixels the producer validated, or a certified plan renders off-plate.
func TestCertifiedGulfCorridorWindow(t *testing.T) {
	w := CenteredOn(29.86, -92.70, 8, 1920, 1080)
	cx, cy := w.ToRaster(29.86, -92.70)
	if math.Abs(cx-960.0) > 0.01 || math.Abs(cy-540.0) > 0.01 {
		t.Errorf("window centre raster = (%.4f, %.4f), want (960, 540)", cx, cy)
	}
	cases := []struct {
		name     string
		lat, lon float64
		wantX    int
		wantY    int
	}{
		{"Houston", 29.7604, -95.3698, 474, 561},
		{"New Orleans", 29.9654, -90.0321, 1446, 518},
	}
	for _, tc := range cases {
		x, y := w.ToRaster(tc.lat, tc.lon)
		if int(math.Round(x)) != tc.wantX || int(math.Round(y)) != tc.wantY {
			t.Errorf("%s raster pixel = (%.2f, %.2f), want rounded (%d, %d)", tc.name, x, y, tc.wantX, tc.wantY)
		}
		if !w.Contains(tc.lat, tc.lon) {
			t.Errorf("%s must fall inside the certified window", tc.name)
		}
	}
}

// TestWindowRejectsOutOfWindowPoints pins the fail-closed predicate: a route
// point outside the plate window must be detectable.
func TestWindowRejectsOutOfWindowPoints(t *testing.T) {
	w := CenteredOn(29.86, -92.70, 8, 1920, 1080)
	if w.Contains(48.8566, 2.3522) {
		t.Error("Paris must not fall inside the Gulf-coast window")
	}
	if !w.Contains(29.7604, -95.3698) {
		t.Error("Houston must fall inside the Gulf-coast window")
	}
}
