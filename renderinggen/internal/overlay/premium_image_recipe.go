package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func premiumImageDefinition(id string) (*motion.MotionDefinition, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil || plugin == nil {
		return nil, err
	}
	declarative, ok := plugin.(motion.DeclarativePlugin)
	if !ok || declarative.Definition.Category != "image_premium_v1" {
		return nil, nil
	}
	return &declarative.Definition, nil
}

func imageMotionAnimation(id string, params map[string]any, duration int64, presetExit int) (*LayerAnimation, error) {
	definition, err := premiumImageDefinition(id)
	if err != nil {
		return nil, err
	}
	if definition != nil {
		animation := premiumTracks(*definition, definition.Tracks, duration)
		enter, exit := motionWindows(params, definition.Exit)
		if animation != nil {
			if enter > 0 {
				animation.Tracks = retimeMotionTracks(animation.Tracks, duration, int64(enter))
			}
			if exit != definition.Exit {
				animation.Tracks = appendExitTracks(animation.Tracks, exit, duration)
			}
		}
		return animation, nil
	}
	return animationForMotion(id, params, "", duration, presetExit)
}

func premiumTracks(definition motion.MotionDefinition, tracks []motion.TrackDefinition, duration int64) *LayerAnimation {
	if len(tracks) == 0 {
		return nil
	}
	converted := make([]AnimationTrack, 0, len(tracks))
	for _, track := range tracks {
		keys := make([]AnimationKeyframe, len(track.Keyframes))
		for i, key := range track.Keyframes {
			keys[i] = AnimationKeyframe{Frame: key.Frame, Value: key.Value}
		}
		converted = append(converted, AnimationTrack{Property: track.Property, Keyframes: keys, Easing: track.Easing})
	}
	entrance := entranceFrames(definition.Enter, definition.Exit, duration, false)
	converted = retimeMotionTracks(converted, entrance, int64(definition.Enter))
	return &LayerAnimation{Tracks: converted}
}

func premiumNativePathAnimation(track motion.TrackDefinition, duration, authoredEnter int64) *LayerPathAnimation {
	converted := []AnimationTrack{{Property: track.Property, Easing: track.Easing, Keyframes: make([]AnimationKeyframe, len(track.Keyframes))}}
	for i, key := range track.Keyframes {
		converted[0].Keyframes[i] = AnimationKeyframe{Frame: key.Frame, Value: key.Value}
	}
	entrance := entranceFrames(int(authoredEnter), 0, duration, false)
	converted = retimeMotionTracks(converted, entrance, authoredEnter)
	if duration > entrance && len(converted[0].Keyframes) > 0 {
		last := converted[0].Keyframes[len(converted[0].Keyframes)-1]
		converted[0].Keyframes = append(converted[0].Keyframes,
			AnimationKeyframe{Frame: lastValidFrame(duration), Value: last.Value})
	}
	return &LayerPathAnimation{Easing: converted[0].Easing, Keyframes: converted[0].Keyframes}
}

func premiumWipeMaskPath(width, height float64, reveal bool) []LayerPathCommand {
	left, right := -width/2, width/2
	if !reveal {
		epsilon := math.Max(0.001, width*0.00001)
		right = math.Min(right, left+epsilon)
	}
	return []LayerPathCommand{
		{Type: "move_to", Point: []float64{left, -height / 2}},
		{Type: "line_to", Point: []float64{right, -height / 2}},
		{Type: "line_to", Point: []float64{right, height / 2}},
		{Type: "line_to", Point: []float64{left, height / 2}},
		{Type: "close"},
	}
}

// ResizePremiumImageMask rebuilds catalog-authored wipe geometry after the
// processor fits an entity image to the verified source aspect ratio.
func ResizePremiumImageMask(layer *Layer) error {
	if layer == nil || !layer.PremiumWipeMask {
		return nil
	}
	if len(layer.Size) < 2 || layer.Size[0] <= 0 || layer.Size[1] <= 0 || len(layer.Masks) == 0 {
		return fmt.Errorf("overlay: image layer %q has no positive size for its premium wipe mask", layer.ID)
	}
	layer.Masks[0].Path = premiumWipeMaskPath(layer.Size[0], layer.Size[1], false)
	layer.Masks[0].TargetPath = premiumWipeMaskPath(layer.Size[0], layer.Size[1], true)
	return nil
}

func retimePremiumNativeTrack(track motion.TrackDefinition, duration, authoredEnter int64) *LayerPathAnimation {
	return premiumNativePathAnimation(track, duration, authoredEnter)
}

