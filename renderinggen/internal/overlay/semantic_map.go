// semantic_map.go owns contract admission for georeferenced map items and
// their lowering: basemap provenance, viewport, pins, routes and certified
// LODs are validated here, then a resolved map is lowered to Chronon layers
// together with its pin-label geometry.
//
// A map is not a first-class renderer primitive. It lowers to the primitives
// declared by mapLayoutCatalog and executed by Chronon:
//
//	basemap      one full-canvas image layer of the operator's certified raster
//	             (the plate IS the canvas, so cover neither crops nor stretches)
//	pin          one ellipse shape layer per grounded place
//	pin label    one text layer per pin, the place's own name
//	attribution  one text layer carrying the provider-required credit
//
// Every geometry decision here comes from the plan and from the Web Mercator
// window, which this file RECOMPUTES with internal/geo instead of trusting the
// producer's word: a pin the producer placed on the wrong pixel is rejected,
// not drawn. Nothing is fetched, defaulted or invented — a map whose raster,
// window or attribution the contract cannot honour fails the whole compile.

package overlay

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/clampx"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

// Map contract bounds. They mirror the published overlay-plan.v1 schema, so a
// plan the producer accepted is never rejected here for a bound it already
// enforced, and a hand-written plan cannot smuggle a larger raster past the
// renderer.
const (
	maxMapPins             = 128
	maxMapRasterWidth      = 7680
	maxMapRasterHeight     = 4320
	maxMapZoom             = 22
	maxMapAttributionBytes = 512
	maxMapPinLabelBytes    = 256
	maxMapPinIDBytes       = 128
	maxMapLODCount         = 8
	maxCameraMoveFrames    = 65536
)

// Pin and attribution presentation. These are worker-owned drawing constants,
// not producer inputs: the plan decides WHERE a place is and what it is called,
// never how thick the ring around it is.
const (
	mapPinStrokeWidthPX = 3.0
	// The dot's core is light and its ring carries the contract colour; the
	// stroke also grows with the marker so a large pin keeps the same weight.
	mapPinCoreColor         = "#FFFFFF"
	mapPinRingFallbackColor = "#1F252A"
	mapPinStrokeRadiusRatio = 0.22
	// Place names get a light halo so the contract ink reads on the accent
	// territory as well as on the paper sea.
	mapPinLabelHaloColor     = "#FFFFFF"
	mapPinLabelHaloRatio     = 0.03
	mapPinLabelShadowColor   = "#000000"
	mapPinLabelShadowOpacity = 0.35
	mapPinLabelShadowBlur    = 4.0
	mapPinLabelShadowOffsetY = 1.0
	mapPinLabelHeightPX      = 52.0
	mapPinLabelFitDensity    = 2.0
	// Territory fill reveal: one paper cover wipes the accent in from west to
	// east, and its soft front is a feather of this many pixels.
	//
	// The window is deliberately short: while any frame differs from the
	// previous one the native renderer re-executes the whole graph, so a long
	// reveal costs render time, not quality. Measured on the Italy 1920x1080
	// plate, one executed frame costs ~246 ms (graph_execute 3.9 s over 16
	// frames) against ~0.1 ms for a duplicated one, while the composite pass set
	// itself is unchanged (37 passes, all of them composite). Eight frames keep
	// the sweep fluid at 24 fps and spend a third less than fourteen.
	modernMapFillWipeFrames    = 8
	modernMapFillWipeFeatherPX = 120.0

	mapPinLabelGapPX      = 10.0
	mapPinLabelFontPX     = 34.0
	mapPinLabelMinFontPX  = 12.0
	mapAttributionWidth   = 1200.0
	mapAttributionHeight  = 48.0
	mapAttributionFontPX  = 26.0
	mapAttributionMinFont = 10.0
	mapAttributionMargin  = 24.0
	// A map plate's shadow and fit policy are NOT here: typography belongs to
	// the text system (see text_role.go, mapTextPlateShadow*/mapTextPlateFitMode).
	// This file owns geometry only.
	mapLabelCollisionGap = 6.0
)

