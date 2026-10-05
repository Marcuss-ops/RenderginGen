package overlay

import "math"

func RoundedPremiumRectPath(width, height, radius float64) []LayerPathCommand {
	return roundedRectPath(width, height, radius)
}

func roundedRectPath(width, height, radius float64) []LayerPathCommand {
	x0, x1, y0, y1 := -width/2, width/2, -height/2, height/2
	r := math.Max(0, math.Min(radius, math.Min(width, height)/2))
	k := 0.5522847498 * r
	return []LayerPathCommand{
		{Type: "move_to", Point: []float64{x0 + r, y0}},
		{Type: "line_to", Point: []float64{x1 - r, y0}},
		{Type: "cubic_to", Control1: []float64{x1 - r + k, y0}, Control2: []float64{x1, y0 + r - k}, Point: []float64{x1, y0 + r}},
		{Type: "line_to", Point: []float64{x1, y1 - r}},
		{Type: "cubic_to", Control1: []float64{x1, y1 - r + k}, Control2: []float64{x1 - r + k, y1}, Point: []float64{x1 - r, y1}},
		{Type: "line_to", Point: []float64{x0 + r, y1}},
		{Type: "cubic_to", Control1: []float64{x0 + r - k, y1}, Control2: []float64{x0, y1 - r + k}, Point: []float64{x0, y1 - r}},
		{Type: "line_to", Point: []float64{x0, y0 + r}},
		{Type: "cubic_to", Control1: []float64{x0, y0 + r - k}, Control2: []float64{x0 + r - k, y0}, Point: []float64{x0 + r, y0}},
		{Type: "close"},
	}
}

// brushPath supplies native vector geometry for the authored brush_v1 recipe
// names. Coordinates are centered in the same local box as the target layer.
func brushPath(kind string, width, height float64) []LayerPathCommand {
	x, y := width/2, height/2
	switch kind {
	case "arrow":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .75, y * .45}}, {Type: "quadratic_to", Control1: []float64{0, -y * .6}, Point: []float64{x * .65, -y * .35}}, {Type: "line_to", Point: []float64{x * .25, -y * .4}}, {Type: "move_to", Point: []float64{x * .65, -y * .35}}, {Type: "line_to", Point: []float64{x * .48, -y * .05}}}
	case "scribble":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .25}}, {Type: "line_to", Point: []float64{-x * .45, y * .3}}, {Type: "line_to", Point: []float64{-x * .1, -y * .35}}, {Type: "line_to", Point: []float64{x * .25, y * .35}}, {Type: "line_to", Point: []float64{x * .55, -y * .2}}, {Type: "line_to", Point: []float64{x, y * .25}}}
	case "check":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .8, 0}}, {Type: "line_to", Point: []float64{-x * .2, y * .65}}, {Type: "line_to", Point: []float64{x * .8, -y * .65}}}
	case "ellipse":
		k := .5522847498
		return []LayerPathCommand{{Type: "move_to", Point: []float64{0, -y}}, {Type: "cubic_to", Control1: []float64{x * k, -y}, Control2: []float64{x, -y * k}, Point: []float64{x, 0}}, {Type: "cubic_to", Control1: []float64{x, y * k}, Control2: []float64{x * k, y}, Point: []float64{0, y}}, {Type: "cubic_to", Control1: []float64{-x * k, y}, Control2: []float64{-x, y * k}, Point: []float64{-x, 0}}, {Type: "cubic_to", Control1: []float64{-x, -y * k}, Control2: []float64{-x * k, -y}, Point: []float64{0, -y}}}
	case "corner_marks":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .55}}, {Type: "line_to", Point: []float64{-x, -y}}, {Type: "line_to", Point: []float64{-x * .55, -y}}, {Type: "move_to", Point: []float64{x * .55, -y}}, {Type: "line_to", Point: []float64{x, -y}}, {Type: "line_to", Point: []float64{x, -y * .55}}, {Type: "move_to", Point: []float64{x, y * .55}}, {Type: "line_to", Point: []float64{x, y}}, {Type: "line_to", Point: []float64{x * .55, y}}, {Type: "move_to", Point: []float64{-x * .55, y}}, {Type: "line_to", Point: []float64{-x, y}}, {Type: "line_to", Point: []float64{-x, y * .55}}}
	case "cross":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .8, -y * .8}}, {Type: "line_to", Point: []float64{x * .8, y * .8}}, {Type: "move_to", Point: []float64{x * .8, -y * .8}}, {Type: "line_to", Point: []float64{-x * .8, y * .8}}}
	case "double_line":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .2}}, {Type: "line_to", Point: []float64{x, -y * .2}}, {Type: "move_to", Point: []float64{-x, y * .25}}, {Type: "line_to", Point: []float64{x, y * .25}}}
	case "wave":
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, 0}}, {Type: "cubic_to", Control1: []float64{-x * .5, -y}, Control2: []float64{-x * .25, y}, Point: []float64{0, 0}}, {Type: "cubic_to", Control1: []float64{x * .25, -y}, Control2: []float64{x * .5, y}, Point: []float64{x, 0}}}
	default:
		return nil
	}
}
