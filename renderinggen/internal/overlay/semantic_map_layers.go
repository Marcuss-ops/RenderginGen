// semantic_map_layers.go owns map lowering: it turns a resolved map into the
// renderable layers Chronon executes (basemap plate, grounded pins, pin labels,
// attribution). Contract admission and geographic resolution happen upstream.
package overlay

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

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
	for _, resolvedPin := range resolved.Pins {
		pin := resolvedPin.Contract
		// Reveal the point after the camera settles, then the title slightly
		// later, matching the runtime V1 sequence: zoom, point, name.
		pinDelay := duration * 3 / 5
		markerRI := ri
		markerRI.Start += pinDelay
		marker, err := mapPinLayer(markerRI, src, pin, resolvedPin.RasterX, resolvedPin.RasterY, duration-pinDelay)
		if err != nil {
			return nil, err
		}
		labelRI := ri
		labelDelay := duration * 7 / 10
		labelRI.Start += labelDelay
		label, err := resolveMapPinLabel(labelRI, src, resolvedPin, duration-labelDelay)
		if err != nil {
			return nil, err
		}
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
	credit, err := resolveMapAttribution(ri, src, resolved.Attribution, duration)
	if err != nil {
		return nil, err
	}
	if resolved.Camera != nil {
		credit.ScreenSpace = true
	}
	layers = append(layers, credit)
	return layers, nil
}

func resolveMapPinLabel(ri resolvedItem, src *semanticPlan, resolvedPin resolvedMapPin, duration int64) (Layer, error) {
	pin := resolvedPin.Contract
	topLeft := resolvedPin.LabelTopLeft
	width, height := resolvedPin.LabelWidth, resolvedPin.LabelHeight
	scale := mapTypographyScale(src.Width, src.Height)
	font := OfficialFontPathForLanguage(src.Language)
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
	styleParams := map[string]any{
		"position_x": topLeft[0] + width/2,
		"position_y": topLeft[1] + height/2,
	}
	captionStyle := &LayerStyle{Font: font}
	roleOverrides := &LayerStyle{}
	if pin.LabelStyle != nil {
		if pin.LabelStyle.FontSizePX != nil {
			styleParams["font_size_px"] = fontSize
		}
		roleOverrides.Stroke = cloneLayerStroke(pin.LabelStyle.Stroke)
		roleOverrides.Shadow = cloneLayerShadow(pin.LabelStyle.Shadow)
		roleOverrides.Glow = cloneLayerGlow(pin.LabelStyle.Glow)
		roleOverrides.Background = cloneLayerBackground(pin.LabelStyle.Background)
	}
	spec := resolvedTextSpec{
		Text: pin.Label, Size: []float64{width, height}, OmitBox: true,
		Position: canvasBoxPosition("text", topLeft[0], topLeft[1], width, height, src.Width, src.Height),
		Style:    captionStyle, RoleOverrides: roleOverrides, StyleParams: styleParams, MotionTarget: "text", StylePolicy: textRoleMapLabel,
		FontSize: fontSize, MinFontSize: minFontSize, MaxFontSize: maxFontSize, OverrideFill: fill,
	}
	layer, err := compileResolvedText(mapPinLabelLayerID(ri.Item.ID, pin.ID), ri.Start, ri.Start+duration, spec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: map item %q pin %q label: %w", ri.Item.ID, pin.ID, err)
	}
	return layer, nil
}

func cloneLayerStroke(source *semanticMapTextStroke) *LayerStroke {
	if source == nil {
		return nil
	}
	return &LayerStroke{Color: source.Color, Width: source.Width}
}

func cloneLayerShadow(source *semanticMapTextShadow) *LayerShadow {
	if source == nil {
		return nil
	}
	return &LayerShadow{Color: source.Color, Opacity: source.Opacity, Blur: source.Blur, Offset: append([]float64(nil), source.Offset...)}
}

func cloneLayerGlow(source *semanticMapTextGlow) *LayerGlow {
	if source == nil {
		return nil
	}
	return &LayerGlow{Color: source.Color, Radius: source.Radius, Intensity: source.Intensity}
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
		Style:       &LayerStyle{Font: OfficialFontPathForLanguage(src.Language)},
		StyleParams: map[string]any{}, MotionTarget: "text", StylePolicy: textRoleMapAttribution,
		FontSize: mapAttributionFontPX * scale,
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