// validateMapContract is the single owner of the map item's admission rules. It
// runs for EVERY item: a map declaration on a non-map item is as much a bug as
// a map item without one, and both are rejected here rather than silently
// ignored or silently drawn.
func validateMapContract(item semanticItem, kind ItemKind, canvasWidth, canvasHeight int) error {
	if item.Map == nil {
		if kind == KindMap {
			return fmt.Errorf("overlay: map item %q requires a map declaration", item.ID)
		}
		return nil
	}
	if kind != KindMap {
		return fmt.Errorf("overlay: item %q kind %q cannot carry a map declaration", item.ID, kind)
	}
	m := item.Map
	if err := validateMapTypeParam(item); err != nil {
		return err
	}
	if isModernMap1(item) {
		return validateModernMap1Contract(item, canvasWidth, canvasHeight)
	}
	if strings.ToLower(strings.TrimSpace(m.Provider)) != "local" {
		return fmt.Errorf("overlay: map item %q provider %q is not a locally supplied raster", item.ID, m.Provider)
	}
	if strings.TrimSpace(m.SourceID) == "" || strings.TrimSpace(m.SourceLicense) == "" {
		return fmt.Errorf("overlay: map item %q requires source_id and source_license provenance", item.ID)
	}
	if err := validateMapAttribution(m.Attribution); err != nil {
		return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
	}
	if m.Width <= 0 || m.Height <= 0 {
		return fmt.Errorf("overlay: map item %q raster dimensions must be positive", item.ID)
	}
	if m.Zoom < 0 || m.Zoom > maxMapZoom {
		return fmt.Errorf("overlay: map item %q zoom %d outside [0,%d]", item.ID, m.Zoom, maxMapZoom)
	}
	if m.CameraMove == nil && (m.Width != canvasWidth || m.Height != canvasHeight) {
		return fmt.Errorf("overlay: map item %q static raster %dx%d does not match the %dx%d canvas", item.ID, m.Width, m.Height, canvasWidth, canvasHeight)
	}
	if m.CameraMove != nil && (m.Width < 1 || m.Height < 1) {
		return fmt.Errorf("overlay: map item %q raster dimensions must be positive", item.ID)
	}
	if m.Width > maxMapRasterWidth || m.Height > maxMapRasterHeight {
		return fmt.Errorf("overlay: map item %q raster %dx%d exceeds the contract maximum %dx%d", item.ID, m.Width, m.Height, maxMapRasterWidth, maxMapRasterHeight)
	}
	if strings.TrimSpace(m.MotionID) != "" && !motionAdmitsTarget(m.MotionID, TargetMapView) {
		return fmt.Errorf("overlay: map item %q motion %q is not a registered centered image motion", item.ID, m.MotionID)
	}
	if err := validateMapPoint(m.Center.Latitude, m.Center.Longitude); err != nil {
		return fmt.Errorf("overlay: map item %q center: %w", item.ID, err)
	}
	if err := validateMapLODs(item, canvasWidth, canvasHeight); err != nil {
		return err
	}
	if len(m.Routes) > maxMapRouteCount {
		return fmt.Errorf("overlay: map item %q declares %d routes; the contract allows at most %d", item.ID, len(m.Routes), maxMapRouteCount)
	}
	seenRouteIDs := make(map[string]bool, len(m.Routes))
	for routeIndex, route := range m.Routes {
		if route.ID == "" || len(route.ID) > 128 || !isSingleLineVisibleText(route.ID) || seenRouteIDs[route.ID] {
			return fmt.Errorf("overlay: map item %q route[%d] id must be unique, non-empty single-line text of at most 128 bytes", item.ID, routeIndex)
		}
		seenRouteIDs[route.ID] = true
		if len(route.Stops) < 2 || len(route.Stops) > maxMapRouteStops || !isHexColor(route.Color) || !finite(route.WidthPX) || route.WidthPX <= 0 || route.WidthPX > 64 {
			return fmt.Errorf("overlay: map item %q route[%d] requires 2..%d stops, #RRGGBB color, and width_px in (0,64]", item.ID, routeIndex, maxMapRouteStops)
		}
		for stopIndex, stop := range route.Stops {
			if err := validateMapPoint(stop.Latitude, stop.Longitude); err != nil {
				return fmt.Errorf("overlay: map item %q route[%d] stop[%d]: %w", item.ID, routeIndex, stopIndex, err)
			}
		}
	}
	if m.CameraMove == nil && len(m.Routes) > 0 {
		return fmt.Errorf("overlay: map item %q routes require a certified camera_move/LOD coverage window", item.ID)
	}
	if len(m.Routes) > 0 {
		if _, err := validateMapRouteLODCoverage(m.Routes, m); err != nil {
			return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
		}
	}
	if len(m.Pins) > maxMapPins {
		return fmt.Errorf("overlay: map item %q declares %d pins; the contract allows at most %d", item.ID, len(m.Pins), maxMapPins)
	}
	window := geo.CenteredOn(m.Center.Latitude, m.Center.Longitude, m.Zoom, m.Width, m.Height)
	seen := make(map[string]struct{}, len(m.Pins))
	for index, pin := range m.Pins {
		if err := validateMapPin(pin, index, window); err != nil {
			return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
		}
		if _, duplicate := seen[pin.ID]; duplicate {
			return fmt.Errorf("overlay: map item %q pin[%d] duplicates id %q", item.ID, index, pin.ID)
		}
		seen[pin.ID] = struct{}{}
	}
	return validateMapPresentationParams(item)
}

// Certified runtime map presentation catalogs. A map item may carry its
// RESOLVED presentation (composition family, camera, label treatment, backend,
// group identity and the voiceover's per-pin schedule) in params; the worker
// validates those declarations instead of silently ignoring them, so a
// composition the renderer cannot draw is a compile error, never a blank map.
var (
	// certifiedMapCompositions / certifiedMapCameraAnimations /
	// certifiedMapLabelAnimations are projected from the ChrononPlugin
	// renderer catalog (see maps_renderer_catalog.go); this file does not
	// redeclare the map vocabulary.

	// certifiedMapBackends: "dynamic" is the native dynamic map renderer of
	// ChrononPlugin (the single map lane since the Python renderer's
	// retirement, TICKET-MAPS-NATIVE-PORT fase 7), "native" is the Chronon
	// map-compiler plate path. The retired "python" backend is REJECTED in
	// new work — accepting it would let a stale plan claim a renderer that no
	// longer exists; legacy artifacts that already carried it are only ever
	// imported as inert video (overlay.import).
	certifiedMapBackends        = map[string]bool{"native": true, "dynamic": true}
	certifiedMapGroupStrategies = map[string]bool{"tour": true, "multi_pin": true}
	// certifiedMapFx is the lightweight, primitive-only lane authored by
	// ChrononTemplate's MapFxPack. It is intentionally separate from the
	// ChrononPlugin GPU catalog: these four treatments need no map renderer,
	// only ellipses, rectangles and trimmed paths.
	certifiedMapFx = map[string]bool{
		"area_glow_pulse":        true,
		"radar_location_reveal":  true,
		"light_beam_drop":        true,
		"city_network_expansion": true,
	}
)

