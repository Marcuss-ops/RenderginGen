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
	if shapeType != "world_map" && !wmExists {
		return nil
	}
	srcMap, ok := wmRaw.(map[string]any)
	if !ok && wmExists {
		return fmt.Errorf("overlay: item %q world_map must be an object", ri.Item.ID)
	}
	if srcMap == nil {
		srcMap = ri.Params
	}
	proj, err := validateWorldMapProjection(srcMap, ri.Item.ID)
	if err != nil {
		return err
	}
	center, err := parseWorldMapCenter(srcMap, ri.Item.ID)
	if err != nil {
		return err
	}
	zoom := 1.0
	if z, ok, err := optionalShapeNumber(srcMap, ri.Item.ID, "zoom", 1, 8, "zoom"); err != nil {
		return err
	} else if ok {
		zoom = z
	}
	highlight, err := parseWorldMapHighlight(srcMap, ri.Item.ID)
	if err != nil {
		return err
	}
	hlCol, err := parseWorldMapHighlightColor(srcMap, ri.Item.ID)
	if err != nil {
		return err
	}
	route, err := parseWorldMapRoute(srcMap, ri.Item.ID)
	if err != nil {
		return err
	}
	dots, err := parseWorldMapDots(srcMap, ri.Item.ID)
	if err != nil {
		return err
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
	return nil
}

// validateWorldMapProjection normalizes the projection and rejects unknown
// world_map properties before any value is parsed.
func validateWorldMapProjection(srcMap map[string]any, itemID string) (string, error) {
	proj := "mercator"
	if rawProjection, exists := srcMap["projection"]; exists {
		p, ok := rawProjection.(string)
		if !ok || (p != "mercator" && p != "equirectangular" && p != "equirect") {
			return "", fmt.Errorf("overlay: item %q world_map projection must be mercator or equirectangular", itemID)
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
			return "", fmt.Errorf("overlay: item %q world_map has unsupported property %q", itemID, key)
		}
	}
	return proj, nil
}

func parseWorldMapCenter(srcMap map[string]any, itemID string) ([]float64, error) {
	center := []float64{0, 0}
	if cRaw, ok := srcMap["center"]; ok {
		c, err := parse2Floats(cRaw)
		if err != nil || c[0] < -180 || c[0] > 180 || c[1] < -90 || c[1] > 90 {
			return nil, fmt.Errorf("overlay: item %q world_map center must be [lon, lat] within [-180, 180] and [-90, 90]", itemID)
		}
		center = c
	}
	return center, nil
}

func parseWorldMapHighlight(srcMap map[string]any, itemID string) ([]string, error) {
	var highlight []string
	rawHighlight, exists := srcMap["highlight"]
	if !exists {
		return nil, nil
	}
	hlRaw, ok := rawHighlight.([]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q world_map highlight must be an array", itemID)
	}
	if len(hlRaw) > 64 {
		return nil, fmt.Errorf("overlay: item %q world_map highlight cannot exceed 64 codes", itemID)
	}
	for _, code := range hlRaw {
		s, ok := code.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("overlay: item %q world_map highlight entries must be non-empty strings", itemID)
		}
		highlight = append(highlight, s)
	}
	return highlight, nil
}

func parseWorldMapHighlightColor(srcMap map[string]any, itemID string) (string, error) {
	hlCol, _ := srcMap["highlight_color"].(string)
	rawColor, exists := srcMap["highlight_color"]
	if !exists {
		return hlCol, nil
	}
	color, ok := rawColor.(string)
	if !ok {
		return "", fmt.Errorf("overlay: item %q world_map highlight_color must be a string", itemID)
	}
	if _, err := parseHexColor(color); err != nil {
		return "", fmt.Errorf("overlay: item %q world_map highlight_color is invalid", itemID)
	}
	return color, nil
}

func parseWorldMapRoute(srcMap map[string]any, itemID string) ([][]float64, error) {
	var route [][]float64
	rawRoute, exists := srcMap["route"]
	if !exists {
		return nil, nil
	}
	routeRaw, ok := rawRoute.([]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q world_map route must be an array", itemID)
	}
	if len(routeRaw) > 256 {
		return nil, fmt.Errorf("overlay: item %q world_map route cannot exceed 256 points", itemID)
	}
	for _, ptRaw := range routeRaw {
		pt, err := parse2Floats(ptRaw)
		if err != nil || pt[0] < -180 || pt[0] > 180 || pt[1] < -90 || pt[1] > 90 {
			return nil, fmt.Errorf("overlay: item %q world_map route coordinate out of range", itemID)
		}
		route = append(route, pt)
	}
	return route, nil
}

func parseWorldMapDots(srcMap map[string]any, itemID string) (bool, error) {
	dots, _ := srcMap["dots"].(bool)
	rawDots, exists := srcMap["dots"]
	if !exists {
		return dots, nil
	}
	value, ok := rawDots.(bool)
	if !ok {
		return false, fmt.Errorf("overlay: item %q world_map dots must be a boolean", itemID)
	}
	return value, nil
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
