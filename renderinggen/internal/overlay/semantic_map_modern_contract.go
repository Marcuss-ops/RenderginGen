package overlay

import (
	"fmt"
	"strings"
)

// semantic_map_modern_contract.go owns the ModernMap1 family's declared
// constants and palettes plus the request-side validation: which map_type and
// place are accepted, and which pins/routes/regions a plate may carry. Layer
// construction lives in semantic_map_modern.go and
// semantic_map_modern_layers.go.

const (
	// MapTypeClassic is the default raster map (operator-supplied PNG plate).
	MapTypeClassic = "classic"
	// MapTypeModernMap1 is the vector territory map.
	MapTypeModernMap1 = "ModernMap1"

	modernMapMaxPlaceBytes = 200
	// The territory ring is simplified to a point budget: one even-odd fill
	// path replaces the former per-triangle layers, while halo, outline and
	// fill all consume the same ring so the accent cannot drift off its own
	// border.
	modernMapTerritoryPointBudget  = 210
	modernMapMinRegionPoints       = 8
	modernMapContextRingPoints     = 300
	modernMapMaxContextLayers      = 72
	modernMapMaxBorderPoints       = 2200
	modernMapMaxBorderPointsPerRow = 900
	modernMapLogoMinSizePX         = 96.0
	modernMapLogoMaxSizePX         = 200.0
	modernMapLabelFontPX           = 88.0
	modernMapCreditFontPX          = 20.0
	modernMapMarginPX              = 84.0
)

// ModernMap1 presentation constants. They are worker-owned: the payload chooses
// the place and the logo, never the drawing palette.
var (
	// Paper sea and the highlighted territory: the ChrononPlugin editorial
	// plate reads as paper, never as a neon overlay.
	modernMapPaper         = []float64{0.973, 0.965, 0.945, 1.0} // #F8F6F1
	modernMapTerritory     = []float64{0.851, 0.239, 0.204, 1.0} // #D93D34
	modernMapLogoOpacity   = 0.95
	modernMapTerritoryLine = "#8E241D"
	modernMapHaloLine      = "#D93D34"
	modernMapBorderLine    = "#C2BEB2"
	modernMapGridLine      = "#E6E2D6"
	modernMapInk           = "#1F252A"
	modernMapCreditInk     = "#6E6A60"
)

