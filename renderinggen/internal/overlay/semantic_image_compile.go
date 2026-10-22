// semantic_image_compile.go owns the image dispatcher: it routes a resolved
// image item through the recipe registry (single, premium multi-image,
// visual accents) and the shared single-layer lowering. Admission lives in
// semantic_image_validate.go; captions in semantic_image_caption.go.
package overlay

import (
	"fmt"
	"image"
	"os"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// compileImageItemCore is the pure recipe/motion lowering of one image item.
// The runtime effect decoration is applied by the compileImageItem wrapper in
// semantic_image_effects.go; keep this function effect-stack agnostic.
// entityCaptionLayer builds the caption layer anchored to the image layer of
// layers, or nil when the item declares no caption. Both image lowering paths
// share the anchor index and the error path.
func entityCaptionLayer(ri resolvedItem, src *semanticPlan, layers []Layer) (*Layer, error) {
	caption := strings.TrimSpace(ri.Item.EntityCaption)
	if caption == "" {
		return nil, nil
	}
	imageIndex := premiumImageLayerIndex(layers)
	captionLayer, err := compileEntityCaptionLayer(ri, src, ri.Item, caption, &layers[imageIndex], "entity")
	if err != nil {
		return nil, err
	}
	return &captionLayer, nil
}

func compileImageItemCore(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	// Item-level controls apply only to the standalone image or to a catalog
	// recipe that coordinates multiple image layers. Ordinary composite children own their
	// independent motion_params and are validated in compileSingleImageLayer.
	if len(ri.Item.ImageLayers) > 0 {
		if _, _, err := motionWindows(ri.Item.MotionParams, ri.Preset.Motion.Exit); err != nil {
			return nil, err
		}
	}
	resolvedImageMotion, imageRecipe, err := resolveImageRecipeMotion(ri.Item.MotionID, true)
	if err != nil {
		return nil, err
	}
	groupRecipe := imageGroupRecipeDefinition(imageRecipe)
	if groupRecipe != nil && (len(ri.Item.ImageLayers) == 0 || !isImageKind(ri.Kind)) {
		return nil, fmt.Errorf("overlay: group motion %q requires an image item with image_layers", groupRecipe.ID)
	}
	premium := premiumImageRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(imageRecipe) && premium == nil {
		return nil, fmt.Errorf("overlay: multi-image motion %q in category %q has no recipe lowering", ri.Item.MotionID, imageRecipe.Category)
	}
	visualAccents := visualAccentsRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(visualAccents) {
		return nil, fmt.Errorf("overlay: motion %q requires multiple image_layers", visualAccents.ID)
	}
	if visualAccents != nil && visualAccents.GroupRecipe != nil {
		return nil, fmt.Errorf("overlay: visual accents cannot be used as image group recipes")
	}
	if ri.Kind == KindLightLeak && len(ri.Item.Assets) == 0 && len(ri.Item.ImageLayers) == 0 {
		if visualAccents == nil || visualAccents.Category != "light_leak_v1" {
			return nil, fmt.Errorf("overlay: assetless light leak item %q requires a light_leak_v1 motion", ri.Item.ID)
		}
		opacity := 0.0
		base := Layer{ID: ri.Item.ID + "__transparent_surface", Type: "image", Position: []float64{0, 0}, Size: []float64{float64(src.Width), float64(src.Height)}, Fit: "contain", StartFrame: ri.Start, DurationFrames: ri.End - ri.Start, Opacity: &opacity}
		layers, err := compileVisualAccentsImageLayers(ri, src, base, visualAccents)
		if err != nil {
			return nil, err
		}
		out := make([]Layer, 0, len(layers)-1)
		for _, layer := range layers {
			if layer.ID != base.ID {
				out = append(out, layer)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("overlay: light leak item %q produced no glow layers", ri.Item.ID)
		}
		return out, nil
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
			captionLayer, err := entityCaptionLayer(ri, src, layers)
			if err != nil {
				return nil, err
			}
			if captionLayer != nil {
				layers = append(layers, *captionLayer)
			}
			return layers, nil
		}
		layers, err := compilePremiumImageLayers(ri, src, layer, premium)
		if err != nil {
			return nil, err
		}
		captionLayer, err := entityCaptionLayer(ri, src, layers)
		if err != nil {
			return nil, err
		}
		if captionLayer != nil {
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			return append(layers, *captionLayer), nil
		}
		if premium != nil && premium.ImageRecipe.RequireCaption {
			return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
		}
		return layers, nil
	}
	if groupRecipe != nil {
		if len(ri.Item.ImageLayers) != len(groupRecipe.GroupRecipe.MemberTracks) {
			return nil, fmt.Errorf("overlay: motion %q supports %d image layers, got %d", groupRecipe.ID, len(groupRecipe.GroupRecipe.MemberTracks), len(ri.Item.ImageLayers))
		}
		return compileImageGroupRecipe(ri, src, registry, groupRecipe)
	}
	if isMultiImageRecipe(premium) {
		if !motionDefinitionAdmitsTarget(premium, TargetImage) {
			return nil, fmt.Errorf("overlay: item %q motion %q is not supported for target %q", ri.Item.ID, ri.Item.MotionID, TargetImage)
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
	return compileCompositeImageLayers(ri, src, registry)
}

// resolveImageRecipeMotion resolves the item's registered motion and selects
// the recipe definition it lowers through. A group recipe counts as an image
// recipe only when the caller allows it: the composite path deliberately
// rejects group recipes because its members are lowered by the group compiler.
func resolveImageRecipeMotion(motionID string, allowGroupRecipe bool) (*resolvedMotion, *motion.MotionDefinition, error) {
	resolved, err := resolveRegisteredMotion(motionID)
	if err != nil {
		return nil, nil, fmt.Errorf("overlay: image motion %q: %w", motionID, err)
	}
	if resolved == nil || resolved.definition == nil {
		return resolved, nil, nil
	}
	recipe := resolved.definition
	if recipe.ImageRecipe != nil || (allowGroupRecipe && recipe.GroupRecipe != nil) {
		return resolved, recipe, nil
	}
	return resolved, nil, nil
}

// resolveAssetPaths maps asset ids to their registered file paths. Every image
// lowering path must resolve identically, so the map is built here instead of
// at each call site.
func resolveAssetPaths(registry *assetRegistry, refs []SemanticAssetRef) map[string]string {
	paths := make(map[string]string, len(refs))
	for _, ref := range refs {
		paths[ref.ID] = registry.Path(ref.ID)
	}
	return paths
}

// compileCompositeImageLayers lowers every image_layers child through the
// shared single-layer path: independent asset, preset, motion, caption and
// explicit position params per child. Entity groups reuse it after applying
// slot defaults, so composite and group members can never drift apart.
func compileCompositeImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	_, imageRecipe, err := resolveImageRecipeMotion(ri.Item.MotionID, false)
	if err != nil {
		return nil, err
	}
	premium := premiumImageRecipeDefinition(imageRecipe)
	assets := resolveAssetPaths(registry, ri.Item.Assets)

	layers := make([]Layer, 0, len(ri.Item.ImageLayers)*2)
	for index, child := range ri.Item.ImageLayers {
		childResolved, assetPath, preset, err := resolveImageChild(ri, assets, child, src, imageChildOptions{
			sequence: ri.Item.CameraMotionID != "",
			index:    index,
		})
		if err != nil {
			return nil, err
		}
		params := childResolved.Params
		imageStart, imageEnd := childResolved.Start, childResolved.End
		layer, err := compileSingleImageLayer(childResolved, src, assetPath, preset, childResolved.Item.MotionID, childResolved.Item.MotionParams, imageStart, imageEnd, childResolved.Item.ID)
		if err != nil {
			return nil, err
		}
		decorated := []Layer{layer}
		if ri.Item.CameraMotionID == "" {
			decorated, err = compilePremiumImageLayers(childResolved, src, layer, premium)
			if err != nil {
				return nil, err
			}
		}
		// Camera sequences use the portrait viewport as a clean crop, without
		// inherited decorative masks or side-fill effects.
		if ri.Item.CameraMotionID != "" && len(decorated) > 0 {
			imageIndex := premiumImageLayerIndex(decorated)
			imageLayer := &decorated[imageIndex]
			// Presets supply a default box, but camera-sequence dimensions belong
			// to the sequence layout. Reapply them after preset/premium lowering so
			// a preset cannot silently shrink a full-frame portrait back to 480px.
			if width, ok := numericValue(params["width"]); ok && width > 0 {
				imageLayer.BoxWidth = int(width)
			}
			if height, ok := numericValue(params["height"]); ok && height > 0 {
				imageLayer.BoxHeight = int(height)
			}
			imageLayer.Size = []float64{float64(imageLayer.BoxWidth), float64(imageLayer.BoxHeight)}
			imageLayer.EntityImage = false
			imageLayer.Fit = FitCover
			// An image preset can add rounded corners after the sequence layout
			// has cleared its frame. Camera sequences use the full rectangular
			// photo surface, so do not let preset radius leak into the crop.
			imageLayer.Radius = 0
			imageLayer.Masks = nil
			// The camera sequence should stay on the native GPU path. The default
			// image drop shadow currently dispatches through the software effect
			// fallback, so omit it for these clean editorial photo cards.
			if imageLayer.Style != nil {
				imageLayer.Style.Shadow = nil
				imageLayer.Style.Glow = nil
			}
			imageLayer.Position = []float64{float64(index * src.Width), 0}
		}
		// Sequence camera tracks only affect world-space layers. Composite
		// portraits are normally 2D HUD overlays, and image motion lowering can
		// reset Enable3D. Apply the sequence rig after lowering so every image
		// component follows the scene camera.
		if ri.Item.CameraMotionID != "" {
			for i := range decorated {
				decorated[i].Animation = nil
				decorated[i].TextAnimators = nil
				decorated[i].Enable3D = true
			}
		}
		if _, hasChildEffects := params["effects"]; hasChildEffects {
			decorated, err = applyImageItemEffects(childResolved, decorated)
			if err != nil {
				return nil, err
			}
		}
		layers = append(layers, decorated...)
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			imageIndex := premiumImageLayerIndex(decorated)
			captionLayer, err := compileImageCaptionLayer(ri, src, childResolved.Item, caption, &decorated[imageIndex], child.ID, textRoleImageCaption)
			if err != nil {
				return nil, err
			}
			if ri.Item.CameraMotionID != "" {
				// Caption layout reserves vertical space by nudging its linked
				// image. Camera sequences keep the portrait centered and position
				// the caption independently in screen space.
				decorated[imageIndex].Position = []float64{float64(index * src.Width), 0}
				layerIndex := len(layers) - len(decorated) + imageIndex
				layers[layerIndex].Position = append([]float64(nil), decorated[imageIndex].Position...)
				captionStart, captionEnd := childResolved.Start, childResolved.End
				// Keep the label attached to the image while the camera is settled.
				// During the pan, clear the old name; reveal the next name once the
				// camera reaches its portrait. The caption itself is screen-space so
				// it stays directly below the centered subject as the camera travels.
				if index+1 < len(ri.Item.ImageLayers) {
					nextStart, _ := msFrames(ri.Item.ImageLayers[index+1].StartMS, ri.Item.ImageLayers[index+1].StartMS, int64(src.FPSNum), int64(src.FPSDen))
					_, travel := msFrames(0, sequenceTravelMS, int64(src.FPSNum), int64(src.FPSDen))
					panStart := ri.Start + nextStart - travel
					if panStart < captionEnd {
						captionEnd = panStart
					}
				}
				captionLayer.StartFrame = captionStart
				captionLayer.DurationFrames = max(int64(1), captionEnd-captionStart)
				captionLayer.Animation = nil
				captionLayer.TextAnimators = nil
				captionLayer.Enable3D = false
				// Chronon TEXT layers carry absolute top-left-origin box centers. The
				// caption is screen-space during a sequence pan, so pin it to the
				// absolute canvas center and keep the resolved below-image
				// vertical position converted back to absolute coordinates.
				// (Image layers use center offsets; text layers do not.)
				captionY := 0.0
				if len(captionLayer.Position) > 1 {
					captionY = captionLayer.Position[1]
				}
				captionLayer.Position = []float64{float64(src.Width) / 2, captionY + float64(src.Height)/2}
			} else if child.CaptionEndMS > 0 {
				captionStart, _ := msFrames(child.CaptionStartMS, child.CaptionStartMS, int64(src.FPSNum), int64(src.FPSDen))
				captionEnd, _ := msFrames(child.CaptionEndMS, child.CaptionEndMS, int64(src.FPSNum), int64(src.FPSDen))
				captionLayer.StartFrame = ri.Start + captionStart
				captionLayer.DurationFrames = max(int64(1), captionEnd-captionStart)
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			layers = append(layers, captionLayer)
		}
	}
	return layers, nil
}

// sequenceImageLayoutParams gives each image a portrait viewport. Cover crops
// landscape padding so baked-in blurred side bands cannot dominate the photo.
func sequenceImageLayoutParams(params map[string]any, src *semanticPlan, index int) map[string]any {
	if src == nil || src.Width <= 0 || src.Height <= 0 {
		return params
	}
	result := make(map[string]any, len(params)+5)
	for key, value := range params {
		result[key] = value
	}
	// Fill most of the frame so portraits remain the visual focus. The reserved
	// lower margin still gives the screen-space caption room beneath the image.
	portraitHeight := float64(src.Height) * sequencePortraitHeightRatio
	result["width"] = int(portraitHeight * 0.73)
	result["height"] = int(portraitHeight)
	result["position_x"] = float64(index * src.Width)
	result["position_y"] = 0.0
	result["fit"] = "cover"
	return result
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
	defaultImageBox(&layer)
	sequenceStatic := ri.Item.CameraMotionID != ""
	if sequenceStatic {
		motionID, motionParams, resolved = "", nil, nil
	}
	if motionID == "" && preset.ID != "" && !sequenceStatic {
		motionID = runtimeDefaultImageMotionID(src, layerID)
		if motionID != "" {
			var err error
			resolved, err = resolveRegisteredMotion(motionID)
			if err != nil {
				return Layer{}, fmt.Errorf("overlay: default image motion %q: %w", motionID, err)
			}
		}
	}
	if motionID != "" {
		var err error
		// Keep the selected motion ID intact. Replacing a failed GPU motion with
		// a different recipe concealed unsupported runtime behavior.
		animation, err := imageMotionAnimationResolved(motionID, motionParams, end-start, preset.Motion.Exit, layerID, TargetImage, resolved)
		if err != nil {
			return Layer{}, err
		}
		// Explicit image motion_id is authoritative for both entity and generic
		// images; 2.5D recipes are no longer silently discarded for portraits.
		applyMotionRouting(&layer, animation)
	} else if preset.ID != "" && !sequenceStatic {
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
	} else if fit, ok := ri.Params["fit"].(string); ok && (fit == "cover" || fit == "contain" || fit == "fill") {
		// Bounded payloads choose how pixels fill the authored box; the
		// default contain path is preserved when the key is absent.
		layer.Fit = fit
	}
	// Bounded clips the card inside its authored box (the renderer already
	// clips to Size/Radius); validation owns the contract, lowering only
	// keeps the authored fit instead of falling back to a preset default.
	if bounded, ok := ri.Params["bounded"].(bool); ok && bounded && layer.Fit == "" {
		layer.Fit = FitContain
	}
	frame := frameFromImageParams(ri.Params, ri.Item.Frame)
	if err := applyFrameTreatment(&layer, frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: image item %q: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// runtimeDefaultImageMotionIDs is the canonical-catalog projection used when
// an image omits motion_id. Keep this automatic pool to the single-image
// families; recipe families (premium accents, stacks, captions) remain explicit
// choices because they require extra geometry or assets. IDs are sorted so
// selection is stable across builds.
var runtimeDefaultImageMotionIDs = func() []string {
	var ids []string
	for _, family := range []string{"overlay_v3_image", "image_25d_clean_v1", "editorial_image_v1"} {
		ids = append(ids, motion.Registry.SelectableCategoryMotionIDs(family)...)
	}
	sort.Strings(ids)
	unique := ids[:0]
	for _, id := range ids {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique
}()

// runtimeDefaultImageMotionID returns a deterministic but video/item-specific
// choice. Explicit motion_id and animation_policies are handled before this
// fallback, and therefore remain authoritative.
var runtimeDefaultCaptionMotionIDs = func() []string {
	var ids []string
	for _, id := range motion.Registry.List() {
		if motionAdmitsTarget(id, TargetCaption) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}()

func runtimeDefaultImageMotionID(src *semanticPlan, itemID string) string {
	return runtimeDefaultImageMotionIDFor(runtimeDefaultImageMotionIDs, src, itemID)
}

func runtimeDefaultImageMotionIDFor(pool []string, src *semanticPlan, itemID string) string {
	if len(pool) == 0 || src == nil {
		return ""
	}
	return pool[styleHash("runtime_default_image_motion", src.PlanID, src.VideoID, itemID, uint64(len(pool)))]
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
