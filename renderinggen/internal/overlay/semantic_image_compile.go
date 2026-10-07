package overlay

import (
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
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
			captionLayer, err := compileEntityCaptionLayer(ri, src, childItem, caption, &decorated[imageIndex], child.ID)
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

// ValidateEntityCaptionCollisions rejects caption boxes that overlap in both
// canvas space and time. Call after entity-image geometry has been fitted to
// the materialized source assets; pre-materialization boxes can be provisional.
func ValidateEntityCaptionCollisions(layers []Layer) error {
	type captionBounds struct {
		id, imageID              string
		left, top, right, bottom float64
		start, end               int64
	}
	var captions []captionBounds
	for _, layer := range layers {
		if layer.EntityCaptionForImageID == "" {
			continue
		}
		if len(layer.Position) < 2 || len(layer.Size) < 2 || layer.Size[0] <= 0 || layer.Size[1] <= 0 || layer.StartFrame < 0 || layer.DurationFrames <= 0 {
			return fmt.Errorf("overlay: caption %q for image %q has invalid collision bounds or frame window", layer.ID, layer.EntityCaptionForImageID)
		}
		captions = append(captions, captionBounds{
			id: layer.ID, imageID: layer.EntityCaptionForImageID,
			left: layer.Position[0] - layer.Size[0]/2, top: layer.Position[1] - layer.Size[1]/2,
			right: layer.Position[0] + layer.Size[0]/2, bottom: layer.Position[1] + layer.Size[1]/2,
			start: layer.StartFrame, end: layer.StartFrame + layer.DurationFrames,
		})
	}
	for i := 0; i < len(captions); i++ {
		for j := i + 1; j < len(captions); j++ {
			a, b := captions[i], captions[j]
			// Layers use half-open frame windows [start,end), matching the
			// semantic timing contract: captions that never appear together
			// cannot visually collide even when they reuse the same layout.
			if a.start < b.end && b.start < a.end && a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top {
				return fmt.Errorf("overlay: captions %q (image %q) and %q (image %q) overlap in a shared frame window; adjust caption_layout or image positions",
					a.id, a.imageID, b.id, b.imageID)
			}
		}
	}
	return nil
}

func compileEntityCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string) (Layer, error) {
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("overlay: item %q image caption has no positive image geometry", parent.Item.ID)
	}
	// The resolver owns the geometry: image bottom_center + margin anchor,
	// safe-area clamping and long-name font fitting all happen there. The
	// lowering below only writes the resolver's answer into the layer.
	imageBounds := EntityCardImageBoundsFromCenter(src.Width, src.Height, image.Position, image.Size[0], image.Size[1])
	layout, err := ResolveEntityCardLayoutAt(src.Width, src.Height, imageBounds, caption, child.CaptionLayout)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q entity caption layout: %w", parent.Item.ID, err)
	}
	// Vertical fallback: the resolver may shift the image up so the pair
	// image + margin + caption fits inside the safe area. Image layers are
	// positioned relative to the canvas center, so an upward canvas shift of
	// `shift` px converts to exactly minus `shift`; the caption's geometry
	// below is canvas-absolute.
	if shift := imageBounds.Y - layout.ImageBounds.Y; shift != 0 {
		if len(image.Position) < 2 {
			image.Position = []float64{0, 0}
		}
		image.Position[1] -= shift
	}
	captionBounds := layout.CaptionBounds
	width := int(captionBounds.Width)
	if width < 1 {
		width = 1
	}
	captionItem := parent.Item
	captionItem.CaptionMotionID = child.CaptionMotionID
	captionItem.CaptionMotionParams = child.CaptionMotionParams
	captionItem.ID = parent.Item.ID + ":" + childID + ":caption"
	captionItem.Kind = string(KindEntityCard)
	captionItem.Template = "PERSON_DEFAULT"
	captionItem.PresetID = PhraseDefaultPresetID
	captionItem.Text = caption
	// The caption is a first-class animated layer: resolve the requested
	// motion (or the shared default) and refuse non-text motions here, before
	// compileTextLayer lowers MotionID — an image motion on a caption would
	// otherwise lower camera-backed tracks a text layer cannot honor.
	captionMotion := EntityCaptionMotionID(child.CaptionMotionID)
	if !motionAdmitsTarget(captionMotion, "caption") {
		return Layer{}, fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", parent.Item.ID, captionMotion)
	}
	captionItem.MotionID = captionMotion
	captionItem.MotionParams = child.CaptionMotionParams
	if captionItem.MotionParams == nil {
		captionItem.MotionParams = map[string]any{"enter_frames": 8}
	}
	captionItem.Assets = nil
	captionItem.ImageLayers = nil
	positionX := captionBounds.CenterX
	positionY := captionBounds.CenterY
	captionItem.Params = map[string]any{
		"position_x":   positionX,
		"position_y":   positionY,
		"font_size_px": captionBounds.FontSize,
	}
	// A run-level font_family is the explicit runtime style choice. Preserve
	// the entity preset's typeface only when the caller did not choose one.
	if family, ok := parent.Params["font_family"].(string); ok && strings.TrimSpace(family) != "" {
		captionItem.Params["font_family"] = strings.TrimSpace(family)
	}
	captionItem.Style = nil
	captionResolved := resolvedItem{
		Item: captionItem, Spec: parent.Spec, Kind: KindEntityCard,
		Params: captionItem.Params,
		Start:  image.StartFrame, End: image.StartFrame + image.DurationFrames,
	}
	captionResolved.Preset = phraseDefaultPreset()
	captionLayer, err := compileTextLayer(captionResolved, src, captionItem.ID)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: entity image caption %q: %w", captionItem.ID, err)
	}
	captionLayer.BoxWidth = width
	captionLayer.BoxHeight = int(captionBounds.Height)
	captionLayer.Size = []float64{captionBounds.Width, captionBounds.Height}
	captionLayer.Position = []float64{captionBounds.CenterX, captionBounds.CenterY}
	// Cinematic nameplate treatment: a large warm-white title, a restrained
	// dark keyline and soft drop shadow keep names readable over moving footage.
	captionLayer.Style.Fill = "#F8F5EA"
	family, _ := captionItem.Params["font_family"].(string)
	if family == "" {
		family = child.CaptionFontFamily
	}
	if family = strings.TrimSpace(family); family != "" {
		fontPath, ok := runtimeFontPath(family)
		if !ok {
			return Layer{}, fmt.Errorf("overlay: item %q entity caption font family %q is unsupported", parent.Item.ID, family)
		}
		captionLayer.Style.Font = fontPath
	}
	if fill := strings.TrimSpace(child.CaptionColor); fill != "" {
		if _, err := parseHexColor(fill); err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q entity caption color: %w", parent.Item.ID, err)
		}
		captionLayer.Style.Fill = fill
		captionLayer.Style.Stroke = nil
		captionLayer.Style.Shadow = nil
		captionLayer.Style.Glow = nil
	}
	captionLayer.Style.FontSize = captionBounds.FontSize
	captionLayer.Style.MinFontSize = captionBounds.FontSize
	captionLayer.Style.MaxFontSize = captionBounds.FontSize
	captionLayer.Style.Stroke = &LayerStroke{Color: "#111827", Width: 2.0}
	captionLayer.Style.Shadow = &LayerShadow{Color: "#000000", Opacity: 0.78, Blur: 9, Offset: []float64{0, 3}}
	// A low intensity warm halo adds a current editorial finish without
	// washing out the letterforms or competing with the portrait.
	captionLayer.Style.Glow = &LayerGlow{Color: "#F8F5EA", Radius: 14, Intensity: 0.24}
	// Avoid text background cards here: the native Vulkan text path lowers
	// those to a text_card node that the strict GPU backend cannot execute.
	captionLayer.Style.Background = nil
	captionLayer.EntityCaptionForImageID = image.ID
	return captionLayer, nil
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
