// semantic_group_compile.go owns the entity_group lowering: a multi-entity
// composite whose members are placed in deterministic canvas slots.
//
// A group item carries 2..5 image_layers members; each member keeps its own
// asset, preset, motion, caption and timing, exactly like an ordinary
// composite child. The group only decides slot geometry via
// params.group_layout ("auto" by member count, or "duo"/"trio"/"quad"/"penta"
// which must match the count). An explicit per-child position_x/position_y
// always wins over the slot default. Anything else fails closed.
package overlay

import (
	"fmt"
	"strings"
)

// groupLayoutNames is the closed layout vocabulary. "auto" resolves from the
// member count; every other value must match it exactly.
var groupLayoutNames = map[string]int{
	"auto":  0,
	"duo":   2,
	"trio":  3,
	"quad":  4,
	"penta": 5,
}

// groupSlot returns the center-origin pixel offset of slot i of n on a W×H
// canvas. Geometry mirrors the ChrononTemplate multi-entity slot fractions
// (duo side-by-side, trio columns, quad 2×2, penta 3+2 rows) scaled to the
// plan canvas, so worker slots and template slots agree by construction.
func groupSlot(layout string, i, n int, w, h float64) (float64, float64) {
	fw, fh := float64(w), float64(h)
	switch layout {
	case "duo":
		if i == 0 {
			return -fw / 4, 0
		}
		return fw / 4, 0
	case "trio":
		return fw * float64(i-1) / 3, 0
	case "quad":
		x := fw / 4
		if i%2 == 0 {
			x = -x
		}
		y := fh / 4
		if i < 2 {
			y = -y
		}
		return x, y
	default: // penta: three across the top row, two across the bottom row.
		switch i {
		case 0:
			return -fw / 3, -fh / 4
		case 1:
			return 0, -fh / 4
		case 2:
			return fw / 3, -fh / 4
		case 3:
			return -fw / 6, fh / 4
		default:
			return fw / 6, fh / 4
		}
	}
}

// compileEntityGroup validates the group contract, applies slot defaults and
// delegates member lowering to the shared composite path.
func compileEntityGroup(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	n := len(ri.Item.ImageLayers)
	if n < 2 || n > 5 {
		return nil, fmt.Errorf("overlay: entity group item %q requires 2..5 image_layers members, got %d", ri.Item.ID, n)
	}
	layout := "auto"
	if raw, ok := ri.Params["group_layout"]; ok {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q group_layout must be a string", ri.Item.ID)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		want, ok := groupLayoutNames[name]
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q has unsupported group_layout %q", ri.Item.ID, name)
		}
		if want != 0 && want != n {
			return nil, fmt.Errorf("overlay: entity group item %q layout %q needs %d members, got %d", ri.Item.ID, name, want, n)
		}
		if want != 0 {
			layout = name
		}
	}
	if layout == "auto" {
		switch n {
		case 2:
			layout = "duo"
		case 3:
			layout = "trio"
		case 4:
			layout = "quad"
		default:
			layout = "penta"
		}
	}
	grouped := ri
	grouped.Item.ImageLayers = make([]SemanticImageLayer, n)
	for i, child := range ri.Item.ImageLayers {
		grouped.Item.ImageLayers[i] = child
		params := child.Params
		if params == nil {
			params = map[string]any{}
		} else {
			cp := make(map[string]any, len(params)+2)
			for k, v := range params {
				cp[k] = v
			}
			params = cp
		}
		if _, ok := params["position_x"]; !ok {
			x, y := groupSlot(layout, i, n, float64(src.Width), float64(src.Height))
			params["position_x"] = x
			params["position_y"] = y
		}
		grouped.Item.ImageLayers[i].Params = params
	}
	return compileCompositeImageLayers(grouped, src, registry)
}
