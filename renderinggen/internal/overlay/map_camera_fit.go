package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
)

// resolveFeatureCameraMove turns a Natural Earth feature selector into the
// existing camera_move contract. It computes the maximum zoom at which the
// feature bounds fit inside the padded output viewport, then requires the
// producer's final offline LOD to be no more detailed than that fit. It never
// fetches tiles or silently falls back to a guessed place.
func resolveFeatureCameraMove(registry *geofeatures.Registry, selector string, padding float64, from SemanticMapPoint, startZoom int, finalLODZoom, canvasWidth, canvasHeight int) (SemanticMapCameraMove, geofeatures.Feature, error) {
	if registry == nil {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("feature registry is required")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("feature is required")
	}
	if !finite(padding) || padding < 0 || padding >= 0.5 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("padding must be finite and within [0,0.5)")
	}
	if canvasWidth <= 0 || canvasHeight <= 0 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("canvas dimensions must be positive")
	}
	feature, err := resolveFeature(registry, selector)
	if err != nil {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, err
	}
	westX, northY := geo.LatLonToGlobalPixel(feature.Bounds.North, feature.Bounds.West, 0)
	eastX, southY := geo.LatLonToGlobalPixel(feature.Bounds.South, feature.Bounds.East, 0)
	width, height := math.Abs(eastX-westX), math.Abs(southY-northY)
	if !finite(width) || !finite(height) || width <= 0 || height <= 0 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("feature %q has bounds that cannot be fitted in Web Mercator", feature.ID)
	}
	usable := 1 - 2*padding
	fitZoom := math.Min(math.Log2(float64(canvasWidth)*usable/width), math.Log2(float64(canvasHeight)*usable/height))
	if !finite(fitZoom) || fitZoom < 0 || fitZoom > 18 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("feature %q fit zoom %.3f is outside supported [0,18]", feature.ID, fitZoom)
	}
	if startZoom < 0 || startZoom > 18 || finalLODZoom <= startZoom || finalLODZoom > 18 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("offline LOD zoom range %d..%d cannot satisfy a feature fly-to", startZoom, finalLODZoom)
	}
	if float64(finalLODZoom) > fitZoom+1e-9 {
		return SemanticMapCameraMove{}, geofeatures.Feature{}, fmt.Errorf("feature %q fits at zoom %.3f, but final offline LOD zoom %d would crop its bounds", feature.ID, fitZoom, finalLODZoom)
	}
	return SemanticMapCameraMove{
		From: from, To: SemanticMapPoint{Latitude: feature.Centroid[1], Longitude: feature.Centroid[0]},
		StartZoom: float64(startZoom), EndZoom: float64(finalLODZoom),
	}, feature, nil
}

func resolveFeature(registry *geofeatures.Registry, selector string) (geofeatures.Feature, error) {
	if strings.HasPrefix(selector, "country:") || strings.HasPrefix(selector, "admin1:") || selector == geofeatures.WorldID {
		return registry.ByID(selector)
	}
	return registry.ByName(selector)
}
