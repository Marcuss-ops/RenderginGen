package overlay

// shape_resolve_stages.go splits resolveShape into three independent stages:
// canvas geometry, procedural parameters and paint. Each stage validates and
// returns only its own contract, so a geometry bug cannot hide inside a
// 300-line lowering and a paint change cannot move the shape's box.
import (
	"fmt"
	"math"
)

// resolveShapeCanvas resolves the shape's box and top-left position. An
// omitted position centres a sub-canvas shape exactly once, here.
func resolveShapeCanvas(ri resolvedItem, src *semanticPlan) (float64, float64, []float64, error) {
	width := float64(src.Width)
	height := float64(src.Height)
	if raw, exists := ri.Params["width"]; exists {
		w, err := toFloat(raw)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("overlay: item %q requires positive finite width", ri.Item.ID)
		}
		if err := validateDimension(w, "width", ri.Item.ID); err != nil {
			return 0, 0, nil, err
		}
		width = w
	}
	if raw, exists := ri.Params["height"]; exists {
		h, err := toFloat(raw)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("overlay: item %q requires positive finite height", ri.Item.ID)
		}
		if err := validateDimension(h, "height", ri.Item.ID); err != nil {
			return 0, 0, nil, err
		}
		height = h
	}
	if err := validateDimension(width, "width", ri.Item.ID); err != nil {
		return 0, 0, nil, err
	}
	if err := validateDimension(height, "height", ri.Item.ID); err != nil {
		return 0, 0, nil, err
	}

	pos := []float64{0, 0}
	if pRaw, ok := ri.Params["position"]; ok {
		p, err := parse2Floats(pRaw)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers: %w", ri.Item.ID, err)
		}
		if !finite(p[0]) || !finite(p[1]) {
			return 0, 0, nil, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers", ri.Item.ID)
		}
		pos = p
	} else if width < float64(src.Width) || height < float64(src.Height) {
		pos = []float64{(float64(src.Width) - width) / 2, (float64(src.Height) - height) / 2}
	}

	return width, height, pos, nil
}

// applyShapeProceduralParams lowers the shape-family parameters (points,
// rotations, radii, arcs and device frames) onto the layer shape.
func applyShapeProceduralParams(shape *LayerShape, ri resolvedItem, shapeType string, width, height float64) error {
	// 1. Procedural bounds checks
	if ptsRaw, ok := ri.Params["points"]; ok {
		pts, err := toInt(ptsRaw)
		if err != nil || pts < 3 || pts > 64 {
			return fmt.Errorf("overlay: item %q shape.points %v must be in [3, 64]", ri.Item.ID, ptsRaw)
		}
		shape.Points = pts
	}

	if rot, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "rotation_degrees", math.Inf(-1), math.Inf(1), "rotation_degrees", "rotation", "shape_rotation_deg"); err != nil {
		return err
	} else if ok {
		shape.RotationDeg = rot
	}
	if ir, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "inner_radius_ratio", 0.05, 0.95, "inner_radius_ratio"); err != nil {
		return err
	} else if ok {
		shape.InnerRadius = ir
	}
	if sp, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "grid_spacing", 4, 512, "grid_spacing", "spacing"); err != nil {
		return err
	} else if ok {
		shape.GridSpacing = sp
	}
	if lw, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "grid_line_width", 0.25, 32, "grid_line_width", "line_width"); err != nil {
		return err
	} else if ok {
		shape.GridLineWidth = lw
	}
	if dr, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "dot_radius", 0.5, 64, "dot_radius", "dotRadius"); err != nil {
		return err
	} else if ok {
		shape.DotRadius = dr
	}
	if st, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "arc_start_deg", math.Inf(-1), math.Inf(1), "arc_start_deg", "start_deg"); err != nil {
		return err
	} else if ok {
		shape.ArcStartDeg = st
	}
	if sw, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "arc_sweep_deg", 1, 359.9, "arc_sweep_deg", "sweep_deg"); err != nil {
		return err
	} else if ok {
		shape.ArcSweepDeg = sw
	}
	if th, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "arc_thickness", 0, 10000, "arc_thickness", "thickness"); err != nil {
		return err
	} else if ok {
		shape.ArcThickness = th
	}

	maxRad := math.Min(width, height) * 0.5
	if rad, ok, err := optionalShapeNumber(ri.Params, ri.Item.ID, "radius", 0, math.Inf(1), "radius"); err != nil {
		return err
	} else if ok {
		if shapeType == "rounded_rect" && rad > maxRad {
			return fmt.Errorf("overlay: item %q rounded_rect radius %v exceeds half the smaller dimension %v", ri.Item.ID, rad, maxRad)
		}
		shape.Radius = rad
	}

	if crRaw, ok := ri.Params["corner_radius"]; ok {
		cr, err := parse4Floats(crRaw)
		if err != nil {
			return fmt.Errorf("overlay: item %q shape.corner_radius must contain [tl, tr, br, bl]: %w", ri.Item.ID, err)
		}
		for i, c := range cr {
			if c < 0 || c > maxRad || !finite(c) {
				return fmt.Errorf("overlay: item %q corner_radius[%d]=%v outside [0, %v]", ri.Item.ID, i, c, maxRad)
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
			return fmt.Errorf("overlay: item %q device_frame_kind must be phone or browser", ri.Item.ID)
		}
		shape.DeviceFrame = dfKind
		shape.Device = dfKind
		chrome := true
		if rawChrome, exists := ri.Params["chrome"]; exists {
			value, ok := rawChrome.(bool)
			if !ok {
				return fmt.Errorf("overlay: item %q device_frame chrome must be a boolean", ri.Item.ID)
			}
			chrome = value
		}

		shape.Chrome = &chrome
	}

	return nil
}

