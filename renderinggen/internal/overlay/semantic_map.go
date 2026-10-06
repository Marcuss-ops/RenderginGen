// semantic_map.go owns the worker side of the georeferenced map item: the
// fail-closed validation of the declared basemap, and its deterministic
// lowering to layers the renderer actually supports.
//
// A map is not a first-class renderer primitive. It lowers to exactly the
// primitives Chronon already executes:
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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
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
	mapPinStrokeColor   = "#FFFFFF"
	mapPinLabelHeightPX = 52.0

	mapPinLabelGapPX      = 10.0
	mapPinLabelFontPX     = 34.0
	mapPinLabelMinFontPX  = 12.0
	mapAttributionWidth   = 1200.0
	mapAttributionHeight  = 48.0
	mapAttributionFontPX  = 26.0
	mapAttributionMinFont = 10.0
	mapAttributionMargin  = 24.0
	mapTextShadowColor    = "#000000"
	mapTextShadowOpacity  = 0.75
	mapTextShadowBlur     = 6.0
	mapLabelCollisionGap  = 6.0
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
	if !isCenteredMapMotion(m.MotionID) {
		return fmt.Errorf("overlay: map item %q motion %q is not a registered centered image motion", item.ID, m.MotionID)
	}
	if err := validateMapPoint(m.Center.Latitude, m.Center.Longitude); err != nil {
		return fmt.Errorf("overlay: map item %q center: %w", item.ID, err)
	}
	if err := validateMapLODs(item, canvasWidth, canvasHeight); err != nil {
		return err
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
	if _, err := mapLabelPlacements(m.Pins, window, canvasWidth, canvasHeight); err != nil {
		return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
	}
	return nil
}

