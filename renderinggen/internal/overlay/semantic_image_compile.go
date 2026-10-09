// semantic_image_compile.go owns the image dispatcher: it routes a resolved
// image item through the recipe registry (single, premium multi-image,
// visual accents) and the shared single-layer lowering. Admission lives in
// semantic_image_validate.go; captions in semantic_image_caption.go.
package overlay

import (
	"fmt"
	"image"
	"log"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func compileImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	// Item-level controls apply only to the standalone image or to a catalog
	// recipe that coordinates multiple image layers. Ordinary composite children own their
	// independent motion_params and are validated in compileSingleImageLayer.
	if len(ri.Item.ImageLayers) > 0 {
		if _, _, err := motionWindows(ri.Item.MotionParams, ri.Preset.Motion.Exit); err != nil {
			return nil, err
		}
	}
	resolvedImageMotion, err := resolveRegisteredMotion(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: image motion %q: %w", ri.Item.MotionID, err)
	}
	var imageRecipe *motion.MotionDefinition
	if resolvedImageMotion != nil {
		imageRecipe = resolvedImageMotion.definition
	}
	if imageRecipe == nil || imageRecipe.ImageRecipe == nil {
		imageRecipe = nil
	}
	premium := premiumImageRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(imageRecipe) && premium == nil {
		return nil, fmt.Errorf("overlay: multi-image motion %q in category %q has no recipe lowering", ri.Item.MotionID, imageRecipe.Category)
	}
	visualAccents := visualAccentsRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(visualAccents) {
		return nil, fmt.Errorf("overlay: motion %q requires multiple image_layers", visualAccents.ID)
	}
	if len(ri.Item.ImageLayers) == 0 {
		if len(ri.Item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
		}
		if isMultiImageRecipe(premium) {
			return nil, fmt.Errorf("overlay: motion %q requires at least two image_layers and motion_params.active_layer_id", premium.ID)
		}
		layer, err := compileSingleImageLayerResolved(ri, src, registry.Path(ri.Item.Assets[0].ID), ri.Preset, ri.Item.MotionID, ri.Item.MotionParams, ri.Start, ri.End, ri.Item.ID, resolvedImageMotion)
		if err != nil {
			return nil, err
		}
		if visualAccents != nil {
			layers, err := compileVisualAccentsImageLayers(ri, src, layer, visualAccents)
			if err != nil {
				return nil, err
			}
			if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
				imageIndex := premiumImageLayerIndex(layers)
				captionLayer, err := compileEntityCaptionLayer(ri, src, ri.Item, caption, &layers[imageIndex], "entity")
				if err != nil {
					return nil, err
				}
				layers = append(layers, captionLayer)
			}
			return layers, nil
		}
		layers, err := compilePremiumImageLayers(ri, src, layer, premium)
		if err != nil {
			return nil, err
		}
		if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
			imageIndex := premiumImageLayerIndex(layers)
			captionLayer, err := compileEntityCaptionLayer(ri, src, ri.Item, caption, &layers[imageIndex], "entity")
			if err != nil {
				return nil, err
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			return append(layers, captionLayer), nil
		}
		if premium != nil && premium.ImageRecipe.RequireCaption {
			return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
		}
		return layers, nil
	}
	if isMultiImageRecipe(premium) {
		if !motionDefinitionAdmitsTarget(premium, "image") {
			return nil, fmt.Errorf("overlay: item %q motion %q is not supported for target %q", ri.Item.ID, ri.Item.MotionID, "image")
		}
		return compilePremiumMultiImageRecipe(ri, src, registry, premium)
	}
	if premium != nil && premium.ImageRecipe.RequireCaption {
		for _, child := range ri.Item.ImageLayers {
			if strings.TrimSpace(child.Caption) == "" {
				return nil, fmt.Errorf("overlay: motion %q requires a caption on every image layer", premium.ID)
			}
		}
	}
	assets := make(map[string]string, len(ri.Item.Assets))
	for _, ref := range ri.Item.Assets {
		assets[ref.ID] = registry.Path(ref.ID)
	}
	layers := make([]Layer, 0, len(ri.Item.ImageLayers)*2)
	for _, child := range ri.Item.ImageLayers {
		assetPath, ok := assets[child.AssetID]
		if !ok {
			return nil, fmt.Errorf("overlay: composite image item %q layer %q references undeclared asset %q", ri.Item.ID, child.ID, child.AssetID)
		}
		preset, err := resolveOfficialPreset(child.PresetID, string(PresetImage))
		if err != nil {
			return nil, fmt.Errorf("overlay: composite image item %q layer %q: %w", ri.Item.ID, child.ID, err)
		}
		startOffset, endOffset := msFrames(child.StartMS, child.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		start, end := ri.Start+startOffset, ri.Start+endOffset
		params := child.Params
		if params == nil {
			params = map[string]any{}
		}
		childItem := ri.Item
		childItem.ID = ri.Item.ID + ":" + child.ID
		childItem.PresetID = child.PresetID
		childItem.MotionID = child.MotionID
		childItem.MotionParams = child.MotionParams
		childItem.EntityCaption = ""
		childItem.CaptionMotionID = child.CaptionMotionID
		childItem.CaptionMotionParams = child.CaptionMotionParams
		childItem.Frame = child.Frame
		childItem.StartMS, childItem.EndMS = child.StartMS, child.EndMS
		childItem.Params = params
		childResolved := ri
		childResolved.Item = childItem
		childResolved.Params = params
		childResolved.Start, childResolved.End = start, end
		layer, err := compileSingleImageLayer(childResolved, src, assetPath, preset, child.MotionID, child.MotionParams, start, end, ri.Item.ID+":"+child.ID)
		if err != nil {
			return nil, err
		}
		// Keep camera-backed 2.5D tracks as authored. Image geometry and
		// motion are independent contract inputs; Enable3D is derived by the
		// shared motion-routing helper above.
		decorated, err := compilePremiumImageLayers(childResolved, src, layer, premium)
		if err != nil {
			return nil, err
		}
		layers = append(layers, decorated...)
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			imageIndex := premiumImageLayerIndex(decorated)
			captionLayer, err := compileImageCaptionLayer(ri, src, childItem, caption, &decorated[imageIndex], child.ID, textRoleImageCaption)
			if err != nil {
				return nil, err
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			layers = append(layers, captionLayer)
		}
	}
	return layers, nil
}