// validateMapPresentationParams admits the new certified map presentation
// contract on map items. Every field is optional (absent = legacy behaviour);
// present fields must be internally consistent — per-pin timing may only
// reference declared pins, and a group identity must be coherent.
func validateMapPresentationParams(item semanticItem) error {
	params := item.Params
	if params == nil {
		return nil
	}
	if raw, present := params["map_place"]; present {
		place, ok := raw.(string)
		if !ok || strings.TrimSpace(place) == "" || len(place) > 200 || !isSingleLineVisibleText(place) {
			return fmt.Errorf("overlay: map item %q map_place must be non-blank single-line text of at most 200 bytes", item.ID)
		}
	}
	for key, certified := range map[string]map[string]bool{
		"map_animation":        certifiedMapCompositions,
		"map_camera_animation": certifiedMapCameraAnimations,
		"map_label_animation":  certifiedMapLabelAnimations,
		"map_backend":          certifiedMapBackends,
		"map_group_strategy":   certifiedMapGroupStrategies,
		"map_fx":               certifiedMapFx,
	} {
		raw, present := params[key]
		if !present {
			continue
		}
		value, ok := raw.(string)
		if !ok || value == "" || !certified[value] {
			return fmt.Errorf("overlay: map item %q declares unsupported %s %v", item.ID, key, raw)
		}
	}
	if raw, present := params["map_fx"]; present {
		fx, _ := raw.(string)
		if _, also := params["map_animation"]; also {
			return fmt.Errorf("overlay: map item %q declares both map_fx and map_animation; choose one map treatment", item.ID)
		}
		if item.Map.CameraMove != nil {
			return fmt.Errorf("overlay: map item %q map_fx %q requires a static map (camera_move is not supported)", item.ID, fx)
		}
		pins := len(item.Map.Pins)
		if fx == "city_network_expansion" {
			if pins < 2 || pins > 8 {
				return fmt.Errorf("overlay: map item %q map_fx %q requires 2..8 pins, got %d", item.ID, fx, pins)
			}
		} else if pins != 1 {
			return fmt.Errorf("overlay: map item %q map_fx %q requires exactly one pin, got %d", item.ID, fx, pins)
		}
	}
	pinIDs := make(map[string]bool, len(item.Map.Pins))
	for _, pin := range item.Map.Pins {
		pinIDs[pin.ID] = true
	}
	if raw, present := params["map_pin_timing"]; present {
		timing, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("overlay: map item %q map_pin_timing must be an object keyed by pin id", item.ID)
		}
		for pinID, rawEntry := range timing {
			if !pinIDs[pinID] {
				return fmt.Errorf("overlay: map item %q map_pin_timing references undeclared pin %q", item.ID, pinID)
			}
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return fmt.Errorf("overlay: map item %q map_pin_timing[%q] must be an object", item.ID, pinID)
			}
			startUS, startOK := mapParamNumber(entry, "start_us")
			durationUS, durationOK := mapParamNumber(entry, "duration_us")
			if !startOK || !durationOK || startUS < 0 || durationUS <= 0 {
				return fmt.Errorf("overlay: map item %q map_pin_timing[%q] requires start_us >= 0 and duration_us > 0", item.ID, pinID)
			}
			// Same invariant as PipelineGen ValidateMapPinTiming: a pin must end
			// inside its own map window. Overflow-safe, no silent clamping;
			// PipelineGen prevents this, RenderingGen rejects it. When the
			// item carries no duration (legacy millisecond path unset),
			// there is no window to enforce — skip rather than rejecting
			// a well-formed pin schedule.
			mapDurationUS := item.EndMS*1000 - item.StartMS*1000
			if item.StartUS != nil && item.DurationUS != nil {
				mapDurationUS = *item.DurationUS
			}
			if mapDurationUS > 0 && (startUS > float64(mapDurationUS) || durationUS > float64(mapDurationUS) || startUS+durationUS > float64(mapDurationUS)) {
				return fmt.Errorf("overlay: map item %q map_pin_timing[%q] escapes the map window: start_us=%v duration_us=%v map_duration_us=%d", item.ID, pinID, startUS, durationUS, mapDurationUS)
			}
		}
	}
	if raw, present := params["map_group"]; present {
		group, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("overlay: map item %q map_group must be an object", item.ID)
		}
		index, indexOK := mapParamNumber(group, "index")
		count, countOK := mapParamNumber(group, "count")
		size, sizeOK := mapParamNumber(group, "size")
		if !indexOK || !countOK || !sizeOK || index < 1 || count < 1 || index > count || size < 1 {
			return fmt.Errorf("overlay: map item %q map_group requires 1 <= index <= count and size >= 1", item.ID)
		}
	}
	return nil
}

func mapParamNumber(entry map[string]any, key string) (float64, bool) {
	value, ok := entry[key].(float64)
	return value, ok
}

func validateMapAttribution(attribution string) error {
	if !isSingleLineVisibleText(attribution) {
		return fmt.Errorf("attribution must be non-blank single-line visible text")
	}
	if len(attribution) > maxMapAttributionBytes {
		return fmt.Errorf("attribution exceeds %d bytes", maxMapAttributionBytes)
	}
	return nil
}

func validateMapPin(pin SemanticMapPin, index int, window geo.Window) error {
	if strings.TrimSpace(pin.ID) != pin.ID || pin.ID == "" || len(pin.ID) > maxMapPinIDBytes || !isSingleLineVisibleText(pin.ID) {
		return fmt.Errorf("pin[%d] id must be a non-blank trimmed single-line identifier of at most %d bytes", index, maxMapPinIDBytes)
	}
	if !isSingleLineVisibleText(pin.Label) || len(pin.Label) > maxMapPinLabelBytes {
		return fmt.Errorf("pin[%d] label must be non-blank single-line visible text of at most %d bytes", index, maxMapPinLabelBytes)
	}
	if !isHexColor(pin.Color) {
		return fmt.Errorf("pin[%d] color %q must be #RRGGBB", index, pin.Color)
	}
	if pin.LabelPriority < -1000 || pin.LabelPriority > 1000 {
		return fmt.Errorf("pin[%d] label_priority must be within [-1000,1000]", index)
	}
	if len(pin.LabelOffsetPX) != 0 && len(pin.LabelOffsetPX) != 2 {
		return fmt.Errorf("pin[%d] label_offset_px must contain exactly [x,y]", index)
	}
	for _, offset := range pin.LabelOffsetPX {
		if !finite(offset) || math.Abs(offset) > 2048 {
			return fmt.Errorf("pin[%d] label_offset_px components must be finite and within [-2048,2048]", index)
		}
	}
	if pin.LabelStyle != nil {
		if err := validateMapTextStyle(pin.LabelStyle, index); err != nil {
			return err
		}
	}
	if !finite(pin.RadiusPX) || pin.RadiusPX <= 0 || pin.RadiusPX > 128 {
		return fmt.Errorf("pin[%d] radius_px %v must be finite and within (0,128]", index, pin.RadiusPX)
	}
	if err := validateMapPoint(pin.Latitude, pin.Longitude); err != nil {
		return fmt.Errorf("pin[%d]: %w", index, err)
	}
	if !window.Contains(pin.Latitude, pin.Longitude) {
		return fmt.Errorf("pin[%d] %q falls outside the basemap window", index, pin.Label)
	}
	return nil
}