func premiumComponentTracks(definition motion.MotionDefinition, tracks []motion.TrackDefinition, duration int64) *LayerAnimation {
	if len(tracks) == 0 {
		return nil
	}
	converted := make([]AnimationTrack, 0, len(tracks))
	for _, track := range tracks {
		keys := make([]AnimationKeyframe, len(track.Keyframes))
		for i, key := range track.Keyframes {
			keys[i] = AnimationKeyframe{Frame: key.Frame, Value: key.Value}
		}
		converted = append(converted, AnimationTrack{Property: track.Property, Keyframes: keys, Easing: track.Easing})
	}
	entrance := entranceFrames(definition.Enter, 0, duration, false)
	return &LayerAnimation{Tracks: retimeMotionTracks(converted, entrance, int64(definition.Enter))}
}

func premiumLayerActive(layer *Layer, animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	layer.Enable3D = layerUses3D(animation)
	layer.Animation = animation
	return true
}

func imageRecipeEffectID(kind string) string {
	switch kind {
	case "glow":
		return "light.glow"
	case "bloom":
		return "light.bloom"
	case "drop_shadow":
		return "light.drop_shadow"
	case "gaussian_blur":
		return "blur.gaussian"
	default:
		return ""
	}
}

func premiumPathAnimation(track motion.TrackDefinition, duration, authoredEnter int64) *LayerPathAnimation {
	return premiumNativePathAnimation(track, duration, authoredEnter)
}

// SyncPremiumComponentTransform mirrors an image's transform onto a linked
// frame component while preserving the component's authored Z offset.
func SyncPremiumComponentTransform(image, component *Layer) {
	if image == nil || component == nil || !component.PremiumSyncTransform {
		return
	}
	component.Position = append([]float64(nil), image.Position...)
	if len(component.Position) > 2 {
		component.Position[2] += component.PremiumZOffset
	} else if image.Enable3D || component.PremiumZOffset != 0 {
		component.Position = append(component.Position, component.PremiumZOffset)
	}
	component.Enable3D = image.Enable3D || component.PremiumZOffset != 0
	component.Animation = premiumAnimationWithZOffset(image.Animation, component.PremiumZOffset)
}

// numericValue extracts a supported scalar from a keyframe or semantic parameter.
func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func premiumAnimationWithZOffset(source *LayerAnimation, zOffset float64) *LayerAnimation {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Tracks = append([]AnimationTrack(nil), source.Tracks...)
	for i := range clone.Tracks {
		clone.Tracks[i].Keyframes = append([]AnimationKeyframe(nil), source.Tracks[i].Keyframes...)
		if clone.Tracks[i].Property != "position_z" || zOffset == 0 {
			continue
		}
		for j := range clone.Tracks[i].Keyframes {
			if value, ok := numericValue(clone.Tracks[i].Keyframes[j].Value); ok {
				clone.Tracks[i].Keyframes[j].Value = value + zOffset
			}
		}
	}
	return &clone
}

func normalizedPremiumComponents(src *semanticPlan, image Layer, layers []Layer) []Layer {
	for i := range layers {
		layer := &layers[i]
		if layer.ID == image.ID || layer.PremiumParentImageID != image.ID {
			continue
		}
		if layer.PremiumCanvasSize {
			layer.Position = []float64{0, 0}
			layer.Size = []float64{float64(src.Width), float64(src.Height)}
			continue
		}
		if len(layer.Position) < 2 {
			layer.Position = []float64{0, 0}
		}
		if len(image.Position) >= 2 {
			layer.Position[0] = image.Position[0]
			layer.Position[1] = image.Position[1]
			if len(layer.PremiumPositionOffset) == 2 {
				layer.Position[0] += layer.PremiumPositionOffset[0]
				layer.Position[1] += layer.PremiumPositionOffset[1]
			}
		}
		if len(layer.PremiumSizeScale) == 2 && len(image.Size) >= 2 {
			layer.Size = []float64{image.Size[0] * layer.PremiumSizeScale[0], image.Size[1] * layer.PremiumSizeScale[1]}
		} else if len(layer.PremiumParentSize) == 2 {
			layer.Size = append([]float64(nil), layer.PremiumParentSize...)
		}
		if layer.PremiumPathKind == "rounded_rect" && layer.Shape != nil {
			radius := image.Radius * layer.PremiumRadiusScale
			if radius == 0 {
				radius = image.Radius
			}
			layer.Shape.Path = roundedRectPath(layer.Size[0], layer.Size[1], radius)
			if layer.PremiumShapeKind == "rounded_rect" {
				layer.Shape.Radius = radius
			}
		}
		if layer.PremiumSyncTransform {
			SyncPremiumComponentTransform(&image, layer)
		}
		if layer.PremiumZOffset != 0 && !layer.PremiumSyncTransform {
			layer.Enable3D = true
			if len(layer.Position) < 3 {
				layer.Position = append(layer.Position, layer.PremiumZOffset)
			} else {
				layer.Position[2] = layer.PremiumZOffset
			}
		}
	}
	return layers
}