// applyShapePaint lowers fills, gradients, strokes and effects. It runs after
// geometry so family defaults can depend on the resolved type and size.
func applyShapePaint(shape *LayerShape, ri resolvedItem, shapeType string) ([]LayerEffect, error) {
	// 2. Fills and Gradients
	if fill, hasFill := ri.Params["fill"]; hasFill {
		if color, hasColor := ri.Params["color"]; hasColor && color != fill {
			return nil, fmt.Errorf("overlay: item %q fill and color disagree", ri.Item.ID)
		}
	}
	if gradRaw, ok := firstParam(ri.Params, "fill_gradient", "gradient"); ok {
		grad, err := parseGradient(gradRaw, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		shape.Gradient = grad
		shape.Fill = grad
	} else if fillRaw, ok := firstParam(ri.Params, "fill", "color"); ok {
		col, err := parseRGBA(fillRaw)
		if err != nil {
			return nil, fmt.Errorf("overlay: item %q shape.fill is invalid: %w", ri.Item.ID, err)
		}
		shape.Fill = col
	}

	// 3. Stroke
	if strokeValue, exists := ri.Params["stroke"]; exists {
		strokeRaw, ok := strokeValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q shape.stroke must be an object", ri.Item.ID)
		}
		for key := range strokeRaw {
			if key != "color" && key != "width" {
				return nil, fmt.Errorf("overlay: item %q shape.stroke has unsupported property %q", ri.Item.ID, key)
			}
		}
		colStr := "#FFFFFF"
		if cRaw, exists := strokeRaw["color"]; exists {
			value, ok := cRaw.(string)
			if !ok {
				return nil, fmt.Errorf("overlay: item %q shape.stroke.color must be a hex color string", ri.Item.ID)
			}
			if _, err := parseHexColor(value); err != nil {
				return nil, fmt.Errorf("overlay: item %q shape.stroke.color is invalid: %w", ri.Item.ID, err)
			}
			colStr = value
		}
		w := 1.0
		if wRaw, exists := strokeRaw["width"]; exists {
			wf, err := toFloat(wRaw)
			if err != nil || wf < 0 || !finite(wf) {
				return nil, fmt.Errorf("overlay: item %q shape.stroke.width must be a non-negative finite number", ri.Item.ID)
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
			return nil, err
		}
		effects = effs
	}

	return effects, nil
}