func validateMapTextStyle(style *SemanticMapTextStyle, index int) error {
	if style.Preset != "" {
		if _, ok := geographicTagStylePreset(style.Preset); !ok {
			return fmt.Errorf("pin[%d] label_style.preset %q is unsupported", index, style.Preset)
		}
	}
	if style.FontFamily != "" {
		if _, ok := runtimeFontPath(style.FontFamily); !ok {
			return fmt.Errorf("pin[%d] label_style.font_family %q is unsupported", index, style.FontFamily)
		}
	}
	if style.FontSizePX != nil && (!finite(*style.FontSizePX) || *style.FontSizePX < mapPinLabelMinFontPX || *style.FontSizePX > 256) {
		return fmt.Errorf("pin[%d] label_style.font_size_px must be finite and within [%g,256]", index, mapPinLabelMinFontPX)
	}
	if style.Fill != "" && !isHexColor(style.Fill) {
		return fmt.Errorf("pin[%d] label_style.fill must be #RRGGBB", index)
	}
	if stroke := style.Stroke; stroke != nil {
		if !isHexColor(stroke.Color) || !finite(stroke.Width) || stroke.Width < 0 || stroke.Width > 16 {
			return fmt.Errorf("pin[%d] label_style.stroke requires #RRGGBB and width in [0,16]", index)
		}
	}
	if shadow := style.Shadow; shadow != nil {
		if !isHexColor(shadow.Color) || !finite(shadow.Opacity) || shadow.Opacity < 0 || shadow.Opacity > 1 ||
			!finite(shadow.Blur) || shadow.Blur < 0 || shadow.Blur > 64 || len(shadow.Offset) != 2 ||
			!finite(shadow.Offset[0]) || !finite(shadow.Offset[1]) || math.Abs(shadow.Offset[0]) > 128 || math.Abs(shadow.Offset[1]) > 128 {
			return fmt.Errorf("pin[%d] label_style.shadow has invalid color/opacity/blur/offset", index)
		}
	}
	if glow := style.Glow; glow != nil {
		if !isHexColor(glow.Color) || !finite(glow.Radius) || glow.Radius < 0 || glow.Radius > 128 ||
			!finite(glow.Intensity) || glow.Intensity < 0 || glow.Intensity > 1 {
			return fmt.Errorf("pin[%d] label_style.glow has invalid color/radius/intensity", index)
		}
	}
	if plate := style.Background; plate != nil {
		if !isHexColor(plate.Color) || !finite(plate.Opacity) || plate.Opacity < 0 || plate.Opacity > 1 ||
			!finite(plate.Radius) || plate.Radius < 0 || plate.Radius > 128 || len(plate.Padding) != 2 ||
			!finite(plate.Padding[0]) || !finite(plate.Padding[1]) || plate.Padding[0] < 0 || plate.Padding[1] < 0 ||
			plate.Padding[0] > 128 || plate.Padding[1] > 128 {
			return fmt.Errorf("pin[%d] label_style.background has invalid color/opacity/radius/padding", index)
		}
	}
	return nil
}

func validateMapPoint(latitude, longitude float64) error {
	if !finite(latitude) || latitude < -90 || latitude > 90 {
		return fmt.Errorf("latitude %v is outside [-90,90]", latitude)
	}
	if !finite(longitude) || longitude < -180 || longitude > 180 {
		return fmt.Errorf("longitude %v is outside [-180,180]", longitude)
	}
	return nil
}

func isSingleLineVisibleText(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validateMapLODs(item semanticItem, canvasWidth, canvasHeight int) error {
	m := item.Map
	if m.CameraMove == nil {
		if len(m.LODs) != 0 || len(item.Assets) != 1 {
			return fmt.Errorf("overlay: static map item %q requires exactly one asset and no LOD/camera metadata", item.ID)
		}
		if !validMapPNGAsset(item.Assets[0]) {
			return fmt.Errorf("overlay: map item %q basemap must be a content-addressed local image/png asset", item.ID)
		}
		return nil
	}
	move := m.CameraMove
	if len(m.LODs) < 2 || len(m.LODs) > maxMapLODCount || len(item.Assets) != len(m.LODs) {
		return fmt.Errorf("overlay: camera map item %q requires 2..%d LODs and a matching asset per LOD", item.ID, maxMapLODCount)
	}
	if m.Width != m.LODs[0].Width || m.Height != m.LODs[0].Height || m.LODs[0].Zoom != m.Zoom || m.LODs[0].Center != m.Center {
		return fmt.Errorf("overlay: camera map item %q base raster must match first LOD", item.ID)
	}
	if move.StartZoom < 0 || move.EndZoom > 18 || move.EndZoom <= move.StartZoom ||
		move.StartZoom != float64(m.LODs[0].Zoom) || move.EndZoom != float64(m.LODs[len(m.LODs)-1].Zoom) {
		return fmt.Errorf("overlay: camera map item %q zoom must increase from first to last LOD", item.ID)
	}
	for name, point := range map[string]SemanticMapPoint{"from": move.From, "to": move.To} {
		if err := validateMapPoint(point.Latitude, point.Longitude); err != nil {
			return fmt.Errorf("overlay: map item %q camera %s: %w", item.ID, name, err)
		}
	}
	for name, value := range map[string]float64{"start_zoom": move.StartZoom, "end_zoom": move.EndZoom, "start_tilt_deg": move.StartTiltDeg, "end_tilt_deg": move.EndTiltDeg, "bearing_deg": move.BearingDeg} {
		if !finite(value) {
			return fmt.Errorf("overlay: map item %q camera %s must be finite", item.ID, name)
		}
	}
	if math.Abs(move.StartTiltDeg) > 80 || math.Abs(move.EndTiltDeg) > 80 || math.Abs(move.BearingDeg) > 360 {
		return fmt.Errorf("overlay: map item %q camera tilt/bearing is outside supported bounds", item.ID)
	}
	seen := make(map[string]bool, len(m.LODs))
	for index, lod := range m.LODs {
		if lod.Zoom < 0 || lod.Zoom > 18 || (index > 0 && lod.Zoom <= m.LODs[index-1].Zoom) || lod.Width < 1 || lod.Height < 1 || lod.Width > maxMapRasterWidth || lod.Height > maxMapRasterHeight {
			return fmt.Errorf("overlay: map item %q LOD[%d] has invalid zoom/order/dimensions", item.ID, index)
		}
		if err := validateMapPoint(lod.Center.Latitude, lod.Center.Longitude); err != nil {
			return fmt.Errorf("overlay: map item %q LOD[%d]: %w", item.ID, index, err)
		}
		if strings.TrimSpace(lod.SourceID) == "" || strings.TrimSpace(lod.SourceLicense) == "" ||
			lod.SourceLicense != m.SourceLicense || lod.Attribution != m.Attribution || validateMapAttribution(lod.Attribution) != nil {
			return fmt.Errorf("overlay: map item %q LOD[%d] provenance and attribution must match the basemap", item.ID, index)
		}
		asset := item.Assets[index]
		if asset.ID != lod.AssetID || seen[lod.AssetID] || !validMapPNGAsset(asset) {
			return fmt.Errorf("overlay: map item %q LOD[%d] must name its unique local content-addressed PNG asset in order", item.ID, index)
		}
		seen[lod.AssetID] = true
		window := geo.CenteredOn(lod.Center.Latitude, lod.Center.Longitude, lod.Zoom, lod.Width, lod.Height)
		lowZoom, highZoom := mapLODActiveZoomRange(index, m.LODs, move)
		if !mapLODWindowCoversMove(window, move, lowZoom, highZoom, canvasWidth, canvasHeight) {
			return fmt.Errorf("overlay: map item %q LOD[%d] does not cover active camera viewport and transition interval", item.ID, index)
		}
		for pinIndex, pin := range m.Pins {
			if !window.Contains(pin.Latitude, pin.Longitude) {
				return fmt.Errorf("overlay: map item %q LOD[%d] does not cover pin[%d] %q", item.ID, index, pinIndex, pin.ID)
			}
		}
	}
	return nil
}

// mapLODFadeHalfBandZoom is the ONE half-width (in zoom stops) of every LOD
// cross-fade. Validation and animation share it: a plate is certified exactly
// over the interval where its opacity is > 0, so a fade can never run outside
// the window the plate actually covers, and a wider band is a single edit that
// softens both sides together.
const mapLODFadeHalfBandZoom = 0.5

func mapLODActiveZoomRange(index int, lods []SemanticMapLOD, move *SemanticMapCameraMove) (float64, float64) {
	var low, high float64
	if index == 0 {
		low = move.StartZoom
	} else {
		low = (float64(lods[index-1].Zoom)+float64(lods[index].Zoom))/2 - mapLODFadeHalfBandZoom
		if low < move.StartZoom {
			low = move.StartZoom
		}
	}
	if index == len(lods)-1 {
		high = move.EndZoom
	} else {
		high = (float64(lods[index].Zoom)+float64(lods[index+1].Zoom))/2 + mapLODFadeHalfBandZoom
		if high > move.EndZoom {
			high = move.EndZoom
		}
	}
	return low, high
}

func mapLODWindowCoversMove(window geo.Window, move *SemanticMapCameraMove, lowZoom, highZoom float64, canvasWidth, canvasHeight int) bool {
	if highZoom < lowZoom || move.EndZoom <= move.StartZoom || canvasWidth <= 0 || canvasHeight <= 0 {
		return false
	}
	fromX, fromY := geo.LatLonToGlobalPixel(move.From.Latitude, move.From.Longitude, window.Zoom)
	toX, toY := geo.LatLonToGlobalPixel(move.To.Latitude, move.To.Longitude, window.Zoom)
	deltaX := wrapMapPlaneDelta(toX-fromX, geo.MercatorTileSize*math.Pow(2, float64(window.Zoom)))
	viewportScale := 1 / math.Max(0.17, math.Cos(math.Pi/180*math.Max(math.Abs(move.StartTiltDeg), math.Abs(move.EndTiltDeg))))
	viewportHalfWidth := float64(canvasWidth) / 2 * viewportScale
	viewportHalfHeight := float64(canvasHeight) / 2 * viewportScale
	zoomFactor := math.Pow(2, move.EndZoom-move.StartZoom)
	toTime := func(zoom float64) float64 {
		return math.Max(0, math.Min(1, (math.Pow(2, zoom-move.StartZoom)-1)/(zoomFactor-1)))
	}
	startT, endT := toTime(lowZoom), toTime(highZoom)
	for step := 0; step <= 64; step++ {
		t := startT + (endT-startT)*float64(step)/64
		zoom := move.StartZoom + math.Log2(1+(zoomFactor-1)*t)
		x := fromX + deltaX*t - window.TopLeftX
		y := fromY + (toY-fromY)*t - window.TopLeftY
		zoomScale := math.Pow(2, float64(window.Zoom)-zoom)
		marginX := viewportHalfWidth * zoomScale
		marginY := viewportHalfHeight * zoomScale
		if x < marginX || y < marginY || x > window.Width-marginX || y > window.Height-marginY {
			return false
		}
	}
	return true
}

func validMapPNGAsset(asset SemanticAssetRef) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(asset.MediaType, ";", 2)[0]))
	assetPath := strings.TrimSpace(asset.URL)
	isLocalPath := assetPath == "" || strings.HasPrefix(assetPath, "assets/")
	return strings.TrimSpace(asset.ID) != "" && len(asset.SHA256) == 64 &&
		strings.Trim(asset.SHA256, "0123456789abcdefABCDEF") == "" && mediaType == "image/png" && isLocalPath
}