func compilePremiumMask(image Layer, definition motion.ImageMaskDefinition, duration, authoredEnter int64) (LayerMask, error) {
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return LayerMask{}, fmt.Errorf("overlay: image layer %q has no positive size for its animated mask", image.ID)
	}
	w, h := image.Size[0], image.Size[1]
	from := premiumWipeMaskPath(w, h, false)
	to := premiumWipeMaskPath(w, h, true)
	animation := premiumPathAnimation(definition.Progress, duration, authoredEnter)
	mask := LayerMask{Type: "path", Mode: "add", Feather: definition.Feather,
		Path: from, TargetPath: to, Animation: animation}
	return mask, nil
}

func compilePremiumComponent(src *semanticPlan, image Layer, definition motion.MotionDefinition, component motion.ImageMotionComponent) (Layer, error) {
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("overlay: image layer %q has no positive geometry for component %q", image.ID, component.ID)
	}
	width, height := image.Size[0], image.Size[1]
	if component.CanvasSize {
		width, height = float64(src.Width), float64(src.Height)
	} else if len(component.SizeScale) == 2 {
		width *= component.SizeScale[0]
		height *= component.SizeScale[1]
	}
	position := []float64{float64(src.Width) / 2, float64(src.Height) / 2}
	if !component.CanvasSize && len(image.Position) >= 2 {
		position[0] += image.Position[0]
		position[1] += image.Position[1]
	}
	if len(component.PositionOffset) == 2 {
		position[0] += component.PositionOffset[0]
		position[1] += component.PositionOffset[1]
	}
	if component.ZOffset != 0 || component.SyncTransform && image.Enable3D {
		position = append(position, component.ZOffset)
	}
	sizeScale := append([]float64(nil), component.SizeScale...)
	if !component.CanvasSize && len(sizeScale) == 0 {
		sizeScale = []float64{1, 1}
	}
	layer := Layer{ID: image.ID + ":premium:" + component.ID, Type: component.Type,
		Size: []float64{width, height}, Position: position,
		StartFrame: image.StartFrame, DurationFrames: image.DurationFrames,
		PremiumParentImageID: image.ID, PremiumParentSize: []float64{width, height},
		PremiumSizeScale:      sizeScale,
		PremiumPositionOffset: append([]float64(nil), component.PositionOffset...),
		PremiumRadiusScale:    component.RadiusScale, PremiumZOffset: component.ZOffset,
		PremiumShapeKind: component.Shape, PremiumPathKind: component.PathKind,
		PremiumCanvasSize: component.CanvasSize, PremiumSyncTransform: component.SyncTransform}
	if component.Type == "color" {
		layer.Color = append([]float64(nil), component.Fill...)
	}
	if component.Type == "shape" {
		shapeType := component.Shape
		if shapeType == "" {
			shapeType = "rounded_rect"
		}
		var fill any
		if len(component.Fill) > 0 {
			fill = component.Fill
		} else if shapeType == "path" {
			// Recipe paths are commonly decorative outlines. The render-plan
			// decoder defaults an omitted path fill to opaque white, which makes
			// stroke-only paths fall out of the native Vulkan path-stroke lane.
			fill = []float64{0, 0, 0, 0}
		}
		if component.Gradient != nil {
			fill = nil
		}
		shape := &LayerShape{Type: shapeType, Fill: fill}
		if component.Gradient != nil {
			gradient := &LayerGradient{Type: component.Gradient.Type,
				Start: append([]float64(nil), component.Gradient.Start...),
				End:   append([]float64(nil), component.Gradient.End...)}
			for _, stop := range component.Gradient.Stops {
				gradient.ColorStops = append(gradient.ColorStops, LayerGradientStop{
					Position: stop.Position, Color: append([]float64(nil), stop.Color...)})
			}
			shape.Fill = gradient
		}
		switch shapeType {
		case "rounded_rect":
			shape.Radius = image.Radius * component.RadiusScale
			if shape.Radius <= 0 {
				shape.Radius = image.Radius
			}
		case "path":
			switch component.PathKind {
			case "arrow", "scribble", "check", "ellipse", "corner_marks", "cross", "double_line", "wave", "underline", "underline_double", "underline_wave":
				shape.Path = brushPath(component.PathKind, width, height)
			case "rounded_rect":
				radius := image.Radius * component.RadiusScale
				if radius == 0 {
					radius = image.Radius
				}
				shape.Path = roundedRectPath(width, height, radius)
			case "line":
				shape.Path = []LayerPathCommand{
					{Type: "move_to", Point: []float64{-width / 2, 0}},
					{Type: "line_to", Point: []float64{width / 2, 0}},
				}
			default:
				return Layer{}, fmt.Errorf("overlay: motion %q component %q has unsupported path_kind %q", definition.ID, component.ID, component.PathKind)
			}
		}
		if component.Stroke != nil {
			shape.Stroke = &LayerStroke{Color: component.Stroke.Color, Width: component.Stroke.Width}
		}
		if component.Trim != nil {
			shape.Operators = []LayerPathOperator{{Kind: "trim", Params: LayerTrimParams{
				Start: component.Trim.Start, End: component.Trim.End,
				Animation: premiumPathAnimation(component.Trim.Animation, image.DurationFrames, int64(definition.Enter))}}}
		}
		layer.Shape = shape
	}
	if component.Opacity != nil {
		opacity := *component.Opacity
		layer.Opacity = &opacity
	}
	if component.SyncTransform {
		SyncPremiumComponentTransform(&image, &layer)
	} else {
		premiumLayerActive(&layer, premiumComponentTracks(definition, component.Tracks, image.DurationFrames))
	}
	if component.ZOffset != 0 {
		layer.Enable3D = true
		if len(layer.Position) < 3 {
			layer.Position = append(layer.Position, component.ZOffset)
		} else {
			layer.Position[2] = component.ZOffset
		}
	}
	if component.CanvasSize {
		layer.Position = []float64{0, 0}
		layer.Size = []float64{float64(src.Width), float64(src.Height)}
		layer.Color = append([]float64(nil), component.Fill...)
		layer.Opacity = nil
	}
	for _, effect := range component.Effects {
		params := make(map[string]any, len(effect.Params))
		for key, value := range effect.Params {
			params[key] = value
		}
		layer.Effects = append(layer.Effects, LayerEffect{Type: effect.Type, Params: params})
	}

	for _, track := range component.EffectParamTracks {
		animation := retimePremiumNativeTrack(track.Track, image.DurationFrames, int64(definition.Enter))
		layer.EffectParamTracks = append(layer.EffectParamTracks, LayerEffectParamTrack{
			EffectID: track.EffectID, Param: track.Param, Keyframes: animation.Keyframes, Easing: animation.Easing})
	}
	if len(component.EffectParamTracks) > 0 {
		// Built-in shorthand effects are the native (and more portable) form
		// for effect params; apply each parameter's authored initial sample to
		// the shorthand effect and keep its V3 param track beside it.
		for i := range layer.Effects {
			for _, track := range component.EffectParamTracks {
				if imageRecipeEffectID(layer.Effects[i].Type) != track.EffectID || len(track.Track.Keyframes) == 0 {
					continue
				}
				if layer.Effects[i].Params == nil {
					layer.Effects[i].Params = make(map[string]any)
				}
				layer.Effects[i].Params[track.Param] = track.Track.Keyframes[0].Value
			}
		}
	}
	return layer, nil
}

