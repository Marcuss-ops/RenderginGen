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

// compileImageItem lowers one semantic image item and then runs the shared
// finalizer. The core lowering is compileImageItemCore; compileItem routes
// entity cards and entity groups through the same finalizer, so an item's
// declared backdrop, glow, runtime effect stack and grid can never be skipped
// because of the path that lowered its base layers.
func compileImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	layers, err := compileImageItemCore(ri, src, registry)
	if err != nil {
		return nil, err
	}
	return finalizeImageLayers(ri, src, registry, layers, true)
}

// finalizeImageLayers is the ONE post-lowering pipeline for image-producing
// items, in execution order:
//
//  1. optional per-member backdrops (owner items only — an entity card already
//     emitted its own single backdrop during lowering);
//  2. the image glow plate;
//  3. the item's runtime params.effects stack;
//  4. the standalone params.grid overlay.
//
// It replaces three copies of the same glow→effects→grid chain inside
// compileItem and the fourth inside compileImageItem, so a new decoration
// stage is added in exactly one place.
func finalizeImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry, layers []Layer, backdrops bool) ([]Layer, error) {
	var err error
	if backdrops {
		// One entry point owns the single/member/shared-backdrop decision.
		layers, err = addImageBackdrops(ri, src, registry, layers)
		if err != nil {
			return nil, err
		}
	}
	layers, err = applyImageGlowToBaseLayers(ri, layers)
	if err != nil {
		return nil, err
	}
	layers, err = applyImageItemEffects(ri, layers)
	if err != nil {
		return nil, err
	}
	return addStandaloneImageGrid(ri, src, layers)
}

// decorateImageLayerWithItemEffects applies the item's runtime effect stack to
// one freshly lowered image layer, leaving effect-free items untouched. Recipe
// members call it for their own params.effects so a per-child stack is honoured
// exactly like a composite child's.
func decorateImageLayerWithItemEffects(ri resolvedItem, layer Layer) (Layer, error) {
	if _, declared := ri.Params["effects"]; !declared {
		return layer, nil
	}
	layers, err := applyImageItemEffects(ri, []Layer{layer})
	if err != nil {
		return Layer{}, err
	}
	return layers[0], nil
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
