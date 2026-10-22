package overlay

import (
	"math"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/clampx"
)

func RoundedPremiumRectPath(width, height, radius float64) []LayerPathCommand {
	return roundedRectPath(width, height, radius)
}

func roundedRectPath(width, height, radius float64) []LayerPathCommand {
	x0, x1, y0, y1 := -width/2, width/2, -height/2, height/2
	r := clampx.Float(radius, math.Min(width, height)/2)
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

// PathKind is the component path vocabulary of the brush_v1 recipe family:
// the authored component.path_kind spellings. Declared once, here next to the
// geometry that gives each spelling its shape, so the recipe lowering and the
// phrase-accent remap cannot spell a path kind the geometry does not know (or
// drop one it does). NOTE: these are PATH kinds of recipe components; the
// shape types of semantic_shape.go are a different vocabulary that happens to
// share the "arrow" spelling.
const (
	PathKindArrow           = "arrow"
	PathKindScribble        = "scribble"
	PathKindCheck           = "check"
	PathKindEllipse         = "ellipse"
	PathKindCornerMarks     = "corner_marks"
	PathKindCross           = "cross"
	PathKindDoubleLine      = "double_line"
	PathKindWave            = "wave"
	PathKindUnderline       = "underline"
	PathKindUnderlineDouble = "underline_double"
	PathKindUnderlineWave   = "underline_wave"
	PathKindRoundedRect     = "rounded_rect"
	PathKindLine            = "line"
)

// componentPath is the single dispatch from a component path kind to its
// vector geometry. It covers the brush family, the rounded rectangle and the
// plain line, so the recipe lowering carries no spelling list of its own: an
// unknown path kind reports false and the caller names the motion and the
// component in the diagnostic.
func componentPath(kind string, width, height, radius float64) ([]LayerPathCommand, bool) {
	switch kind {
	case PathKindRoundedRect:
		return roundedRectPath(width, height, radius), true
	case PathKindLine:
		return []LayerPathCommand{
			{Type: "move_to", Point: []float64{-width / 2, 0}},
			{Type: "line_to", Point: []float64{width / 2, 0}},
		}, true
	}
	path := brushPath(kind, width, height)
	return path, path != nil
}

// brushPath supplies native vector geometry for the authored brush_v1 recipe
// names. Coordinates are centered in the same local box as the target layer.
func brushPath(kind string, width, height float64) []LayerPathCommand {
	x, y := width/2, height/2
	switch kind {
	case PathKindArrow:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .75, y * .45}}, {Type: "quadratic_to", Control1: []float64{0, -y * .6}, Point: []float64{x * .65, -y * .35}}, {Type: "line_to", Point: []float64{x * .25, -y * .4}}, {Type: "move_to", Point: []float64{x * .65, -y * .35}}, {Type: "line_to", Point: []float64{x * .48, -y * .05}}}
	case PathKindScribble:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .25}}, {Type: "line_to", Point: []float64{-x * .45, y * .3}}, {Type: "line_to", Point: []float64{-x * .1, -y * .35}}, {Type: "line_to", Point: []float64{x * .25, y * .35}}, {Type: "line_to", Point: []float64{x * .55, -y * .2}}, {Type: "line_to", Point: []float64{x, y * .25}}}
	case PathKindCheck:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .8, 0}}, {Type: "line_to", Point: []float64{-x * .2, y * .65}}, {Type: "line_to", Point: []float64{x * .8, -y * .65}}}
	case PathKindEllipse:
		k := .5522847498
		return []LayerPathCommand{{Type: "move_to", Point: []float64{0, -y}}, {Type: "cubic_to", Control1: []float64{x * k, -y}, Control2: []float64{x, -y * k}, Point: []float64{x, 0}}, {Type: "cubic_to", Control1: []float64{x, y * k}, Control2: []float64{x * k, y}, Point: []float64{0, y}}, {Type: "cubic_to", Control1: []float64{-x * k, y}, Control2: []float64{-x, y * k}, Point: []float64{-x, 0}}, {Type: "cubic_to", Control1: []float64{-x, -y * k}, Control2: []float64{-x * k, -y}, Point: []float64{0, -y}}}
	case PathKindCornerMarks:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .55}}, {Type: "line_to", Point: []float64{-x, -y}}, {Type: "line_to", Point: []float64{-x * .55, -y}}, {Type: "move_to", Point: []float64{x * .55, -y}}, {Type: "line_to", Point: []float64{x, -y}}, {Type: "line_to", Point: []float64{x, -y * .55}}, {Type: "move_to", Point: []float64{x, y * .55}}, {Type: "line_to", Point: []float64{x, y}}, {Type: "line_to", Point: []float64{x * .55, y}}, {Type: "move_to", Point: []float64{-x * .55, y}}, {Type: "line_to", Point: []float64{-x, y}}, {Type: "line_to", Point: []float64{-x, y * .55}}}
	case PathKindCross:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x * .8, -y * .8}}, {Type: "line_to", Point: []float64{x * .8, y * .8}}, {Type: "move_to", Point: []float64{x * .8, -y * .8}}, {Type: "line_to", Point: []float64{-x * .8, y * .8}}}
	case PathKindDoubleLine:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, -y * .2}}, {Type: "line_to", Point: []float64{x, -y * .2}}, {Type: "move_to", Point: []float64{-x, y * .25}}, {Type: "line_to", Point: []float64{x, y * .25}}}
	case PathKindUnderline:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, height * .43}}, {Type: "line_to", Point: []float64{x, height * .43}}}
	case PathKindUnderlineDouble:
		y1, y2 := height*.32, height*.45
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, y1}}, {Type: "line_to", Point: []float64{x, y1}}, {Type: "move_to", Point: []float64{-x, y2}}, {Type: "line_to", Point: []float64{x, y2}}}
	case PathKindUnderlineWave:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, height * .4}}, {Type: "cubic_to", Control1: []float64{-width * .25, height * .28}, Control2: []float64{-width * .12, height * .54}, Point: []float64{0, height * .4}}, {Type: "cubic_to", Control1: []float64{width * .12, height * .25}, Control2: []float64{width * .25, height * .55}, Point: []float64{x, height * .4}}}
	case PathKindWave:
		return []LayerPathCommand{{Type: "move_to", Point: []float64{-x, 0}}, {Type: "cubic_to", Control1: []float64{-x * .5, -y}, Control2: []float64{-x * .25, y}, Point: []float64{0, 0}}, {Type: "cubic_to", Control1: []float64{x * .25, -y}, Control2: []float64{x * .5, y}, Point: []float64{x, 0}}}
	default:
		return nil
	}
}