// Geospatial-to-canvas label placement and its
// text-box measurements. It deliberately does not compile typography: map
// layout resolves position and box constraints; text roles own appearance.
// resolveMapLabelPlacements computes deterministic screen-space placements for
// all pins before compilation, so placement errors remain contract errors.
func resolveMapLabelPlacements(pins []SemanticMapPin, window geo.Window, canvasW, canvasH int) (map[string][2]float64, error) {
	placements := make(map[string][2]float64, len(pins))
	order := make([]int, 0, len(pins))
	for index := range pins {
		order = append(order, index)
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := pins[order[i]], pins[order[j]]
		if left.LabelPriority != right.LabelPriority {
			return left.LabelPriority > right.LabelPriority
		}
		return left.ID < right.ID
	})
	placed := make([][4]float64, 0, len(order))
	for _, index := range order {
		pin := pins[index]
		x, y := window.ToRaster(pin.Latitude, pin.Longitude)
		x *= float64(canvasW) / float64(window.Width)
		y *= float64(canvasH) / float64(window.Height)
		width, height := mapPinLabelDimensions(pin, canvasW, canvasH)
		candidates := mapPinLabelCandidates(pin, x, y, width, height)
		found := false
		for _, candidate := range candidates {
			left := clampMapBox(candidate[0], float64(canvasW)-width)
			top := clampMapBox(candidate[1], float64(canvasH)-height)
			if math.Abs(left-candidate[0]) >= 0.001 || math.Abs(top-candidate[1]) >= 0.001 {
				continue
			}
			rect := [4]float64{left, top, left + width, top + height}
			collision := false
			for _, previous := range placed {
				if mapLabelRectsOverlap(rect, previous) {
					collision = true
					break
				}
			}
			if !collision {
				placed = append(placed, rect)
				placements[pin.ID] = [2]float64{left, top}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("pin %q label cannot be placed without clipping or colliding; move it or adjust label_offset_px", pin.ID)
		}
	}
	return placements, nil
}

func mapTypographyScale(canvasW, canvasH int) float64 {
	scale := math.Sqrt(float64(canvasW*canvasH) / float64(1280*720))
	return math.Max(0.65, math.Min(2.2, scale))
}

// mapPinLabelDimensions derives the map-owned box from label content, canvas
// scale, and optional background padding; it does not choose text appearance.
func mapPinLabelDimensions(pin SemanticMapPin, canvasW, canvasH int) (float64, float64) {
	labelStyle := resolveGeographicTagStyle(pin.LabelStyle)
	fontSize := mapPinLabelFontPX
	if labelStyle != nil && labelStyle.FontSizePX != nil {
		fontSize = *labelStyle.FontSizePX
	}
	scale := mapTypographyScale(canvasW, canvasH)
	width := math.Max(96*scale, float64(utf8.RuneCountInString(pin.Label))*fontSize*scale*0.68+24*scale)
	height := math.Max(mapPinLabelHeightPX*scale, fontSize*scale*1.6)
	if plate := labelStyle; plate != nil && plate.Background != nil && len(plate.Background.Padding) == 2 {
		width += 2 * plate.Background.Padding[0] * scale
		height += 2 * plate.Background.Padding[1] * scale
	}
	// The native text fitter probes ink bounds at glyph density, which is
	// noticeably wider and taller than this layout estimate. A box sized from
	// the estimate alone makes every place name shrink to its floor, so the
	// authored font size is never what gets drawn. Padding the measured box
	// keeps the placement geometry honest AND renders the intended size.
	width *= mapPinLabelFitDensity
	height *= mapPinLabelFitDensity
	return math.Min(width, float64(canvasW)), math.Min(height, float64(canvasH))
}