// compileSingleImageLayer is shared by standalone images and composite
// children; callers pass the correct animation controls for that semantic unit.
func compileSingleImageLayer(ri resolvedItem, src *semanticPlan, assetPath string, preset PresetDefinition, motionID string, motionParams map[string]any, start, end int64, layerID string) (Layer, error) {
	var resolved *resolvedMotion
	var err error
	if motionID != "" {
		resolved, err = resolveRegisteredMotion(motionID)
		if err != nil {
			return Layer{}, err
		}
	}
	return compileSingleImageLayerResolved(ri, src, assetPath, preset, motionID, motionParams, start, end, layerID, resolved)
}

func compileSingleImageLayerResolved(ri resolvedItem, src *semanticPlan, assetPath string, preset PresetDefinition, motionID string, motionParams map[string]any, start, end int64, layerID string, resolved *resolvedMotion) (Layer, error) {
	layer := imageLayer(ri, assetPath)
	layer.ID = imageLayerID(layerID)
	layer.StartFrame, layer.DurationFrames = start, end-start
	applyPresetDefinition(&layer, preset)
	if motionID != "" {
		var err error
		// Runtime entity imagery passes every motion through except one a
		// recorded strict-GPU run failed: the embedded motioncert snapshot is
		// the evidence (several premium recipes still fail the native lane with
		// an encoder-side error), so the gate replaces only recorded failures.
		if ri.Kind == KindEntityImage && len(ri.Item.ImageLayers) == 0 {
			safeMotionID := entityCardRuntimeImageMotion(motionID, ri.Item.ID)
			if safeMotionID != motionID {
				log.Printf("overlay: entity image item %q: strict-GPU-failed motion %q replaced by certified motion %q", ri.Item.ID, motionID, safeMotionID)
				motionID = safeMotionID
				resolved, err = resolveRegisteredMotion(motionID)
				if err != nil {
					return Layer{}, err
				}
			}
		}
		animation, err := imageMotionAnimationResolved(motionID, motionParams, end-start, preset.Motion.Exit, layerID, "image", resolved)
		if err != nil {
			return Layer{}, err
		}
		// Explicit image motion_id is authoritative for both entity and generic
		// images; 2.5D recipes are no longer silently discarded for portraits.
		applyMotionRouting(&layer, animation)
	} else if preset.ID != "" {
		animation, err := animationForPreset(preset, "", end-start)
		if err != nil {
			return Layer{}, err
		}
		applyMotionRouting(&layer, animation)
	}
	if x, ok := numericValue(ri.Params["position_x"]); ok {
		y, _ := numericValue(ri.Params["position_y"])
		layer.Position = []float64{x, y}
	} else if layer.Position == nil {
		if ri.Kind == KindEntityImage {
			layer.Position = []float64{0, 0}
			layer.Fit = FitContain
		} else if position, ok := ri.Params["position"].(string); ok && strings.EqualFold(strings.TrimSpace(position), "center") {
			layer.Position = []float64{0, 0}
		} else {
			layer.Position = resolveImageLayout(preset.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
		}
	}
	if y, ok := numericValue(ri.Params["position_y"]); ok {
		if layer.Position == nil {
			layer.Position = []float64{0, 0}
		}
		layer.Position[1] = y
	}
	if ri.Kind == KindEntityImage {
		layer.Fit = FitContain
		layer.EntityImage = true
	}
	if err := applyFrameTreatment(&layer, ri.Item.Frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: image item %q: %w", ri.Item.ID, err)
	}
	return layer, nil
}

func motionDefinitionRequires3D(resolved *resolvedMotion) bool {
	if resolved == nil || resolved.definition == nil {
		return false
	}
	d := resolved.definition
	if d.Requires3D != nil && *d.Requires3D {
		return true
	}
	for _, track := range d.Tracks {
		if motion.IsCameraBacked3DProperty(track.Property) {
			return true
		}
	}
	for _, animator := range d.TextAnimators {
		for _, track := range animator.Properties {
			if motion.IsCameraBacked3DProperty(track.Property) {
				return true
			}
		}
	}
	return false
}

// FitEntityImageLayerToAsset matches an entity image's bounded contain box to
// the source aspect ratio. Call after the processor materializes the asset at
// assetPath; compilation alone only knows its logical path.
func FitEntityImageLayerToAsset(layer *Layer, assetPath string) error {
	if layer == nil || strings.TrimSpace(assetPath) == "" || layer.BoxWidth <= 0 || layer.BoxHeight <= 0 {
		return fmt.Errorf("missing image geometry")
	}
	f, err := os.Open(assetPath)
	if err != nil {
		return err
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid source dimensions %dx%d", config.Width, config.Height)
	}
	maxW, maxH := layer.BoxWidth, layer.BoxHeight
	if int64(config.Width)*int64(maxH) > int64(config.Height)*int64(maxW) {
		layer.BoxWidth = maxW
		layer.BoxHeight = max(1, int(float64(maxW)*float64(config.Height)/float64(config.Width)+0.5))
	} else {
		layer.BoxHeight = maxH
		layer.BoxWidth = max(1, int(float64(maxH)*float64(config.Width)/float64(config.Height)+0.5))
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
	// The box now has the source's aspect ratio, so cover fills the complete
	// surface without cropping meaningful image content or leaving a dark
	// contain matte around the texture.
	layer.Fit = FitCover
	return nil
}
