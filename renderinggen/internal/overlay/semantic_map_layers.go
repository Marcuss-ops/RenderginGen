// semantic_map_layers.go owns the lowering half of the map subsystem:
// it turns a VALIDATED map contract into the renderable layers Chronon
// executes (basemap plate, grounded pins, pin labels, attribution). All
// admission rules stay in semantic_map.go; this file only draws.
package overlay

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

// compileMapLayers lowers a validated map item to its renderable layers: one
// static plate or ordered LODs, grounded pins and labels, and visible credit.
func compileMapLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	// resolveSemanticItems owns map-contract validation for every item before
	// lowering. Keep this function focused on turning that validated contract
	// into render layers instead of revalidating the same map contract twice.
	m := ri.Item.Map
	duration := ri.End - ri.Start
	window := geo.CenteredOn(m.Center.Latitude, m.Center.Longitude, m.Zoom, m.Width, m.Height)
	layers := make([]Layer, 0, 1+2*len(m.Pins)+1)
	var mapAnimation *LayerAnimation
	if m.MotionID != "" {
		animation, err := imageMotionAnimation(m.MotionID, nil, duration, 0, ri.Item.ID, "map_view")
		if err != nil {
			return nil, fmt.Errorf("overlay: map item %q motion %q: %w", ri.Item.ID, m.MotionID, err)
		}
		mapAnimation = animation
	}
	var basemap Layer
	if m.CameraMove == nil {
		basemap = Layer{
			ID: mapBasemapLayerID(ri.Item.ID), Type: "image",
			Asset:    registry.Path(ri.Item.Assets[0].ID),
			BoxWidth: m.Width, BoxHeight: m.Height, MapRasterWidth: m.Width, MapRasterHeight: m.Height,
			Size: []float64{float64(m.Width), float64(m.Height)}, Fit: FitStretch,
			StartFrame: ri.Start, DurationFrames: duration,
			Scale: []float64{float64(src.Width) / float64(m.Width), float64(src.Height) / float64(m.Height)},
		}
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
	// Both static plates and camera-moved plates receive their declared
	// map_view treatment. Before this was shared, the camera_move branch skipped
	// map_image_v1 entirely, leaving every newly authored map motion inert on
	// the runtime path that generated maps use most often.
	applyMotionRouting(&basemap, mapAnimation)
	layers = append(layers, basemap)
	if len(m.Routes) > 0 {
		routeLayers, err := compileMapRoutes(ri.Item.ID, m.Routes, m.CameraMove.From, int(m.CameraMove.StartZoom),
			src.Width, src.Height, ri.Start, duration)
		if err != nil {
			return nil, fmt.Errorf("overlay: map item %q routes: %w", ri.Item.ID, err)
		}
		layers = append(layers, routeLayers...)
	}
	font := OfficialFontPathForLanguage(src.Language)
	labelPlacements, err := resolveMapLabelPlacements(m.Pins, window, src.Width, src.Height)
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
	candidates := mapPinLabelCandidates(pin, x, y, width, height)
	boxX, boxY := candidates[0][0], candidates[0][1]
	if len(pin.LabelOffsetPX) != 2 {
		boxX = x - width/2
		boxY = y + pin.RadiusPX + mapPinLabelGapPX
	}
	boxX = clampMapBox(boxX, float64(src.Width)-width)
	boxY = clampMapBox(boxY, float64(src.Height)-height)
	labelStyle := mapTextStyle(textRoleMapLabel, font, fontSize, minFontSize, fill)
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
		Style: mapTextStyle(textRoleMapAttribution, font, mapAttributionFontPX*scale, mapAttributionMinFont*scale, "#FFFFFF")}
}

// mapTextStyle asks the shared text system for a map plate's base style. It is a
// thin call rather than a second style builder on purpose: the map decides
// WHERE a plate goes, how big its box is and how plates avoid each other, while
// the text system decides what a map plate looks like (text_role.go). The
// previous shape built the shadow and fit policy inline, which is how a
// subsystem that is supposed to own geometry quietly becomes a second text
// engine.
func mapTextStyle(role textRole, font string, size, minSize float64, fill string) *LayerStyle {
	return mapTextPlateStyle(role, font, size, minSize, fill)
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