func modernMapType(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, ok := item.Params["map_type"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func isModernMap1(item semanticItem) bool {
	return modernMapType(item) == MapTypeModernMap1
}

func modernMapPlace(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, _ := item.Params["map_place"].(string)
	return strings.TrimSpace(raw)
}

func modernMapLogo(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, _ := item.Params["map_logo"].(string)
	return strings.TrimSpace(raw)
}

// validateMapTypeParam is called before any family-specific rule so an unknown
// map_type is always reported as such, not as a missing raster. It also keeps
// map_place from silently decorating the classic family, where it would have no
// effect.
func validateMapTypeParam(item semanticItem) error {
	if item.Params == nil {
		return nil
	}
	raw, present := item.Params["map_type"]
	if present {
		mapType, ok := raw.(string)
		if !ok || (mapType != MapTypeClassic && mapType != MapTypeModernMap1) {
			return fmt.Errorf("overlay: map item %q declares unsupported map_type %v", item.ID, raw)
		}
	}
	if rawPlace, hasPlace := item.Params["map_place"]; hasPlace {
		place, ok := rawPlace.(string)
		if !ok || strings.TrimSpace(place) == "" || len(place) > modernMapMaxPlaceBytes || !isSingleLineVisibleText(place) {
			return fmt.Errorf("overlay: map item %q map_place must be non-blank single-line text of at most %d bytes", item.ID, modernMapMaxPlaceBytes)
		}
		if modernMapType(item) != MapTypeModernMap1 {
			return fmt.Errorf("overlay: map item %q declares map_place without map_type %s", item.ID, MapTypeModernMap1)
		}
	}
	return nil
}

// validateModernMap1Contract admits the vector contract. A ModernMap1 item
// carries provenance, a place and at most one logo asset, plus the unified
// overlay set: grounded pins, static routes projected through the vector fit,
// an optional centered map_view motion and the certified presentation
// passthrough (map_animation/map_camera_animation/map_backend/...). Raster
// camera moves and LODs stay raster-only: the vector fit owns the viewport.
func validateModernMap1Contract(item semanticItem, canvasWidth, canvasHeight int) error {
	m := item.Map
	if strings.ToLower(strings.TrimSpace(m.Provider)) != "local" {
		return fmt.Errorf("overlay: map item %q provider %q is not a locally supplied Natural Earth catalog", item.ID, m.Provider)
	}
	if strings.TrimSpace(m.SourceID) == "" || strings.TrimSpace(m.SourceLicense) == "" {
		return fmt.Errorf("overlay: map item %q requires source_id and source_license provenance", item.ID)
	}
	if err := validateMapAttribution(m.Attribution); err != nil {
		return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
	}
	if m.Width != canvasWidth || m.Height != canvasHeight {
		return fmt.Errorf("overlay: ModernMap1 item %q viewport %dx%d must match the %dx%d canvas", item.ID, m.Width, m.Height, canvasWidth, canvasHeight)
	}
	if err := validateMapPoint(m.Center.Latitude, m.Center.Longitude); err != nil {
		return fmt.Errorf("overlay: map item %q center: %w", item.ID, err)
	}
	if m.Zoom < 0 || m.Zoom > maxMapZoom {
		return fmt.Errorf("overlay: map item %q zoom %d outside [0,%d]", item.ID, m.Zoom, maxMapZoom)
	}
	place := modernMapPlace(item)
	if place == "" {
		return fmt.Errorf("overlay: ModernMap1 item %q requires params.map_place", item.ID)
	}
	if len(place) > modernMapMaxPlaceBytes || !isSingleLineVisibleText(place) {
		return fmt.Errorf("overlay: ModernMap1 item %q map_place must be non-blank single-line text of at most %d bytes", item.ID, modernMapMaxPlaceBytes)
	}
	if m.CameraMove != nil || len(m.LODs) != 0 {
		return fmt.Errorf("overlay: ModernMap1 item %q cannot combine generated territory with camera LODs; the vector fit owns the viewport", item.ID)
	}
	if err := validateModernMap1PinsAndRoutes(item); err != nil {
		return err
	}
	if strings.TrimSpace(m.MotionID) != "" && !motionAdmitsTarget(m.MotionID, TargetMapView) {
		return fmt.Errorf("overlay: map item %q motion %q is not a registered centered image motion", item.ID, m.MotionID)
	}
	if err := validateMapPresentationParams(item); err != nil {
		return err
	}
	logo := modernMapLogo(item)
	if logo == "" {
		if len(item.Assets) != 0 {
			return fmt.Errorf("overlay: ModernMap1 item %q declares assets without params.map_logo", item.ID)
		}
		return nil
	}
	if len(item.Assets) != 1 || item.Assets[0].ID != logo || !validMapPNGAsset(item.Assets[0]) {
		return fmt.Errorf("overlay: ModernMap1 item %q map_logo %q must name its unique content-addressed local image/png asset", item.ID, logo)
	}
	return nil
}

// validateModernMap1PinsAndRoutes admits the unified overlay set on the vector
// plate: grounded pins validated like classic pins, and static routes with the
// same id/stop/color contract but projected through the vector fit (no
// camera_move/LOD window required).
func validateModernMap1PinsAndRoutes(item semanticItem) error {
	m := item.Map
	if len(m.Pins) > maxMapPins {
		return fmt.Errorf("overlay: map item %q declares %d pins; the contract allows at most %d", item.ID, len(m.Pins), maxMapPins)
	}
	seen := make(map[string]struct{}, len(m.Pins))
	for index, pin := range m.Pins {
		if err := validateMapPoint(pin.Latitude, pin.Longitude); err != nil {
			return fmt.Errorf("overlay: map item %q pin[%d]: %w", item.ID, index, err)
		}
		if pin.ID == "" || len(pin.ID) > maxMapPinIDBytes || !isSingleLineVisibleText(pin.ID) {
			return fmt.Errorf("overlay: map item %q pin[%d] id must be unique, non-empty single-line text of at most %d bytes", item.ID, index, maxMapPinIDBytes)
		}
		if _, duplicate := seen[pin.ID]; duplicate {
			return fmt.Errorf("overlay: map item %q pin[%d] duplicates id %q", item.ID, index, pin.ID)
		}
		seen[pin.ID] = struct{}{}
		if strings.TrimSpace(pin.Label) == "" || len(pin.Label) > maxMapPinLabelBytes || !isSingleLineVisibleText(pin.Label) {
			return fmt.Errorf("overlay: map item %q pin %q label must be non-empty single-line text of at most %d bytes", item.ID, pin.ID, maxMapPinLabelBytes)
		}
		if !isHexColor(pin.Color) {
			return fmt.Errorf("overlay: map item %q pin %q color must be #RRGGBB", item.ID, pin.ID)
		}
		if pin.RadiusPX <= 0 || pin.RadiusPX > 128 {
			return fmt.Errorf("overlay: map item %q pin %q radius_px must be in (0,128]", item.ID, pin.ID)
		}
	}
	if len(m.Routes) > maxMapRouteCount {
		return fmt.Errorf("overlay: map item %q declares %d routes; the contract allows at most %d", item.ID, len(m.Routes), maxMapRouteCount)
	}
	seenRoutes := make(map[string]bool, len(m.Routes))
	for routeIndex, route := range m.Routes {
		if route.ID == "" || len(route.ID) > 128 || !isSingleLineVisibleText(route.ID) || seenRoutes[route.ID] {
			return fmt.Errorf("overlay: map item %q route[%d] id must be unique, non-empty single-line text of at most 128 bytes", item.ID, routeIndex)
		}
		seenRoutes[route.ID] = true
		if len(route.Stops) < 2 || len(route.Stops) > maxMapRouteStops || !isHexColor(route.Color) || !finite(route.WidthPX) || route.WidthPX <= 0 || route.WidthPX > 64 {
			return fmt.Errorf("overlay: map item %q route[%d] requires 2..%d stops, #RRGGBB color, and width_px in (0,64]", item.ID, routeIndex, maxMapRouteStops)
		}
		for stopIndex, stop := range route.Stops {
			if err := validateMapPoint(stop.Latitude, stop.Longitude); err != nil {
				return fmt.Errorf("overlay: map item %q route[%d] stop[%d]: %w", item.ID, routeIndex, stopIndex, err)
			}
		}
	}
	return nil
}
