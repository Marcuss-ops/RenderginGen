package overlay

import (
	"fmt"
	"image"
	"os"
	"strings"
)

// compileImageLayers lowers an image kind (IMAGE_OVERLAY/PRODUCT/LOGO/…) to one
// or more Chronon image layers. Composite children share one semantic item and
// queue render while retaining independent timing and motion.
func validateSemanticImageLayers(item semanticItem) error {
	if len(item.ImageLayers) == 0 {
		return nil
	}
	kind := ItemKind(strings.ToLower(strings.TrimSpace(item.Kind)))
	if kind == "" {
		kind = templateSpecFor(item.Template).Kind
	}
	if behaviorOf(kind) != behaviorImage || len(item.ImageLayers) < 2 || len(item.Assets) < 2 {
		return fmt.Errorf("overlay: item %q image_layers require an image item with at least two layers and assets", item.ID)
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
	return nil
}
func compileImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	premium, err := premiumImageDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: image motion %q: %w", ri.Item.MotionID, err)
	}
	visualAccents, err := visualAccentsDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: visual accents motion %q: %w", ri.Item.MotionID, err)
	}
	if visualAccents != nil && visualAccents.ImageRecipe != nil && visualAccents.ImageRecipe.Stack {
		return nil, fmt.Errorf("overlay: motion %q requires multiple image_layers", visualAccents.ID)
	}
	if len(ri.Item.ImageLayers) == 0 {
		if len(ri.Item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
		}
		if premium != nil && premium.ImageRecipe.Stack {
			return nil, fmt.Errorf("overlay: motion %q requires at least two image_layers and motion_params.active_layer_id", premium.ID)
		}
		layer, err := compileSingleImageLayer(ri, src, registry.Path(ri.Item.Assets[0].ID), ri.Preset, ri.Item.MotionID, ri.Item.MotionParams, ri.Start, ri.End, ri.Item.ID)
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
	if premium != nil && premium.ImageRecipe.Stack {
		return compilePremiumImageStack(ri, src, registry, premium)
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
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			imageIndex := premiumImageLayerIndex(decorated)
			captionLayer, err := compileEntityCaptionLayer(ri, src, childItem, caption, &decorated[imageIndex], child.ID)
			if err != nil {
				return nil, err
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			layers = append(layers, decorated...)
			layers = append(layers, captionLayer)
		} else {
			layers = append(layers, decorated...)
		}
	}
	return layers, nil
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
	if !entityCaptionMotionAllowed(captionMotion) {
		captionMotion = ""
	}
	captionItem.MotionID = captionMotion
	captionItem.MotionParams = map[string]any{"enter_frames": 8}
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
func compileSingleImageLayer(ri resolvedItem, src *semanticPlan, assetPath string, preset PresetDefinition, motionID string, motionParams map[string]any, start, end int64, layerID string) (Layer, error) {
	layer := imageLayer(ri, assetPath)
	layer.ID = imageLayerID(layerID)
	layer.StartFrame, layer.DurationFrames = start, end-start
	applyPresetDefinition(&layer, preset)
	if motionID != "" {
		animation, err := imageMotionAnimation(motionID, motionParams, end-start, preset.Motion.Exit)
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
	if x, ok := numericParam(ri.Params["position_x"]); ok {
		y, _ := numericParam(ri.Params["position_y"])
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
	if y, ok := numericParam(ri.Params["position_y"]); ok {
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
func numericParam(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
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
