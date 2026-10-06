package overlay

import (
	"fmt"
	"math"
	"strings"
)

var supportedShapeTypes = map[string]struct{}{
	"rect": {}, "rounded_rect": {}, "ellipse": {}, "line": {}, "path": {},
	"star": {}, "polygon": {}, "arrow": {}, "arc": {}, "grid": {},
	"dot_grid": {}, "device_frame": {}, "world_map": {}, "chart": {},
}

func isSupportedShapeType(t string) bool {
	_, ok := supportedShapeTypes[t]
	return ok
}

// EffectRegistry is the closed vocabulary accepted by shape layers. It is
// intentionally private: external mutation would turn validation into a
// bypass around Chronon's effect contract.
var effectRegistry = map[string]struct{}{
	"noise": {}, "fractal_noise": {}, "vignette": {}, "gaussian_blur": {},
	"drop_shadow": {}, "bloom": {}, "glow": {}, "light_rays": {},
	"threshold": {}, "mosaic": {}, "lut_3d": {}, "echo": {}, "keying": {},
	"chromatic_aberration": {}, "halftone": {}, "directional_blur": {},
	"radial_blur": {}, "sharpen": {}, "turbulent_displace": {}, "wave_warp": {},
	"ripple": {}, "displacement_map": {}, "corner_pin": {}, "twirl": {},
	"bulge": {}, "mesh_warp": {}, "catalog": {},
}

func isSupportedEffectType(t string) bool {
	_, ok := effectRegistry[t]
	return ok
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validateRGBA(color []float64) error {
	if len(color) != 4 {
		return fmt.Errorf("expected RGBA[4], got %d components", len(color))
	}
	for index, component := range color {
		if !finite(component) || component < 0 || component > 1 {
			return fmt.Errorf("component %d must be finite and in [0, 1]", index)
		}
	}
	return nil
}

func validatePair(raw any, itemID, name string) ([]float64, error) {
	pair, err := parse2Floats(raw)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q %s must contain two finite numbers", itemID, name)
	}
	return pair, nil
}

func isSupportedPathCommand(command string) bool {
	switch command {
	case "move_to", "line_to", "quadratic_to", "cubic_to", "close", "M", "L", "Q", "C", "Z":
		return true
	default:
		return false
	}
}

func validateDimension(value float64, name, itemID string) error {
	if !finite(value) || value <= 0 {
		return fmt.Errorf("overlay: item %q requires positive finite %s", itemID, name)
	}
	return nil
}

func validateShapeContract(shape *LayerShape, width, height float64, itemID string) error {
	if err := validateRGBAFromShape(shape); err != nil {
		return fmt.Errorf("overlay: item %q shape.fill is invalid: %w", itemID, err)
	}
	if shape.Radius < 0 || !finite(shape.Radius) {
		return fmt.Errorf("overlay: item %q shape.radius must be non-negative and finite", itemID)
	}
	maxRadius := math.Min(width, height) * 0.5
	for _, radius := range shape.CornerRadius {
		if radius < 0 || radius > maxRadius || !finite(radius) {
			return fmt.Errorf("overlay: item %q corner_radius values must be in [0, %v]", itemID, maxRadius)
		}
	}
	if shape.Stroke != nil {
		if shape.Stroke.Width < 0 || !finite(shape.Stroke.Width) {
			return fmt.Errorf("overlay: item %q stroke width must be non-negative and finite", itemID)
		}
		if _, err := parseHexColor(shape.Stroke.Color); err != nil {
			return fmt.Errorf("overlay: item %q stroke color is invalid: %w", itemID, err)
		}
	}
	return nil
}

func validateRGBAFromShape(shape *LayerShape) error {
	fill, ok := shape.Fill.([]float64)
	if !ok || fill == nil {
		return nil
	}
	return validateRGBA(fill)
}

