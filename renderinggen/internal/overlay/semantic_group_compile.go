// semantic_group_compile.go owns the entity_group lowering: a multi-entity
// composite whose members are placed in deterministic canvas slots.
//
// A group item carries 2..5 image_layers members; each member keeps its own
// asset, preset, motion, caption and timing, exactly like an ordinary
// composite child. The group decides slot geometry via params.group_layout
// ("auto" by member count, or "duo"/"trio"/"quad"/"penta" which must match
// the count), using the same certified card table as image-group recipes.
// Explicit per-child position_x/position_y (or width+height) always win over
// the slot defaults. Anything else fails closed.
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

// compileEntityGroup validates the group contract, applies slot defaults and
// delegates member lowering to the shared composite path.
func compileEntityGroup(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	n := len(ri.Item.ImageLayers)
	if n < 2 || n > 5 {
		return nil, fmt.Errorf("overlay: entity group item %q requires 2..5 image_layers members, got %d", ri.Item.ID, n)
	}
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
	}
	sx := float64(src.Width) / 1920.0
	sy := float64(src.Height) / 1080.0
	grouped := ri
	grouped.Item.ImageLayers = make([]SemanticImageLayer, n)
	for i, child := range ri.Item.ImageLayers {
		grouped.Item.ImageLayers[i] = child
		slot, ok := groupMemberSlot(n, i)
		if !ok {
			return nil, fmt.Errorf("overlay: entity group item %q has no card slot for member %d of %d", ri.Item.ID, i, n)
		}
		params := child.Params
		if params == nil {
			params = map[string]any{}
		} else {
			cp := make(map[string]any, len(params)+4)
			for k, v := range params {
				cp[k] = v
			}
			params = cp
		}
		if _, ok := params["position_x"]; !ok {
			params["position_x"] = slot.CenterX * sx
			params["position_y"] = slot.CenterY * sy
		}
		// Card size is a slot default too, but an authored width/height pair
		// keeps the member's own box and fit (the canary sets 420x420).
		_, hasWidth := params["width"]
		_, hasHeight := params["height"]
		if !hasWidth && !hasHeight {
			params["width"] = slot.Width * sx
			params["height"] = slot.Height * sy
			if _, ok := params["fit"]; !ok {
				params["fit"] = FitCover
			}
		}
		grouped.Item.ImageLayers[i].Params = params
	}
	return compileCompositeImageLayers(grouped, src, registry)
}