// mapPinLabelCandidates prefers a name above the grounded pin, followed by
// below/right/left and outward rings when the preferred position collides.
func mapPinLabelCandidates(pin SemanticMapPin, x, y, width, height float64) [][2]float64 {
	offsetX, offsetY := 0.0, 0.0
	if len(pin.LabelOffsetPX) == 2 {
		offsetX, offsetY = pin.LabelOffsetPX[0], pin.LabelOffsetPX[1]
	}
	candidates := [][2]float64{
		{x - width/2, y - pin.RadiusPX - mapPinLabelGapPX - height},
		{x - width/2 + offsetX, y + pin.RadiusPX + mapPinLabelGapPX + offsetY},
		{x + pin.RadiusPX + mapPinLabelGapPX, y - height/2},
		{x - pin.RadiusPX - mapPinLabelGapPX - width, y - height/2},
	}
	for ring := 1; ring <= 8; ring++ {
		distance := float64(ring) * (height + mapLabelCollisionGap)
		for _, direction := range [][2]float64{{0, -1}, {1, 0}, {0, 1}, {-1, 0}, {1, -1}, {1, 1}, {-1, 1}, {-1, -1}} {
			candidates = append(candidates, [2]float64{
				x - width/2 + direction[0]*distance,
				y - height/2 + direction[1]*distance,
			})
		}
	}
	return candidates
}

func mapLabelRectsOverlap(a, b [4]float64) bool {
	return a[0] < b[2]+mapLabelCollisionGap && a[2]+mapLabelCollisionGap > b[0] &&
		a[1] < b[3]+mapLabelCollisionGap && a[3]+mapLabelCollisionGap > b[1]
}

// Map lowering turns a resolved map into the
// renderable layers Chronon executes (basemap plate, grounded pins, pin labels,
// attribution). Contract admission and geographic resolution happen upstream.
// compileResolvedMapLayers lowers a resolved map to renderable layers: one
// static plate or ordered LODs, grounded pins and labels, and visible credit.
func compileResolvedMapLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry, resolved *resolvedMap) ([]Layer, error) {
	duration := ri.End - ri.Start
	layers := make([]Layer, 0, 1+2*len(resolved.Pins)+1)
	mapAnimation := resolved.Motion
	var err error
	var basemap Layer
	if resolved.Camera == nil {
		basemap = Layer{
			ID: mapBasemapLayerID(ri.Item.ID), Type: "image",
			Asset:    registry.Path(resolved.StaticAssetID),
			BoxWidth: resolved.RasterWidth, BoxHeight: resolved.RasterHeight, MapRasterWidth: resolved.RasterWidth, MapRasterHeight: resolved.RasterHeight,
			Size: []float64{float64(resolved.RasterWidth), float64(resolved.RasterHeight)}, Fit: FitStretch,
			StartFrame: ri.Start, DurationFrames: duration,
			Scale: []float64{float64(src.Width) / float64(resolved.RasterWidth), float64(src.Height) / float64(resolved.RasterHeight)},
		}
	} else {
		// Keep one certified base plate on the continuous Web-Mercator plane
		// for the whole move. Crossfading independent raster planes produces
		// visible rectangles on the native Vulkan path; a single opaque plate
		// lets the camera animate without LOD switching or flicker.
		move := resolved.Camera
		originX, originY := geo.LatLonToGlobalPixel(move.From.Latitude, move.From.Longitude, int(move.StartZoom))
		worldSize := geo.MercatorTileSize * math.Pow(2, move.StartZoom)
		lod := resolved.LODs[0]
		centerX, centerY := geo.LatLonToGlobalPixel(lod.Center.Latitude, lod.Center.Longitude, int(move.StartZoom))
		scale := math.Pow(2, move.StartZoom-float64(lod.Zoom))
		basemap = Layer{
			ID: mapBasemapLayerID(ri.Item.ID), Type: "image",
			Asset:    registry.Path(lod.AssetID),
			BoxWidth: lod.Width, BoxHeight: lod.Height,
			MapRasterWidth: lod.Width, MapRasterHeight: lod.Height,
			Size: []float64{float64(lod.Width), float64(lod.Height)}, Fit: FitStretch,
			Position: []float64{
				wrapMapPlaneDelta(centerX-originX, worldSize), -(centerY - originY), 0,
			},
			Scale: []float64{scale, scale}, Enable3D: true,
			StartFrame: ri.Start, DurationFrames: duration,
		}
	}
	// Both static plates and camera-moved plates receive their pre-resolved
	// map_view treatment, with map and pin layers sharing the same recipe.
	applyMotionRouting(&basemap, mapAnimation)
	layers = append(layers, basemap)
	for _, route := range resolved.Routes {
		routeLayers, err := compileMapRoutes(ri.Item.ID, []SemanticMapRoute{route.Contract}, route.Origin, route.Zoom,
			src.Width, src.Height, ri.Start, duration)
		if err != nil {
			return nil, fmt.Errorf("overlay: map item %q route %q: %w", ri.Item.ID, route.Contract.ID, err)
		}
		layers = append(layers, routeLayers...)
	}
	// Every grounded point shares the basemap lifetime: the flight is one shot,
	// not a sequence of reveals. Staggered entrances and exclusive windows
	// were tried (delayed reveal, then one marker at a time) and reverted:
	// markers that pop in late or leave mid-flight read as a glitch on the
	// certified look, and the golden canary pins the full-lifetime plate
	// (TestGoldenMapFlyoverCanary). Re-time the pins only by re-baselining
	// that golden deliberately, never by drifting the lowering.
	for _, resolvedPin := range resolved.Pins {
		pin := resolvedPin.Contract
		marker, err := mapPinLayer(ri, src, pin, resolvedPin.RasterX, resolvedPin.RasterY, duration)
		if err != nil {
			return nil, err
		}
		label, err := resolveMapPinLabel(ri, src, resolvedPin, duration)
		if err != nil {
			return nil, err
		}
		timing, err := resolvePinTiming(ri.Params, pin.ID, int64(src.FPSNum), int64(src.FPSDen), duration)
		if err != nil {
			return nil, fmt.Errorf("overlay: map item %q pin %q: %w", ri.Item.ID, pin.ID, err)
		}
		applyPinTiming(&marker, &label, timing)
		pinAnimation := resolvedPin.Motion
		if pinAnimation == nil {
			pinAnimation = mapAnimation
		}
		if pinAnimation != nil {
			applyMotionRouting(&marker, mapPinMotion(pinAnimation, marker.Position))
			applyMotionRouting(&label, mapPinMotion(pinAnimation, label.Position))
		}
		if resolved.Camera != nil {
			worldX, worldY := mapWorldPoint(pin.Latitude, pin.Longitude, resolved.Zoom, resolved.Camera.From)
			marker.Enable3D = true
			marker.Position = []float64{worldX + float64(src.Width)/2, worldY + float64(src.Height)/2, 0}
			label.Enable3D = true
			labelRasterX, labelRasterY := resolvedPin.RasterX, resolvedPin.RasterY
			labelCenterX := resolvedPin.LabelTopLeft[0] + resolvedPin.LabelWidth/2
			labelCenterY := resolvedPin.LabelTopLeft[1] + resolvedPin.LabelHeight/2
			label.Position = []float64{
				worldX + float64(src.Width)/2 + labelCenterX - labelRasterX,
				worldY + float64(src.Height)/2 - (labelCenterY - labelRasterY), 0,
			}
		}
		layers = append(layers, marker, label)
	}
	fxLayers, err := compileMapFxLayers(ri, src, resolved, duration)
	if err != nil {
		return nil, err
	}
	layers = append(layers, fxLayers...)
	credit, err := resolveMapAttribution(ri, src, resolved.Attribution, duration)
	if err != nil {
		return nil, err
	}
	// Attribution is HUD, not map-plane content: it stays screen-space on both
	// static plates and camera moves so the credit never drifts with the flight.
	credit.ScreenSpace = true
	layers = append(layers, credit)
	return layers, nil
}