// isCenteredMapMotion resolves through the shared motion registry and admits
// only catalog-authored image motions whose tracks cannot translate or rotate
// the raster away from the georeferenced pin window.
func isCenteredMapMotion(id string) bool {
	registeredImageMotion := false
	allowedIDs := append(motion.Registry.ImageOverlayMotionIDs(), motion.Registry.MapImageV1MotionIDs()...)
	for _, imageID := range allowedIDs {
		if imageID == id {
			registeredImageMotion = true
			break
		}
	}
	if !registeredImageMotion {
		return false
	}
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return false
	}
	declarative, ok := plugin.(motion.DeclarativePlugin)
	if !ok || len(declarative.Definition.Tracks) == 0 {
		return false
	}
	for _, track := range declarative.Definition.Tracks {
		switch track.Property {
		case "opacity", "scale", "scale_x", "scale_y", "blur":
		default:
			return false
		}
	}
	return true
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
	if math.IsNaN(pin.RadiusPX) || math.IsInf(pin.RadiusPX, 0) || pin.RadiusPX <= 0 || pin.RadiusPX > 128 {
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

// mapLabelPlacements resolves deterministic candidate positions in priority
// order and refuses plans whose visible labels cannot be placed without
// overlapping one another. It never silently hides producer content.
func mapLabelPlacements(pins []SemanticMapPin, window geo.Window, canvasW, canvasH int) (map[string][2]float64, error) {
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

func mapPinLabelDimensions(pin SemanticMapPin, canvasW, canvasH int) (float64, float64) {
	fontSize := mapPinLabelFontPX
	if pin.LabelStyle != nil && pin.LabelStyle.FontSizePX != nil {
		fontSize = *pin.LabelStyle.FontSizePX
	}
	scale := mapTypographyScale(canvasW, canvasH)
	width := math.Max(96*scale, float64(utf8.RuneCountInString(pin.Label))*fontSize*scale*0.68+24*scale)
	height := math.Max(mapPinLabelHeightPX*scale, fontSize*scale*1.6)
	if plate := pin.LabelStyle; plate != nil && plate.Background != nil && len(plate.Background.Padding) == 2 {
		width += 2 * plate.Background.Padding[0] * scale
		height += 2 * plate.Background.Padding[1] * scale
	}
	return math.Min(width, float64(canvasW)), math.Min(height, float64(canvasH))
}

func mapPinLabelCandidates(pin SemanticMapPin, x, y, width, height float64) [][2]float64 {
	offsetX, offsetY := 0.0, 0.0
	if len(pin.LabelOffsetPX) == 2 {
		offsetX, offsetY = pin.LabelOffsetPX[0], pin.LabelOffsetPX[1]
	}
	candidates := [][2]float64{
		{x - width/2 + offsetX, y + pin.RadiusPX + mapPinLabelGapPX + offsetY},
		{x - width/2, y - pin.RadiusPX - mapPinLabelGapPX - height},
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

func validateMapPoint(latitude, longitude float64) error {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
		return fmt.Errorf("latitude %v is outside [-90,90]", latitude)
	}
	if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
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
		if math.IsNaN(value) || math.IsInf(value, 0) {
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

// compileMapLayers lowers a validated map item to its renderable layers: one
// static plate or ordered LODs, grounded pins and labels, and visible credit.
func compileMapLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	if err := validateMapContract(ri.Item, ri.Kind, src.Width, src.Height); err != nil {
		return nil, err
	}
	m := ri.Item.Map
	duration := ri.End - ri.Start
	window := geo.CenteredOn(m.Center.Latitude, m.Center.Longitude, m.Zoom, m.Width, m.Height)
	layers := make([]Layer, 0, 1+2*len(m.Pins)+1)
	var mapAnimation *LayerAnimation
	if m.CameraMove == nil {
		basemap := Layer{
			ID: mapBasemapLayerID(ri.Item.ID), Type: "image",
			Asset:    registry.Path(ri.Item.Assets[0].ID),
			BoxWidth: m.Width, BoxHeight: m.Height, MapRasterWidth: m.Width, MapRasterHeight: m.Height,
			Size: []float64{float64(m.Width), float64(m.Height)}, Fit: FitStretch,
			StartFrame: ri.Start, DurationFrames: duration,
			Scale: []float64{float64(src.Width) / float64(m.Width), float64(src.Height) / float64(m.Height)},
		}
		// The certified centered motion (validateMapContract admits only
		// opacity/scale/blur tracks) still applies to the static plate: without
		// this routing the declared motion silently never reached the basemap.
		if m.MotionID != "" {
			animation, err := imageMotionAnimation(m.MotionID, nil, duration, 0)
			if err != nil {
				return nil, fmt.Errorf("overlay: map item %q motion %q: %w", ri.Item.ID, m.MotionID, err)
			}
			mapAnimation = animation
			applyMotionRouting(&basemap, animation)
		}
		layers = append(layers, basemap)
	} else {
		// Keep one certified base plate on the continuous Web-Mercator plane
		// for the whole move. Crossfading independent raster planes produces
		// visible rectangles on the native Vulkan path; a single opaque plate
		// lets the camera animate without LOD switching or flicker.
		move := m.CameraMove
		originX, originY := geo.LatLonToGlobalPixel(move.From.Latitude, move.From.Longitude, int(move.StartZoom))
		worldSize := geo.MercatorTileSize * math.Pow(2, move.StartZoom)
		lod := m.LODs[0]
		centerX, centerY := geo.LatLonToGlobalPixel(lod.Center.Latitude, lod.Center.Longitude, int(move.StartZoom))
		scale := math.Pow(2, move.StartZoom-float64(lod.Zoom))
		basemap := Layer{
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
		layers = append(layers, basemap)
	}
	font := OfficialFontPathForLanguage(src.Language)
	labelPlacements, err := mapLabelPlacements(m.Pins, window, src.Width, src.Height)
	if err != nil {
		return nil, fmt.Errorf("overlay: map item %q label layout: %w", ri.Item.ID, err)
	}
	for _, pin := range m.Pins {
		x, y := window.ToRaster(pin.Latitude, pin.Longitude)
		marker, err := mapPinLayer(ri, src, pin, x, y, duration)
		if err != nil {
			return nil, err
		}
		label := mapPinLabelLayer(ri, src, pin, font, x, y, duration)
		labelTopLeft := labelPlacements[pin.ID]
		label.Position = canvasBoxPosition("text", labelTopLeft[0], labelTopLeft[1], label.Size[0], label.Size[1], src.Width, src.Height)
		if mapAnimation != nil {
			applyMotionRouting(&marker, mapPinMotion(mapAnimation, marker.Position))
			applyMotionRouting(&label, mapPinMotion(mapAnimation, label.Position))
		}
		if m.CameraMove != nil {
			worldX, worldY := mapWorldPoint(pin.Latitude, pin.Longitude, m.Zoom, m.CameraMove.From)
			marker.Enable3D = true
			marker.Position = []float64{worldX + float64(src.Width)/2, worldY + float64(src.Height)/2, 0}
			label.Enable3D = true
			pinRasterX, pinRasterY := window.ToRaster(pin.Latitude, pin.Longitude)
			labelCenterX := labelTopLeft[0] + label.Size[0]/2
			labelCenterY := labelTopLeft[1] + label.Size[1]/2
			label.Position = []float64{
				worldX + float64(src.Width)/2 + labelCenterX - pinRasterX,
				worldY + float64(src.Height)/2 - (labelCenterY - pinRasterY), 0,
			}
		}
		layers = append(layers, marker, label)
	}
	credit := mapAttributionLayer(ri, src, font, duration)
	if m.CameraMove != nil {
		credit.ScreenSpace = true
	}
	layers = append(layers, credit)
	return layers, nil
}

func mapPinLabelLayer(ri resolvedItem, src *semanticPlan, pin SemanticMapPin, font string, x, y float64, duration int64) Layer {
	scale := mapTypographyScale(src.Width, src.Height)
	fontSize, minFontSize, maxFontSize, fill := mapPinLabelFontPX*scale, mapPinLabelMinFontPX*scale, mapPinLabelFontPX*scale, pin.Color
	if pin.LabelStyle != nil {
		style := pin.LabelStyle
		if style.FontFamily != "" {
			font, _ = runtimeFontPath(style.FontFamily)
		}
		if style.FontSizePX != nil {
			fontSize, minFontSize, maxFontSize = *style.FontSizePX*scale, *style.FontSizePX*scale, *style.FontSizePX*scale
		}
		if style.Fill != "" {
			fill = style.Fill
		}
	}
	width, height := mapPinLabelDimensions(pin, src.Width, src.Height)
	boxX, boxY := mapPinLabelCandidates(pin, x, y, width, height)[0][0], mapPinLabelCandidates(pin, x, y, width, height)[0][1]
	if len(pin.LabelOffsetPX) != 2 {
		boxX = x - width/2
		boxY = y + pin.RadiusPX + mapPinLabelGapPX
	}
	boxX = clampMapBox(boxX, float64(src.Width)-width)
	boxY = clampMapBox(boxY, float64(src.Height)-height)
	labelStyle := mapTextStyle(font, fontSize, minFontSize, fill)
	labelStyle.MaxFontSize = maxFontSize
	if pin.LabelStyle != nil {
		applyMapTextEffects(labelStyle, pin.LabelStyle)
	}
	return Layer{ID: mapPinLabelLayerID(ri.Item.ID, pin.ID), Type: "text", Text: pin.Label,
		Size: []float64{width, height}, Position: canvasBoxPosition("text", boxX, boxY, width, height, src.Width, src.Height),
		StartFrame: ri.Start, DurationFrames: duration,
		Style: labelStyle}
}

func applyMapTextEffects(style *LayerStyle, overrides *SemanticMapTextStyle) {
	if overrides.Stroke != nil {
		style.Stroke = &LayerStroke{Color: overrides.Stroke.Color, Width: overrides.Stroke.Width}
	}
	if overrides.Shadow != nil {
		style.Shadow = &LayerShadow{Color: overrides.Shadow.Color, Opacity: overrides.Shadow.Opacity,
			Blur: overrides.Shadow.Blur, Offset: append([]float64(nil), overrides.Shadow.Offset...)}
	}
	if overrides.Glow != nil {
		style.Glow = &LayerGlow{Color: overrides.Glow.Color, Radius: overrides.Glow.Radius, Intensity: overrides.Glow.Intensity}
	}
	if overrides.Background != nil {
		opacity := overrides.Background.Opacity
		style.Background = &LayerBackground{Color: overrides.Background.Color, Opacity: &opacity,
			Radius: overrides.Background.Radius, Padding: append([]float64(nil), overrides.Background.Padding...)}
	}
}

func mapAttributionLayer(ri resolvedItem, src *semanticPlan, font string, duration int64) Layer {
	width, height := math.Min(mapAttributionWidth, float64(src.Width)), math.Min(mapAttributionHeight, float64(src.Height))
	boxX := clampMapBox(mapAttributionMargin, float64(src.Width)-width)
	boxY := clampMapBox(float64(src.Height)-mapAttributionMargin-height, float64(src.Height)-height)
	scale := mapTypographyScale(src.Width, src.Height)
	return Layer{ID: mapAttributionLayerID(ri.Item.ID), Type: "text", Text: ri.Item.Map.Attribution,
		Size: []float64{width, height}, Position: canvasBoxPosition("text", boxX, boxY, width, height, src.Width, src.Height),
		StartFrame: ri.Start, DurationFrames: duration,
		Style: mapTextStyle(font, mapAttributionFontPX*scale, mapAttributionMinFont*scale, "#FFFFFF")}
}

func mapTextStyle(font string, size, minSize float64, fill string) *LayerStyle {
	return &LayerStyle{Font: font, FontSize: size, MinFontSize: minSize, MaxFontSize: size,
		FitMode: "shrink_only", Fill: fill,
		Shadow: &LayerShadow{Color: mapTextShadowColor, Opacity: mapTextShadowOpacity, Blur: mapTextShadowBlur, Offset: []float64{0, 2}}}
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
			scale, ok := numericParam(key.Value)
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
	if limit < 0 {
		limit = 0
	}
	if value < 0 {
		return 0
	}
	if value > limit {
		return limit
	}
	return value
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
// longer borrow the text glyph "O"; the white ring is the shape's stroke.
func mapPinLayer(ri resolvedItem, src *semanticPlan, pin SemanticMapPin, x, y float64, duration int64) (Layer, error) {
	size := 2 * pin.RadiusPX
	// validateMapPin already admitted only #RRGGBB, so this parse cannot fail;
	// the error branch keeps the lowering fail-closed anyway.
	rgb, err := parseHexColor(pin.Color)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: map item %q pin %q color: %w", ri.Item.ID, pin.ID, err)
	}
	return Layer{
		ID: mapPinLayerID(ri.Item.ID, pin.ID), Type: "shape",
		Size:       []float64{size, size},
		Position:   canvasBoxPosition("shape", x-pin.RadiusPX, y-pin.RadiusPX, size, size, src.Width, src.Height),
		StartFrame: ri.Start, DurationFrames: duration,
		Shape: &LayerShape{Type: "ellipse", Fill: rgb,
			Stroke: &LayerStroke{Color: mapPinStrokeColor, Width: mapPinStrokeWidthPX}},
	}, nil
}

func mapBasemapLayerID(itemID string) string         { return itemID + ":map_basemap" }
func mapPinLayerID(itemID, pinID string) string      { return itemID + ":map_pin:" + pinID }
func mapPinLabelLayerID(itemID, pinID string) string { return itemID + ":map_pin_label:" + pinID }
func mapAttributionLayerID(itemID string) string     { return itemID + ":map_attribution" }