func firstParam(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// resolveShape is the one lowering boundary for shape items: semantic params
// enter here, are validated fail-closed, and leave as the normalized Chronon
// layer representation.
func resolveShape(ri resolvedItem, src *semanticPlan) (Layer, error) {
	if ri.Params == nil {
		return Layer{}, fmt.Errorf("overlay: item %q shape params are required", ri.Item.ID)
	}
	shapeType := stringParam(ri.Params, "shape", stringParam(ri.Params, "type", "rect"))
	if raw, exists := ri.Params["shape"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape must be a string", ri.Item.ID)
		}
	}
	if raw, exists := ri.Params["type"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape.type must be a string", ri.Item.ID)
		}
	}
	if rawShape, hasShape := ri.Params["shape"]; hasShape {
		if rawType, hasType := ri.Params["type"]; hasType && rawType != rawShape {
			return Layer{}, fmt.Errorf("overlay: item %q shape and type disagree", ri.Item.ID)
		}
	}
	if shapeType == "dotgrid" {
		shapeType = "dot_grid"
	} else if shapeType == "worldmap" {
		shapeType = "world_map"
	}
	if !isSupportedShapeType(shapeType) {
		return Layer{}, fmt.Errorf("overlay: item %q has unsupported shape type %q", ri.Item.ID, shapeType)
	}

	width := float64(src.Width)
	height := float64(src.Height)
	if raw, exists := ri.Params["width"]; exists {
		w, err := toFloat(raw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q requires positive finite width", ri.Item.ID)
		}
		if err := validateDimension(w, "width", ri.Item.ID); err != nil {
			return Layer{}, err
		}
		width = w
	}
	if raw, exists := ri.Params["height"]; exists {
		h, err := toFloat(raw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q requires positive finite height", ri.Item.ID)
		}
		if err := validateDimension(h, "height", ri.Item.ID); err != nil {
			return Layer{}, err
		}
		height = h
	}
	if err := validateDimension(width, "width", ri.Item.ID); err != nil {
		return Layer{}, err
	}
	if err := validateDimension(height, "height", ri.Item.ID); err != nil {
		return Layer{}, err
	}

	pos := []float64{0, 0}
	if pRaw, ok := ri.Params["position"]; ok {
		p, err := parse2Floats(pRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers: %w", ri.Item.ID, err)
		}
		if math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsInf(p[0], 0) || math.IsInf(p[1], 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers", ri.Item.ID)
		}
		pos = p
	} else if width < float64(src.Width) || height < float64(src.Height) {
		pos = []float64{(float64(src.Width) - width) / 2, (float64(src.Height) - height) / 2}
	}

	shape := &LayerShape{Type: shapeType}

	// 1. Procedural bounds checks
	if ptsRaw, ok := ri.Params["points"]; ok {
		pts, err := toInt(ptsRaw)
		if err != nil || pts < 3 || pts > 64 {
			return Layer{}, fmt.Errorf("overlay: item %q shape.points %v must be in [3, 64]", ri.Item.ID, ptsRaw)
		}
		shape.Points = pts
	}

	if rotRaw, ok := firstParam(ri.Params, "rotation_degrees", "rotation", "shape_rotation_deg"); ok {
		rot, err := toFloat(rotRaw)
		if err != nil || math.IsNaN(rot) || math.IsInf(rot, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.rotation_degrees must be finite", ri.Item.ID)
		}
		shape.RotationDeg = rot
	}

	if irRaw, ok := ri.Params["inner_radius_ratio"]; ok {
		ir, err := toFloat(irRaw)
		if err != nil || ir < 0.05 || ir > 0.95 || math.IsNaN(ir) || math.IsInf(ir, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.inner_radius_ratio %v must be in [0.05, 0.95]", ri.Item.ID, irRaw)
		}
		shape.InnerRadius = ir
	}
	if spRaw, ok := firstParam(ri.Params, "grid_spacing", "spacing"); ok {
		sp, err := toFloat(spRaw)
		if err != nil || sp < 4.0 || sp > 512.0 || math.IsNaN(sp) || math.IsInf(sp, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.grid_spacing %v must be in [4, 512]", ri.Item.ID, spRaw)
		}
		shape.GridSpacing = sp
	}

	if lwRaw, ok := firstParam(ri.Params, "grid_line_width", "line_width"); ok {
		lw, err := toFloat(lwRaw)
		if err != nil || lw < 0.25 || lw > 32.0 || math.IsNaN(lw) || math.IsInf(lw, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.grid_line_width %v must be in [0.25, 32]", ri.Item.ID, lwRaw)
		}
		shape.GridLineWidth = lw
	}

	if drRaw, ok := firstParam(ri.Params, "dot_radius", "dotRadius"); ok {
		dr, err := toFloat(drRaw)
		if err != nil || dr < 0.5 || dr > 64.0 || math.IsNaN(dr) || math.IsInf(dr, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.dot_radius %v must be in [0.5, 64]", ri.Item.ID, drRaw)
		}
		shape.DotRadius = dr
	}

	if aStartRaw, ok := firstParam(ri.Params, "arc_start_deg", "start_deg"); ok {
		st, err := toFloat(aStartRaw)
		if err != nil || math.IsNaN(st) || math.IsInf(st, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_start_deg must be finite", ri.Item.ID)
		}
		shape.ArcStartDeg = st
	}

	if aSweepRaw, ok := firstParam(ri.Params, "arc_sweep_deg", "sweep_deg"); ok {
		sw, err := toFloat(aSweepRaw)
		if err != nil || sw < 1.0 || sw > 359.9 || math.IsNaN(sw) || math.IsInf(sw, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_sweep_deg %v must be in [1, 359.9]", ri.Item.ID, aSweepRaw)
		}
		shape.ArcSweepDeg = sw
	}

	if aThickRaw, ok := firstParam(ri.Params, "arc_thickness", "thickness"); ok {
		th, err := toFloat(aThickRaw)
		if err != nil || th < 0 || th > 10000.0 || math.IsNaN(th) || math.IsInf(th, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_thickness %v must be in [0, 10000]", ri.Item.ID, aThickRaw)
		}
		shape.ArcThickness = th
	}

	maxRad := math.Min(width, height) * 0.5
	if radRaw, ok := ri.Params["radius"]; ok {
		rad, err := toFloat(radRaw)
		if err != nil || rad < 0 || math.IsNaN(rad) || math.IsInf(rad, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.radius must be non-negative", ri.Item.ID)
		}
		if shapeType == "rounded_rect" && rad > maxRad {
			return Layer{}, fmt.Errorf("overlay: item %q rounded_rect radius %v exceeds half the smaller dimension %v", ri.Item.ID, rad, maxRad)
		}
		shape.Radius = rad
	}

	if crRaw, ok := ri.Params["corner_radius"]; ok {
		cr, err := parse4Floats(crRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.corner_radius must contain [tl, tr, br, bl]: %w", ri.Item.ID, err)
		}
		for i, c := range cr {
			if c < 0 || c > maxRad || math.IsNaN(c) || math.IsInf(c, 0) {
				return Layer{}, fmt.Errorf("overlay: item %q corner_radius[%d]=%v outside [0, %v]", ri.Item.ID, i, c, maxRad)
			}
		}
		shape.CornerRadius = cr
	}

	if shapeType == "device_frame" || ri.Params["device_frame_kind"] != nil || ri.Params["device"] != nil {
		dfKind := stringParam(ri.Params, "device_frame_kind", "")
		if dfKind == "" {
			dfKind = stringParam(ri.Params, "device", "phone")
		}
		if dfKind != "phone" && dfKind != "browser" && dfKind != "window" {
			return Layer{}, fmt.Errorf("overlay: item %q device_frame_kind must be phone or browser", ri.Item.ID)
		}
		shape.DeviceFrame = dfKind
		shape.Device = dfKind
		chrome := true
		if rawChrome, exists := ri.Params["chrome"]; exists {
			value, ok := rawChrome.(bool)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q device_frame chrome must be a boolean", ri.Item.ID)
			}
			chrome = value
		}

		shape.Chrome = &chrome
	}

	wmRaw, wmExists := ri.Params["world_map"]
	if shapeType == "world_map" || wmExists {
		srcMap, ok := wmRaw.(map[string]any)
		if !ok && wmExists {
			return Layer{}, fmt.Errorf("overlay: item %q world_map must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		proj := "mercator"
		if rawProjection, exists := srcMap["projection"]; exists {
			p, ok := rawProjection.(string)
			if !ok || (p != "mercator" && p != "equirectangular" && p != "equirect") {
				return Layer{}, fmt.Errorf("overlay: item %q world_map projection must be mercator or equirectangular", ri.Item.ID)
			}
			if p == "equirect" {
				p = "equirectangular"
			}
			proj = p
		}
		for key := range srcMap {
			switch key {
			case "projection", "center", "zoom", "highlight", "highlight_color", "route", "dots":
			default:
				return Layer{}, fmt.Errorf("overlay: item %q world_map has unsupported property %q", ri.Item.ID, key)
			}
		}
		center := []float64{0, 0}
		if cRaw, ok := srcMap["center"]; ok {
			c, err := parse2Floats(cRaw)
			if err != nil || c[0] < -180 || c[0] > 180 || c[1] < -90 || c[1] > 90 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map center must be [lon, lat] within [-180, 180] and [-90, 90]", ri.Item.ID)
			}
			center = c
		}
		zoom := 1.0
		if zRaw, ok := srcMap["zoom"]; ok {
			z, err := toFloat(zRaw)
			if err != nil || z < 1.0 || z > 8.0 || math.IsNaN(z) {
				return Layer{}, fmt.Errorf("overlay: item %q world_map zoom %v must be in [1, 8]", ri.Item.ID, zRaw)
			}
			zoom = z
		}
		var highlight []string
		if rawHighlight, exists := srcMap["highlight"]; exists {
			hlRaw, ok := rawHighlight.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight must be an array", ri.Item.ID)
			}
			if len(hlRaw) > 64 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight cannot exceed 64 codes", ri.Item.ID)
			}
			for _, code := range hlRaw {
				s, ok := code.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return Layer{}, fmt.Errorf("overlay: item %q world_map highlight entries must be non-empty strings", ri.Item.ID)
				}
				highlight = append(highlight, s)
			}
		}
		hlCol, _ := srcMap["highlight_color"].(string)
		if rawColor, exists := srcMap["highlight_color"]; exists {
			color, ok := rawColor.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight_color must be a string", ri.Item.ID)
			}
			if _, err := parseHexColor(color); err != nil {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight_color is invalid", ri.Item.ID)
			}
			hlCol = color
		}
		var route [][]float64
		if rawRoute, exists := srcMap["route"]; exists {
			routeRaw, ok := rawRoute.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map route must be an array", ri.Item.ID)
			}
			if len(routeRaw) > 256 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map route cannot exceed 256 points", ri.Item.ID)
			}
			for _, ptRaw := range routeRaw {
				pt, err := parse2Floats(ptRaw)
				if err != nil || pt[0] < -180 || pt[0] > 180 || pt[1] < -90 || pt[1] > 90 {
					return Layer{}, fmt.Errorf("overlay: item %q world_map route coordinate out of range", ri.Item.ID)
				}
				route = append(route, pt)
			}
		}
		dots, _ := srcMap["dots"].(bool)
		if rawDots, exists := srcMap["dots"]; exists {
			value, ok := rawDots.(bool)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map dots must be a boolean", ri.Item.ID)
			}
			dots = value
		}
		shape.WorldMap = &LayerWorldMap{
			Projection:     proj,
			Center:         center,
			Zoom:           zoom,
			Highlight:      highlight,
			HighlightColor: hlCol,
			Route:          route,
			Dots:           dots,
		}
	}

	if shapeType == "arrow" || ri.Params["arrow_direction"] != nil || ri.Params["direction"] != nil {
		if rawDirection, exists := ri.Params["arrow_direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q arrow_direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		} else if rawDirection, exists := ri.Params["direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		}
		if shapeType == "arrow" && shape.ArrowDirection != "up" && shape.ArrowDirection != "down" && shape.ArrowDirection != "left" && shape.ArrowDirection != "right" {
			return Layer{}, fmt.Errorf("overlay: item %q arrow_direction must be up, down, left or right", ri.Item.ID)
		}
		for _, item := range []struct {
			keys []string
			dst  *float64
			name string
		}{{[]string{"arrow_head_length", "head_length"}, &shape.ArrowHeadLength, "arrow_head_length"}, {[]string{"arrow_head_width", "head_width"}, &shape.ArrowHeadWidth, "arrow_head_width"}, {[]string{"arrow_shaft_width", "shaft_width"}, &shape.ArrowShaftWidth, "arrow_shaft_width"}} {
			if raw, ok := firstParam(ri.Params, item.keys...); ok {
				value, err := toFloat(raw)
				if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
					return Layer{}, fmt.Errorf("overlay: item %q shape.%s must be positive and finite", ri.Item.ID, item.name)
				}
				*item.dst = value
			}
		}
	}

	chartRaw, chartExists := ri.Params["chart"]
	if shapeType == "chart" || chartExists {
		srcMap, ok := chartRaw.(map[string]any)
		if !ok && chartExists {
			return Layer{}, fmt.Errorf("overlay: item %q chart must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		for key := range srcMap {
			switch key {
			case "chart_type", "type", "data", "colors":
			default:
				return Layer{}, fmt.Errorf("overlay: item %q chart has unsupported property %q", ri.Item.ID, key)
			}
		}
		cType := "bar"
		if rawChartType, exists := srcMap["chart_type"]; exists {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return Layer{}, fmt.Errorf("overlay: item %q chart_type is unsupported", ri.Item.ID)
			}
			cType = value
		} else if rawChartType, exists := srcMap["type"]; exists && rawChartType != "chart" {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return Layer{}, fmt.Errorf("overlay: item %q chart type is unsupported", ri.Item.ID)
			}
			cType = value
		}
		var data []float64
		if rawData, exists := srcMap["data"]; exists {
			dRaw, ok := rawData.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q chart data must be an array", ri.Item.ID)
			}
			for _, value := range dRaw {
				f, err := toFloat(value)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
					return Layer{}, fmt.Errorf("overlay: item %q chart data values must be finite numbers", ri.Item.ID)
				}
				data = append(data, f)
			}
		}
		var colors [][]float64
		if rawColors, exists := srcMap["colors"]; exists {
			colRaw, ok := rawColors.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q chart colors must be an array", ri.Item.ID)
			}
			for _, color := range colRaw {
				rgba, err := parseRGBA(color)
				if err != nil {
					return Layer{}, fmt.Errorf("overlay: item %q chart color is invalid: %w", ri.Item.ID, err)
				}
				colors = append(colors, rgba)
			}
		}
		if len(data) == 0 {
			return Layer{}, fmt.Errorf("overlay: item %q chart requires non-empty data", ri.Item.ID)
		}
		shape.Chart = &LayerChart{
			ChartType: cType,
			Data:      data,
			Colors:    colors,
		}
	}

	if pathValue, exists := ri.Params["path"]; exists {
		path, err := parsePath(pathValue, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Path = path
	}

	// 2. Fills and Gradients
	if fill, hasFill := ri.Params["fill"]; hasFill {
		if color, hasColor := ri.Params["color"]; hasColor && color != fill {
			return Layer{}, fmt.Errorf("overlay: item %q fill and color disagree", ri.Item.ID)
		}
	}
	if gradRaw, ok := firstParam(ri.Params, "fill_gradient", "gradient"); ok {
		grad, err := parseGradient(gradRaw, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Gradient = grad
		shape.Fill = grad
	} else if fillRaw, ok := firstParam(ri.Params, "fill", "color"); ok {
		col, err := parseRGBA(fillRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.fill is invalid: %w", ri.Item.ID, err)
		}
		shape.Fill = col
	}

	// 3. Stroke
	if strokeValue, exists := ri.Params["stroke"]; exists {
		strokeRaw, ok := strokeValue.(map[string]any)
		if !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape.stroke must be an object", ri.Item.ID)
		}
		for key := range strokeRaw {
			if key != "color" && key != "width" {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke has unsupported property %q", ri.Item.ID, key)
			}
		}
		colStr := "#FFFFFF"
		if cRaw, exists := strokeRaw["color"]; exists {
			value, ok := cRaw.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.color must be a hex color string", ri.Item.ID)
			}
			if _, err := parseHexColor(value); err != nil {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.color is invalid: %w", ri.Item.ID, err)
			}
			colStr = value
		}
		w := 1.0
		if wRaw, exists := strokeRaw["width"]; exists {
			wf, err := toFloat(wRaw)
			if err != nil || wf < 0 || math.IsNaN(wf) || math.IsInf(wf, 0) {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.width must be a non-negative finite number", ri.Item.ID)
			}
			w = wf
		}
		shape.Stroke = &LayerStroke{Color: colStr, Width: w}
	} else if shapeType == "grid" && shape.GridLineWidth > 0 {
		shape.Stroke = &LayerStroke{Color: "#FFFFFF", Width: shape.GridLineWidth}
	} else if shapeType == "arc" {
		sw := 2.0
		if shape.ArcThickness > 0 {
			sw = shape.ArcThickness
		}
		shape.Stroke = &LayerStroke{Color: "#FFFFFF", Width: sw}
	}

	if shapeType == "device_frame" && shape.Fill == nil {
		shape.Fill = []float64{0.12, 0.16, 0.23, 1.0}
	}
	if shapeType == "arc" {
		shape.Fill = nil
	}

	// 4. Effects
	var effects []LayerEffect
	if effRaw, ok := ri.Params["effects"]; ok {
		effs, err := resolveLayerEffects(effRaw, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		effects = effs
	}

	layer := Layer{
		ID:             ri.Item.ID,
		Type:           "shape",
		Size:           []float64{width, height},
		Position:       pos,
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
		Shape:          shape,
		Effects:        effects,
	}

	if err := validateShapeContract(shape, width, height, ri.Item.ID); err != nil {
		return Layer{}, err
	}
	if opRaw, ok := ri.Params["opacity"]; ok {
		op, err := toFloat(opRaw)
		if err != nil || op < 0 || op > 1 || math.IsNaN(op) || math.IsInf(op, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.opacity must be finite and in [0, 1]", ri.Item.ID)
		}
		layer.Opacity = &op
	}

	return layer, nil
}
