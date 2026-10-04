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
