package overlay

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

// imageChildOptions selects the two lowering contexts a composite child can
// appear in. Ordinary composite/group children keep their own motion and
// timing window; a camera-sequence image is a static surface that spans the
// parent window and hands its caption to the screen-space sequence layout.
type imageChildOptions struct {
	staticMotion bool
	sequence     bool
	index        int
}

// resolveImageChild is the single owner of the fields an image_layers child
// inherits from its parent item. Composite lowering, image-group recipes and
// multi-image stack recipes all use it, so caption styling, frame treatment and
// timing cannot drift between the three paths.
func resolveImageChild(ri resolvedItem, assets map[string]string, child SemanticImageLayer, src *semanticPlan, opts imageChildOptions) (resolvedItem, string, PresetDefinition, error) {
	assetPath, ok := assets[child.AssetID]
	if !ok {
		return resolvedItem{}, "", PresetDefinition{}, fmt.Errorf("overlay: image item %q layer %q references undeclared asset %q", ri.Item.ID, child.ID, child.AssetID)
	}
	preset, err := resolveOfficialPreset(child.PresetID, string(PresetImage))
	if err != nil {
		return resolvedItem{}, "", PresetDefinition{}, fmt.Errorf("overlay: image item %q layer %q: %w", ri.Item.ID, child.ID, err)
	}

	params := child.Params
	if params == nil {
		params = map[string]any{}
	}
	if _, requested := params["image_backdrop"]; requested {
		return resolvedItem{}, "", PresetDefinition{}, fmt.Errorf("overlay: image_backdrop is available only on a standalone image item, not image_layers[%q]", child.ID)
	}
	if opts.sequence {
		params = sequenceImageLayoutParams(params, src, opts.index, imageAssetIsLandscape(assetPath))
	}

	childItem := ri.Item
	childItem.ID = ri.Item.ID + ":" + child.ID
	childItem.PresetID = child.PresetID
	if opts.staticMotion {
		childItem.MotionID = ""
		childItem.MotionParams = nil
	} else {
		childItem.MotionID = child.MotionID
		childItem.MotionParams = child.MotionParams
	}
	childItem.EntityCaption = ""
	childItem.CaptionMotionID = child.CaptionMotionID
	childItem.CaptionMotionParams = child.CaptionMotionParams
	childItem.CaptionFontFamily = child.CaptionFontFamily
	childItem.CaptionColor = child.CaptionColor
	childItem.CaptionLayout = child.CaptionLayout
	childItem.Frame = child.Frame
	childItem.StartMS, childItem.EndMS = child.StartMS, child.EndMS
	childItem.Params = params
	var imageStart, imageEnd int64
	if opts.sequence {
		// Every portrait is present before the camera arrives. Avoid separate
		// entrance animations that can reveal a caption ahead of its photo.
		childItem.CaptionLayout = "below"
		// Camera sequences use a clean photo surface. Rounded masks and
		// decorative preset frames can cover the portrait while the camera
		// moves, so leave that treatment to the source image itself.
		childItem.Frame = nil
		imageStart, imageEnd = ri.Start, ri.End
	} else {
		startOffset, endOffset := imageLayerFrames(child, int64(src.FPSNum), int64(src.FPSDen))
		imageStart, imageEnd = ri.Start+startOffset, ri.Start+endOffset
	}

	childResolved := ri
	childResolved.Item = childItem
	childResolved.Params = params
	childResolved.Start, childResolved.End = imageStart, imageEnd
	return childResolved, assetPath, preset, nil
}

// imageAssetIsLandscape reports the source orientation when the staged image
// format has a standard-library decoder. Unknown formats keep the established
// portrait sequence layout rather than guessing from the filename.
func imageAssetIsLandscape(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	return err == nil && config.Height > 0 && float64(config.Width)/float64(config.Height) >= 1.2
}