func compilePremiumComponents(src *semanticPlan, image Layer, definition motion.MotionDefinition) ([]Layer, error) {
	var before, after []Layer
	for _, component := range definition.ImageRecipe.Components {
		layer, err := compilePremiumComponent(src, image, definition, component)
		if err != nil {
			return nil, err
		}
		if component.Placement == "before_image" {
			before = append(before, layer)
		} else {
			after = append(after, layer)
		}
	}
	out := make([]Layer, 0, len(before)+1+len(after))
	out = append(out, before...)
	out = append(out, image)
	out = append(out, after...)
	return normalizedPremiumComponents(src, image, out), nil
}

func premiumImageLayerIndex(layers []Layer) int {
	for i := range layers {
		if layers[i].Type == "image" {
			return i
		}
	}
	return 0
}

func validatePremiumImageDuration(definition motion.MotionDefinition, duration int64) error {
	bounds := definition.DurationBounds
	if bounds == nil || duration < bounds.MinimumFrames || duration > bounds.MaximumFrames {
		if bounds == nil {
			return fmt.Errorf("overlay: motion %q has no duration bounds", definition.ID)
		}
		return fmt.Errorf("overlay: motion %q requires %d..%d frames, got %d", definition.ID, bounds.MinimumFrames, bounds.MaximumFrames, duration)
	}
	return nil
}

