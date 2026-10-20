// semantic_image_effects.go owns the runtime effect decoration of image
// items: the item's `params.effects` entry is the data-driven effect stack a
// producer requests at runtime (job payload, no recompile), lowered onto every
// base image layer the item produces through the shared layer-effect resolver.
// Recipe components (premium plates, captions, glow plates) keep their own
// authored effects untouched: the runtime stack only decorates the photographic
// surfaces, never the recipe's structural layers.
package overlay

import (
	"fmt"
	"strings"
)

// compileImageItem lowers one semantic image item and then decorates its base
// image layers with the item's runtime effects. The core lowering is
// compileImageItemCore; this wrapper is the single place the runtime effect
// stack is applied so every image path (standalone, composite, group, premium)
// shares it.
func compileImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	layers, err := compileImageItemCore(ri, src, registry)
	if err != nil {
		return nil, err
	}
	return applyImageItemEffects(ri, layers)
}

// applyImageItemEffects lowers item-level params.effects onto each base image
// layer of the item. Base image layers are the layers the shared single-layer
// path builds (standalone image, every image_layers child and every group
// member); their ids all end in ":image", which keeps recipe component layers
// out of the decoration. Authored effects on those layers stay first in the
// stack; the runtime stack is appended after them. Every decorated layer gets
// its own parameter copy so one layer can never mutate a sibling's stack.
func applyImageItemEffects(ri resolvedItem, layers []Layer) ([]Layer, error) {
	raw, exists := ri.Params["effects"]
	if !exists || raw == nil {
		return layers, nil
	}
	effects, err := resolveLayerEffects(raw, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	if len(effects) == 0 {
		return layers, nil
	}
	decorated := false
	for index := range layers {
		layer := &layers[index]
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") {
			continue
		}
		for _, effect := range effects {
			params := make(map[string]any, len(effect.Params))
			for key, value := range effect.Params {
				params[key] = value
			}
			layer.Effects = append(layer.Effects, LayerEffect{Type: effect.Type, Params: params})
		}
		decorated = true
	}
	if !decorated {
		return nil, fmt.Errorf("overlay: item %q requests runtime image effects but lowered no base image layer to decorate", ri.Item.ID)
	}
	return layers, nil
}
