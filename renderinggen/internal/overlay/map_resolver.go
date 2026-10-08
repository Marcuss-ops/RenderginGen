package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

// resolvedMap is the compiler-facing projection of a validated public map
// contract. It keeps geographic decisions and screen-space label placement
// together, leaving lowering to emit renderer layers without re-resolving them.
type resolvedMap struct {
	RasterWidth   int
	RasterHeight  int
	StaticAssetID string
	LODs          []SemanticMapLOD
	Center        SemanticMapPoint
	Zoom          int
	Pins          []resolvedMapPin
	Routes        []resolvedMapRoute
	Camera        *SemanticMapCameraMove
	Motion        *LayerAnimation
	Attribution   string
}

type resolvedMapRoute struct {
	Contract SemanticMapRoute
	Origin   SemanticMapPoint
	Zoom     int
}

type resolvedMapPin struct {
	Contract     SemanticMapPin
	RasterX      float64
	RasterY      float64
	LabelTopLeft [2]float64
	LabelWidth   float64
	LabelHeight  float64
	Motion       *LayerAnimation
}

// resolveMap consumes a contract already admitted by validateMapContract and
// resolves map-owned geographic viewport and label geometry. Public wire types
// remain SemanticMap; this is an internal intermediate representation only.
func resolveMap(contract *SemanticMap, assets []SemanticAssetRef, canvasWidth, canvasHeight int, durationFrames int64) (*resolvedMap, error) {
	if contract == nil {
		return nil, fmt.Errorf("map declaration is required")
	}
	window := geo.CenteredOn(contract.Center.Latitude, contract.Center.Longitude, contract.Zoom, contract.Width, contract.Height)
	var placements map[string][2]float64
	if len(contract.Pins) > 0 {
		var err error
		placements, err = resolveMapLabelPlacements(contract.Pins, window, canvasWidth, canvasHeight)
		if err != nil {
			return nil, fmt.Errorf("label placement: %w", err)
		}
	} else {
		placements = map[string][2]float64{}
	}
	resolvedPins := make([]resolvedMapPin, 0, len(contract.Pins))
	for _, pin := range contract.Pins {
		x, y := window.ToRaster(pin.Latitude, pin.Longitude)
		topLeft := placements[pin.ID]
		width, height := mapPinLabelDimensions(pin, canvasWidth, canvasHeight)
		var pinMotion *LayerAnimation
		if contract.MotionID != "" {
			var err error
			pinMotion, err = imageMotionAnimation(contract.MotionID, nil, durationFrames, 0, pin.ID, "map_view")
			if err != nil {
				return nil, fmt.Errorf("pin %q motion: %w", pin.ID, err)
			}
		}
		resolvedPins = append(resolvedPins, resolvedMapPin{
			Contract: pin, RasterX: x, RasterY: y, LabelTopLeft: topLeft, LabelWidth: width, LabelHeight: height,
			Motion: pinMotion,
		})
	}
	staticAssetID := ""
	if len(assets) > 0 {
		staticAssetID = assets[0].ID
	}
	resolvedRoutes := make([]resolvedMapRoute, 0, len(contract.Routes))
	for _, route := range contract.Routes {
		if contract.CameraMove == nil {
			return nil, fmt.Errorf("route %q cannot resolve without a camera viewport", route.ID)
		}
		resolvedRoutes = append(resolvedRoutes, resolvedMapRoute{
			Contract: route, Origin: contract.CameraMove.From, Zoom: int(contract.CameraMove.StartZoom),
		})
	}
	var mapMotion *LayerAnimation
	if contract.MotionID != "" {
		var err error
		mapMotion, err = imageMotionAnimation(contract.MotionID, nil, durationFrames, 0, "map", "map_view")
		if err != nil {
			return nil, fmt.Errorf("map motion: %w", err)
		}
	}
	return &resolvedMap{
		RasterWidth: contract.Width, RasterHeight: contract.Height,
		StaticAssetID: staticAssetID, LODs: append([]SemanticMapLOD(nil), contract.LODs...),
		Center: contract.Center, Zoom: contract.Zoom, Pins: resolvedPins,
		Routes: resolvedRoutes, Camera: contract.CameraMove,
		Motion: mapMotion, Attribution: contract.Attribution,
	}, nil
}