func compilePremiumImageLayers(ri resolvedItem, src *semanticPlan, image Layer, definition *motion.MotionDefinition) ([]Layer, error) {
	if definition == nil {
		return []Layer{image}, nil
	}
	if err := validatePremiumImageDuration(*definition, image.DurationFrames); err != nil {
		return nil, err
	}
	recipe := definition.ImageRecipe
	if recipe.Stack {
		return nil, fmt.Errorf("overlay: motion %q requires multiple image layers", definition.ID)
	}
	if recipe.RequireCaption && strings.TrimSpace(ri.Item.EntityCaption) == "" {
		captionPresent := false
		for _, child := range ri.Item.ImageLayers {
			captionPresent = captionPresent || strings.TrimSpace(child.Caption) != ""
		}
		if !captionPresent {
			return nil, fmt.Errorf("overlay: motion %q requires a caption", definition.ID)
		}
	}
	if recipe.Mask != nil {
		mask, err := compilePremiumMask(image, *recipe.Mask, image.DurationFrames, int64(definition.Enter))
		if err != nil {
			return nil, err
		}
		image.Masks = append(image.Masks, mask)
		image.PremiumWipeMask = true
	}
	return compilePremiumComponents(src, image, *definition)
}

func compilePremiumImageStack(ri resolvedItem, src *semanticPlan, registry *assetRegistry, definition *motion.MotionDefinition) ([]Layer, error) {
	if len(ri.Item.ImageLayers) < 2 {
		return nil, fmt.Errorf("overlay: motion %q requires at least two image_layers", definition.ID)
	}
	activeID, err := premiumStackSelection(ri.Item.MotionParams, definition.ImageRecipe.ActiveLayerParam)
	if err != nil {
		return nil, err
	}
	assets := make(map[string]string, len(ri.Item.Assets))
	for _, ref := range ri.Item.Assets {
		assets[ref.ID] = registry.Path(ref.ID)
	}
	var output []Layer
	activeFound := false
	for _, child := range ri.Item.ImageLayers {
		assetPath, ok := assets[child.AssetID]
		if !ok {
			return nil, fmt.Errorf("overlay: stacked image %q references undeclared asset %q", child.ID, child.AssetID)
		}
		preset, err := resolveOfficialPreset(child.PresetID, string(PresetImage))
		if err != nil {
			return nil, err
		}
		startOffset, endOffset := msFrames(child.StartMS, child.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		childItem := ri.Item
		childItem.ID = ri.Item.ID + ":" + child.ID
		childItem.PresetID = child.PresetID
		childItem.MotionID = ""
		childItem.MotionParams = nil
		childItem.EntityCaption = ""
		childItem.CaptionMotionID = child.CaptionMotionID
		childItem.Frame = child.Frame
		childItem.Params = child.Params
		childResolved := ri
		childResolved.Item, childResolved.Params = childItem, child.Params
		childResolved.Start, childResolved.End = ri.Start+startOffset, ri.Start+endOffset
		image, err := compileSingleImageLayer(childResolved, src, assetPath, preset, "", nil, childResolved.Start, childResolved.End, childResolved.Item.ID)
		if err != nil {
			return nil, err
		}
		if err := validatePremiumImageDuration(*definition, image.DurationFrames); err != nil {
			return nil, err
		}
		if child.ID == activeID {
			activeFound = true
			premiumLayerActive(&image, premiumTracks(*definition, definition.Tracks, image.DurationFrames))
		} else {
			premiumLayerActive(&image, premiumTracks(*definition, definition.ImageRecipe.InactiveTracks, image.DurationFrames))
		}
		decorated := []Layer{image}
		if child.ID == activeID {
			decorated, err = compilePremiumComponents(src, image, *definition)
			if err != nil {
				return nil, err
			}
			decorated = normalizedPremiumComponents(src, image, decorated)
		}
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			captionLayer, err := compileEntityCaptionLayer(ri, src, childItem, caption, &decorated[premiumImageLayerIndex(decorated)], child.ID)
			if err != nil {
				return nil, err
			}
			if len(definition.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*definition, definition.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			decorated = append(decorated, captionLayer)
		}
		output = append(output, decorated...)
	}
	if !activeFound {
		return nil, fmt.Errorf("overlay: motion %q active_layer_id %q does not name an image_layers[].id", definition.ID, activeID)
	}
	return output, nil
}

func premiumStackSelection(params map[string]any, name string) (string, error) {
	value, ok := params[name].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("overlay: image_stack_focus requires motion_params.%s", name)
	}
	return strings.TrimSpace(value), nil
}
