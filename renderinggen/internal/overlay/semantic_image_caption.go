// semantic_image_caption.go owns the caption half of the image subsystem:
// collision contract for linked captions and the entity-caption role
// lowering through the shared text spec seam. Image layer lowering stays in
// semantic_image_compile.go; composition validation in
// semantic_image_validate.go.
package overlay

import (
	"fmt"
	"strings"
)

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