func resolveMapPinLabel(ri resolvedItem, src *semanticPlan, resolvedPin resolvedMapPin, duration int64) (Layer, error) {
	pin := resolvedPin.Contract
	labelStyle := resolveGeographicTagStyle(pin.LabelStyle)
	topLeft := resolvedPin.LabelTopLeft
	width, height := resolvedPin.LabelWidth, resolvedPin.LabelHeight
	scale := mapTypographyScale(src.Width, src.Height)
	font := OfficialFontPathForLanguage(src.Language)
	fontSize, minFontSize, maxFontSize, fill := mapPinLabelFontPX*scale, mapPinLabelMinFontPX*scale, mapPinLabelFontPX*scale, pin.Color
	if labelStyle != nil {
		style := labelStyle
		if style.FontFamily != "" {
			resolvedFont, ok := runtimeFontPath(style.FontFamily)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: map item %q label_style font_family %q is not a runtime font family", ri.Item.ID, style.FontFamily)
			}
			font = resolvedFont
		}
		if style.FontSizePX != nil {
			fontSize, minFontSize, maxFontSize = *style.FontSizePX*scale, *style.FontSizePX*scale, *style.FontSizePX*scale
		}
		if style.Fill != "" {
			fill = style.Fill
		}
	}
	styleParams := map[string]any{
		"position_x": topLeft[0] + width/2,
		"position_y": topLeft[1] + height/2,
	}
	// Map labels and attribution are text too: start from the same effective
	// run/item typography as every other overlay, then let the pin's explicit
	// label_style override its own font/effects below. Geometry remains owned by
	// this map compiler and cannot be displaced by run-level position params.
	styleParams = mergeSemanticParams(ri.Params, styleParams)
	if labelStyle != nil {
		if labelStyle.FontFamily != "" {
			styleParams["font_family"] = labelStyle.FontFamily
		}
		if labelStyle.FontSizePX != nil {
			styleParams["font_size_px"] = *labelStyle.FontSizePX * scale
		}
	}
	captionStyle := &LayerStyle{Font: font}
	roleOverrides := &LayerStyle{}
	if labelStyle != nil {
		if labelStyle.FontSizePX != nil {
			styleParams["font_size_px"] = fontSize
		}
		roleOverrides.Stroke = cloneLayerStyleStroke(labelStyle.Stroke)
		roleOverrides.Shadow = cloneLayerStyleShadow(labelStyle.Shadow)
		roleOverrides.Glow = cloneLayerStyleGlow(labelStyle.Glow)
		roleOverrides.Background = cloneLayerBackground(labelStyle.Background)
	}
	// A place name usually sits ON the accent territory, which is a saturated
	// fill the ink was never chosen against; a raster basemap already provides
	// its own contrast. The halo is worker-owned drawing policy, like the pin
	// ring: a light keyline around the contract ink keeps the label readable on
	// the vector plate without inventing a second fill. An explicit stroke
	// (contract label_style or runtime stroke_size) still wins.
	if isModernMap1(ri.Item) {
		if roleOverrides.Stroke == nil {
			if _, chosen := ri.Params["stroke_size"]; !chosen {
				roleOverrides.Stroke = &LayerStroke{Color: mapPinLabelHaloColor, Width: fontSize * mapPinLabelHaloRatio}
			}
		}
		// The satellite-plate shadow is too heavy on paper: a tight, soft drop
		// keeps the ink off the territory without smudging every glyph.
		if roleOverrides.Shadow == nil && !hasRuntimeShadowKey(ri.Params) {
			roleOverrides.Shadow = &LayerShadow{
				Color: mapPinLabelShadowColor, Opacity: mapPinLabelShadowOpacity,
				Blur: mapPinLabelShadowBlur, Offset: []float64{0, mapPinLabelShadowOffsetY},
			}
		}
	}
	spec := resolvedTextSpec{
		Text: pin.Label, Size: []float64{width, height}, OmitBox: true,
		Position:    canvasBoxPosition("text", topLeft[0], topLeft[1], width, height, src.Width, src.Height),
		CanvasWidth: src.Width, CanvasHeight: src.Height,
		Style: captionStyle, RoleOverrides: roleOverrides, StyleParams: styleParams, MotionTarget: TargetText, StylePolicy: textRoleMapLabel,
		FontSize: fontSize, MinFontSize: minFontSize, MaxFontSize: maxFontSize, OverrideFill: fill,
	}
	layer, err := compileResolvedText(mapPinLabelLayerID(ri.Item.ID, pin.ID), ri.Start, ri.Start+duration, spec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: map item %q pin %q label: %w", ri.Item.ID, pin.ID, err)
	}
	return layer, nil
}

