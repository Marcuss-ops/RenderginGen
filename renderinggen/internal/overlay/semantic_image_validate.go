// semantic_image_validate.go owns the admission half of the image subsystem:
// composition cardinality, layer timing, caption policy and multi-asset
// rules, validated before any lowering happens.
package overlay

import (
	"fmt"
	"strings"
)

// compileImageItem lowers one semantic image item. With no image_layers it uses
// the single-image path (one asset, item-level motion_id/motion_params); with
// image_layers it compiles each child independently (its own asset, time,
// preset, motion_id and motion_params). Multi-image recipes are a separate
// catalog-defined mode that coordinates the image layers as one motion.
// validateSemanticImageLayers uses the kind already resolved for this item so
// composition checks cannot reinterpret its template independently.
func validateSemanticImageLayers(item semanticItem, kind ItemKind) error {
	if len(item.ImageLayers) == 0 {
		if len(item.Assets) > 1 {
			switch behaviorOf(kind) {
			case behaviorImage:
				return fmt.Errorf("overlay: image item %q has %d asset_refs but no image_layers; declare one image_layers entry per image", item.ID, len(item.Assets))
			case behaviorEntity:
				return fmt.Errorf("overlay: entity item %q has %d asset_refs; entity cards support one portrait asset, use separate image items for a composition", item.ID, len(item.Assets))
			}
		}
		return validateImageComposition(item, kind)
	}
	if behaviorOf(kind) != behaviorImage || len(item.ImageLayers) < 2 || len(item.Assets) < 2 {
		return fmt.Errorf("overlay: item %q image_layers require an image item with at least two layers and assets", item.ID)
	}
	maximumImages := maxImageCompositionCount()
	if len(item.ImageLayers) > maximumImages {
		return fmt.Errorf("overlay: item %q has %d image layers; image compositions support at most %d images", item.ID, len(item.ImageLayers), maximumImages)
	}
	assets := make(map[string]struct{}, len(item.Assets))
	for _, ref := range item.Assets {
		assets[ref.ID] = struct{}{}
	}
	layerIDs := make(map[string]struct{}, len(item.ImageLayers))
	parentDuration := item.EndMS - item.StartMS
	for index, layer := range item.ImageLayers {
		if strings.TrimSpace(layer.ID) == "" || strings.TrimSpace(layer.AssetID) == "" {
			return fmt.Errorf("overlay: item %q image_layers[%d] requires id and asset_id", item.ID, index)
		}
		if _, ok := assets[layer.AssetID]; !ok {
			return fmt.Errorf("overlay: item %q image layer %q references undeclared asset %q", item.ID, layer.ID, layer.AssetID)
		}
		if _, exists := layerIDs[layer.ID]; exists {
			return fmt.Errorf("overlay: item %q has duplicate image layer id %q", item.ID, layer.ID)
		}
		layerIDs[layer.ID] = struct{}{}
		if layer.StartMS < 0 || layer.EndMS <= layer.StartMS || layer.EndMS > parentDuration {
			return fmt.Errorf("overlay: item %q image layer %q has invalid relative timing [%d,%d) for parent duration %dms", item.ID, layer.ID, layer.StartMS, layer.EndMS, parentDuration)
		}
		if strings.TrimSpace(layer.PresetID) == "" {
			return fmt.Errorf("overlay: item %q image layer %q requires preset_id", item.ID, layer.ID)
		}
	}
	if strings.TrimSpace(item.EntityCaption) != "" {
		return fmt.Errorf("overlay: item %q uses image_layers; put captions on the owning image layer, not entity_caption", item.ID)
	}
	return validateImageComposition(item, kind)
}

func validateImageComposition(item semanticItem, kind ItemKind) error {
	if item.CompositionID == "" {
		return nil
	}
	composition := imageCompositionFor(item.CompositionID)
	if composition == nil {
		return fmt.Errorf("overlay: item %q has unsupported image composition_id %q", item.ID, item.CompositionID)
	}
	if behaviorOf(kind) != behaviorImage {
		return fmt.Errorf("overlay: item %q composition_id %q requires an image item", item.ID, item.CompositionID)
	}
	imageCount := len(item.Assets)
	if len(item.ImageLayers) > 0 {
		imageCount = len(item.ImageLayers)
	}
	if imageCount != composition.ImageCount {
		return fmt.Errorf("overlay: item %q composition_id %q requires %d images, got %d", item.ID, item.CompositionID, composition.ImageCount, imageCount)
	}
	captionCount := 0
	if len(item.ImageLayers) == 0 {
		if strings.TrimSpace(item.EntityCaption) != "" {
			captionCount = 1
		}
	} else {
		for _, layer := range item.ImageLayers {
			if strings.TrimSpace(layer.Caption) != "" {
				captionCount++
			}
		}
	}
	if composition.Caption == nil {
		if captionCount != 0 {
			return fmt.Errorf("overlay: item %q composition_id %q does not allow captions", item.ID, item.CompositionID)
		}
		return nil
	}
	if captionCount < composition.Caption.Minimum || captionCount > composition.Caption.Maximum {
		return fmt.Errorf("overlay: item %q composition_id %q requires between %d and %d captions, got %d", item.ID, item.CompositionID, composition.Caption.Minimum, composition.Caption.Maximum, captionCount)
	}
	return nil
}
