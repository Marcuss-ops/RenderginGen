package overlay

import (
	"fmt"
	"math"
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

func isChartBarItem(item resolvedItem) bool {
	if item.Kind != KindShape || item.Params == nil {
		return false
	}
	marked, ok := item.Params["chart_bar"].(bool)
	return ok && marked
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

// optionalShapeNumber reads the first present key and validates one numeric
// shape parameter. max=+Inf means "finite only" (and min=0 adds the usual
// non-negative wording); every bounded field reports the same interval format.
func optionalShapeNumber(params map[string]any, itemID, name string, min, max float64, keys ...string) (float64, bool, error) {
	raw, ok := firstParam(params, keys...)
	if !ok {
		return 0, false, nil
	}
	value, err := toFloat(raw)
	if err != nil || !finite(value) || value < min || value > max {
		switch {
		case math.IsInf(max, 1) && min == 0:
			return 0, true, fmt.Errorf("overlay: item %q shape.%s must be non-negative and finite", itemID, name)
		case math.IsInf(max, 1):
			return 0, true, fmt.Errorf("overlay: item %q shape.%s must be finite", itemID, name)
		default:
			return 0, true, fmt.Errorf("overlay: item %q shape.%s %v must be in [%g, %g]", itemID, name, raw, min, max)
		}
	}
	return value, true, nil
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

	width, height, pos, err := resolveShapeCanvas(ri, src)
	if err != nil {
		return Layer{}, err
	}

	shape := &LayerShape{Type: shapeType}

	if err := applyShapeProceduralParams(shape, ri, shapeType, width, height); err != nil {
		return Layer{}, err
	}

	if err := applyWorldMapParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}
	if err := applyArrowParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}
	if err := applyChartParams(shape, ri, shapeType); err != nil {
		return Layer{}, err
	}

	if pathValue, exists := ri.Params["path"]; exists {
		path, err := parsePath(pathValue, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Path = path
	}

	effects, err := applyShapePaint(shape, ri, shapeType)
	if err != nil {
		return Layer{}, err
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
	if op, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "opacity", 0, 1, "opacity"); err != nil {
		return Layer{}, err
	} else if ok {
		layer.Opacity = &op
	}
	return layer, nil
}
