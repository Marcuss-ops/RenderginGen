package overlay

// shape_params.go — the three structured sub-objects of a shape item
// (world_map, arrow, chart). They are extracted from resolveShape so the main
// lowering reads as a linear sequence of independent parameter groups instead
// of three nested blocks.
import (
	"fmt"
	"strings"
)

// applyWorldMapParams lowers and validates the world_map block. It is a no-op
// unless the item is a world_map shape or declares the block explicitly.
func applyWorldMapParams(shape *LayerShape, ri resolvedItem, shapeType string) error {
	wmRaw, wmExists := ri.Params["world_map"]
	if shapeType == "world_map" || wmExists {
		srcMap, ok := wmRaw.(map[string]any)
		if !ok && wmExists {
			return fmt.Errorf("overlay: item %q world_map must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		proj := "mercator"
		if rawProjection, exists := srcMap["projection"]; exists {
			p, ok := rawProjection.(string)
			if !ok || (p != "mercator" && p != "equirectangular" && p != "equirect") {
				return fmt.Errorf("overlay: item %q world_map projection must be mercator or equirectangular", ri.Item.ID)
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
				return fmt.Errorf("overlay: item %q world_map has unsupported property %q", ri.Item.ID, key)
			}
		}
		center := []float64{0, 0}
		if cRaw, ok := srcMap["center"]; ok {
			c, err := parse2Floats(cRaw)
			if err != nil || c[0] < -180 || c[0] > 180 || c[1] < -90 || c[1] > 90 {
				return fmt.Errorf("overlay: item %q world_map center must be [lon, lat] within [-180, 180] and [-90, 90]", ri.Item.ID)
			}
			center = c
		}
		zoom := 1.0
		if z, ok, err := optionalShapeNumber(srcMap, ri.Item.ID, "zoom", 1, 8, "zoom"); err != nil {
			return err
		} else if ok {
			zoom = z
		}
		var highlight []string
		if rawHighlight, exists := srcMap["highlight"]; exists {
			hlRaw, ok := rawHighlight.([]any)
			if !ok {
				return fmt.Errorf("overlay: item %q world_map highlight must be an array", ri.Item.ID)
			}
			if len(hlRaw) > 64 {
				return fmt.Errorf("overlay: item %q world_map highlight cannot exceed 64 codes", ri.Item.ID)
			}
			for _, code := range hlRaw {
				s, ok := code.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return fmt.Errorf("overlay: item %q world_map highlight entries must be non-empty strings", ri.Item.ID)
				}
				highlight = append(highlight, s)
			}
		}
		hlCol, _ := srcMap["highlight_color"].(string)
		if rawColor, exists := srcMap["highlight_color"]; exists {
			color, ok := rawColor.(string)
			if !ok {
				return fmt.Errorf("overlay: item %q world_map highlight_color must be a string", ri.Item.ID)
			}
			if _, err := parseHexColor(color); err != nil {
				return fmt.Errorf("overlay: item %q world_map highlight_color is invalid", ri.Item.ID)
			}
			hlCol = color
		}
		var route [][]float64
		if rawRoute, exists := srcMap["route"]; exists {
			routeRaw, ok := rawRoute.([]any)
			if !ok {
				return fmt.Errorf("overlay: item %q world_map route must be an array", ri.Item.ID)
			}
			if len(routeRaw) > 256 {
				return fmt.Errorf("overlay: item %q world_map route cannot exceed 256 points", ri.Item.ID)
			}
			for _, ptRaw := range routeRaw {
				pt, err := parse2Floats(ptRaw)
				if err != nil || pt[0] < -180 || pt[0] > 180 || pt[1] < -90 || pt[1] > 90 {
					return fmt.Errorf("overlay: item %q world_map route coordinate out of range", ri.Item.ID)
				}
				route = append(route, pt)
			}
		}
		dots, _ := srcMap["dots"].(bool)
		if rawDots, exists := srcMap["dots"]; exists {
			value, ok := rawDots.(bool)
			if !ok {
				return fmt.Errorf("overlay: item %q world_map dots must be a boolean", ri.Item.ID)
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
	return nil
}

// applyArrowParams lowers and validates the arrow direction and head geometry.
func applyArrowParams(shape *LayerShape, ri resolvedItem, shapeType string) error {
	if shapeType == "arrow" || ri.Params["arrow_direction"] != nil || ri.Params["direction"] != nil {
		if rawDirection, exists := ri.Params["arrow_direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return fmt.Errorf("overlay: item %q arrow_direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		} else if rawDirection, exists := ri.Params["direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return fmt.Errorf("overlay: item %q direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		}
		if shapeType == "arrow" && shape.ArrowDirection != "up" && shape.ArrowDirection != "down" && shape.ArrowDirection != "left" && shape.ArrowDirection != "right" {
			return fmt.Errorf("overlay: item %q arrow_direction must be up, down, left or right", ri.Item.ID)
		}
		for _, item := range []struct {
			keys []string
			dst  *float64
			name string
		}{{[]string{"arrow_head_length", "head_length"}, &shape.ArrowHeadLength, "arrow_head_length"}, {[]string{"arrow_head_width", "head_width"}, &shape.ArrowHeadWidth, "arrow_head_width"}, {[]string{"arrow_shaft_width", "shaft_width"}, &shape.ArrowShaftWidth, "arrow_shaft_width"}} {
			if raw, ok := firstParam(ri.Params, item.keys...); ok {
				value, err := toFloat(raw)
				if err != nil || value <= 0 || !finite(value) {
					return fmt.Errorf("overlay: item %q shape.%s must be positive and finite", ri.Item.ID, item.name)
				}
				*item.dst = value
			}
		}
	}
	return nil
}

// applyChartParams lowers and validates the chart data/colors block.
func applyChartParams(shape *LayerShape, ri resolvedItem, shapeType string) error {
	chartRaw, chartExists := ri.Params["chart"]
	if shapeType == "chart" || chartExists {
		srcMap, ok := chartRaw.(map[string]any)
		if !ok && chartExists {
			return fmt.Errorf("overlay: item %q chart must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		for key := range srcMap {
			switch key {
			case "chart_type", "type", "data", "colors":
			default:
				return fmt.Errorf("overlay: item %q chart has unsupported property %q", ri.Item.ID, key)
			}
		}
		cType := "bar"
		if rawChartType, exists := srcMap["chart_type"]; exists {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return fmt.Errorf("overlay: item %q chart_type is unsupported", ri.Item.ID)
			}
			cType = value
		} else if rawChartType, exists := srcMap["type"]; exists && rawChartType != "chart" {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return fmt.Errorf("overlay: item %q chart type is unsupported", ri.Item.ID)
			}
			cType = value
		}
		var data []float64
		if rawData, exists := srcMap["data"]; exists {
			dRaw, ok := rawData.([]any)
			if !ok {
				return fmt.Errorf("overlay: item %q chart data must be an array", ri.Item.ID)
			}
			for _, value := range dRaw {
				f, err := toFloat(value)
				if err != nil || !finite(f) {
					return fmt.Errorf("overlay: item %q chart data values must be finite numbers", ri.Item.ID)
				}
				data = append(data, f)
			}
		}
		var colors [][]float64
		if rawColors, exists := srcMap["colors"]; exists {
			colRaw, ok := rawColors.([]any)
			if !ok {
				return fmt.Errorf("overlay: item %q chart colors must be an array", ri.Item.ID)
			}
			for _, color := range colRaw {
				rgba, err := parseRGBA(color)
				if err != nil {
					return fmt.Errorf("overlay: item %q chart color is invalid: %w", ri.Item.ID, err)
				}
				colors = append(colors, rgba)
			}
		}
		if len(data) == 0 {
			return fmt.Errorf("overlay: item %q chart requires non-empty data", ri.Item.ID)
		}
		shape.Chart = &LayerChart{
			ChartType: cType,
			Data:      data,
			Colors:    colors,
		}
	}
	return nil
}