func cloneLayerBackground(source *semanticMapTextPlate) *LayerBackground {
	if source == nil {
		return nil
	}
	opacity := source.Opacity
	return &LayerBackground{Color: source.Color, Opacity: &opacity, Radius: source.Radius, Padding: append([]float64(nil), source.Padding...)}
}

func resolveMapAttribution(ri resolvedItem, src *semanticPlan, attribution string, duration int64) (Layer, error) {
	width, height := math.Min(mapAttributionWidth, float64(src.Width)), math.Min(mapAttributionHeight, float64(src.Height))
	boxX := clampMapBox(mapAttributionMargin, float64(src.Width)-width)
	boxY := clampMapBox(float64(src.Height)-mapAttributionMargin-height, float64(src.Height)-height)
	scale := mapTypographyScale(src.Width, src.Height)
	spec := resolvedTextSpec{
		Text: attribution, Size: []float64{width, height}, OmitBox: true,
		Position:    canvasBoxPosition("text", boxX, boxY, width, height, src.Width, src.Height),
		CanvasWidth: src.Width, CanvasHeight: src.Height,
		Style:       &LayerStyle{Font: OfficialFontPathForLanguage(src.Language)},
		StyleParams: ri.Params, MotionTarget: TargetText, StylePolicy: textRoleMapAttribution,
		FontSize: mapAttributionFontPX * scale, OverrideFill: "#F8F5EA",
	}
	// Role base styles supply fit and shadow policy; retain the contract's
	// authored range, including its smaller shrink floor.
	spec.MinFontSize = mapAttributionMinFont * scale
	spec.MaxFontSize = mapAttributionFontPX * scale
	layer, err := compileResolvedText(mapAttributionLayerID(ri.Item.ID), ri.Start, ri.Start+duration, spec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: map item %q attribution: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// mapPinMotion applies the basemap's image recipe to each projected pin and
// label as a group: opacity stays in sync, glyph size follows the zoom, and
// each pin's centre follows the raster's scale about the canvas centre.
func mapPinMotion(source *LayerAnimation, position []float64) *LayerAnimation {
	if source == nil {
		return nil
	}
	result := &LayerAnimation{Tracks: make([]AnimationTrack, 0, len(source.Tracks)+2)}
	var scaleKeys []AnimationKeyframe
	var scaleEasing string
	for _, track := range source.Tracks {
		cloned := AnimationTrack{Property: track.Property, Easing: track.Easing, Keyframes: append([]AnimationKeyframe(nil), track.Keyframes...)}
		result.Tracks = append(result.Tracks, cloned)
		if track.Property == "scale" {
			scaleKeys = track.Keyframes
			scaleEasing = track.Easing
		}
	}
	if len(scaleKeys) == 0 || len(position) < 2 {
		return result
	}
	for axis, property := range []string{"position_x", "position_y"} {
		keys := make([]AnimationKeyframe, len(scaleKeys))
		for index, key := range scaleKeys {
			scale, ok := numericValue(key.Value)
			if !ok {
				return result
			}
			keys[index] = AnimationKeyframe{Frame: key.Frame, Value: position[axis] * scale}
		}
		result.Tracks = append(result.Tracks, AnimationTrack{Property: property, Easing: scaleEasing, Keyframes: keys})
	}
	return result
}

func clampMapBox(value, limit float64) float64 {
	return clampx.Float(value, limit)
}

func wrapMapPlaneDelta(value, worldSize float64) float64 {
	if value > worldSize/2 {
		value -= worldSize
	}
	if value < -worldSize/2 {
		value += worldSize
	}
	return value
}

func mapWorldPoint(latitude, longitude float64, zoom int, origin SemanticMapPoint) (float64, float64) {
	x, y := geo.LatLonToGlobalPixel(latitude, longitude, zoom)
	originX, originY := geo.LatLonToGlobalPixel(origin.Latitude, origin.Longitude, zoom)
	return wrapMapPlaneDelta(x-originX, geo.MercatorTileSize*math.Pow(2, float64(zoom))), -(y - originY)
}

// mapPinLayer draws one grounded place as a native ellipse shape centred on
// its projected raster pixel. The plan's shape primitive is supported on the
// native Vulkan path (and is what the certified map canary pins), so pins no
// longer borrow the text glyph "O".
//
// The dot is a light core inside the contract colour, not the reverse: a dark
// fill with a white ring reads as a hole punched through a filled territory,
// while paper-white inside a coloured ring reads as a marker both on the
// accent land and on the paper sea.
func mapPinLayer(ri resolvedItem, src *semanticPlan, pin SemanticMapPin, x, y float64, duration int64) (Layer, error) {
	size := 2 * pin.RadiusPX
	// The ring honors the contract color (validated #RRGGBB upstream); the core
	// is worker-owned so a marker stays a marker instead of a dot of the same
	// ink as the land it points at.
	if _, err := parseHexColor(pin.Color); err != nil {
		return Layer{}, fmt.Errorf("overlay: map item %q pin %q color: %w", ri.Item.ID, pin.ID, err)
	}
	core, err := parseHexColor(mapPinCoreColor)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: map pin core color: %w", err)
	}
	ring := pin.Color
	if mapPinColorIsLight(pin.Color) {
		// A near-white contract colour would vanish into the light core.
		ring = mapPinRingFallbackColor
	}
	width := math.Max(mapPinStrokeWidthPX, pin.RadiusPX*mapPinStrokeRadiusRatio)
	return Layer{
		ID: mapPinLayerID(ri.Item.ID, pin.ID), Type: "shape",
		Size:       []float64{size, size},
		Position:   canvasBoxPosition("shape", x-pin.RadiusPX, y-pin.RadiusPX, size, size, src.Width, src.Height),
		StartFrame: ri.Start, DurationFrames: duration,
		Shape: &LayerShape{Type: "ellipse", Fill: core,
			Stroke: &LayerStroke{Color: ring, Width: width}},
	}, nil
}

// mapPinColorIsLight reports whether a contract colour is too close to the pin
// core to read as its ring.
func mapPinColorIsLight(hex string) bool {
	rgb, err := parseHexColor(hex)
	if err != nil || len(rgb) < 3 {
		return false
	}
	luminance := 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
	return luminance > 0.82
}

func mapBasemapLayerID(itemID string) string         { return itemID + ":map_basemap" }
func mapPinLayerID(itemID, pinID string) string      { return itemID + ":map_pin:" + pinID }
func mapPinLabelLayerID(itemID, pinID string) string { return itemID + ":map_pin_label:" + pinID }
func mapAttributionLayerID(itemID string) string     { return itemID + ":map_attribution" }
