// semantic_image_compile.go owns the image dispatcher: it routes a resolved
// image item through the recipe registry (single, premium multi-image,
// visual accents) and the shared single-layer lowering. Admission and
// runtime-effect validation live in this file; caption lowering in
// semantic_image_effects.go.

package overlay

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

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

// imageRecipeSelection is the validated recipe set one image item lowers
// through. At most one of group/premium/visualAccents is non-nil: they are
// distinct motion families, and resolveImageRecipes has already rejected the
// combinations the contract does not allow.
type imageRecipeSelection struct {
	resolvedMotion *resolvedMotion
	group          *motion.MotionDefinition
	premium        *motion.MotionDefinition
	visualAccents  *motion.MotionDefinition
}

// compileImageItemCore is the pure recipe/motion lowering of one image item.
// The runtime effect decoration is applied by the compileImageItem wrapper in
// semantic_image_effects.go; keep this function effect-stack agnostic.
//
// The dispatcher owns no lowering of its own: it validates the recipe
// selection once, then hands the item to its one choreography — standalone
// (single asset, light leak), or multi-card (group recipe, premium stack,
// sealed legacy replay).
func compileImageItemCore(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	recipes, err := resolveImageRecipes(ri)
	if err != nil {
		return nil, err
	}
	if len(ri.Item.ImageLayers) == 0 {
		return compileStandaloneImageItem(ri, src, registry, recipes)
	}
	return compileMultiImageItem(ri, src, registry, recipes)
}

// resolveImageRecipes resolves the item's registered motion and validates
// target admission once, before any layer is built. Every check here is
// item-level: it answers which lowering the payload asks for, not how a layer
// looks.
func resolveImageRecipes(ri resolvedItem) (imageRecipeSelection, error) {
	var selection imageRecipeSelection
	// Item-level controls apply only to the standalone image or to a catalog
	// recipe that coordinates multiple image layers. Ordinary composite children own their
	// independent motion_params and are validated in compileSingleImageLayer.
	if len(ri.Item.ImageLayers) > 0 {
		if _, _, err := motionWindows(ri.Item.MotionParams, ri.Preset.Motion.Exit); err != nil {
			return selection, err
		}
	}
	resolvedImageMotion, imageRecipe, err := resolveImageRecipeMotion(ri.Item.MotionID, true)
	if err != nil {
		return selection, err
	}
	selection.resolvedMotion = resolvedImageMotion
	groupRecipe := imageGroupRecipeDefinition(imageRecipe)
	if groupRecipe != nil && len(ri.Item.ImageLayers) == 0 && isReusableImageGroupRecipe(groupRecipe) {
		if !motionDefinitionAdmitsTarget(groupRecipe, TargetImage) {
			return selection, fmt.Errorf("overlay: motion %q is not supported for a single image", groupRecipe.ID)
		}
		// The same authored tracks serve a one-image item directly and are
		// reused from group_recipe for multiple-image compositions.
		groupRecipe = nil
		imageRecipe = nil
	}
	if groupRecipe != nil && (len(ri.Item.ImageLayers) == 0 || !isImageKind(ri.Kind)) {
		return selection, fmt.Errorf("overlay: group motion %q requires an image item with image_layers", groupRecipe.ID)
	}
	premium := premiumImageRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(imageRecipe) && premium == nil {
		return selection, fmt.Errorf("overlay: multi-image motion %q in category %q has no recipe lowering", ri.Item.MotionID, imageRecipe.Category)
	}
	visualAccents := visualAccentsRecipeDefinition(imageRecipe)
	if isMultiImageRecipe(visualAccents) {
		return selection, fmt.Errorf("overlay: motion %q requires multiple image_layers", visualAccents.ID)
	}
	if visualAccents != nil && visualAccents.GroupRecipe != nil {
		return selection, fmt.Errorf("overlay: visual accents cannot be used as image group recipes")
	}
	selection.group = groupRecipe
	selection.premium = premium
	selection.visualAccents = visualAccents
	return selection, nil
}

// compileStandaloneImageItem lowers the one-card choreographies: an assetless
// light leak, or a single asset with optional visual accents and caption.
func compileStandaloneImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry, recipes imageRecipeSelection) ([]Layer, error) {
	if ri.Kind == KindLightLeak && len(ri.Item.Assets) == 0 {
		return compileAssetlessLightLeak(ri, src, recipes.visualAccents)
	}
	if len(ri.Item.Assets) == 0 {
		return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
	}
	if isMultiImageRecipe(recipes.premium) {
		return nil, fmt.Errorf("overlay: motion %q requires at least two image_layers and motion_params.active_layer_id", recipes.premium.ID)
	}
	layer, err := compileSingleImageLayerResolved(ri, src, registry.Path(ri.Item.Assets[0].ID), ri.Preset, ri.Item.MotionID, ri.Item.MotionParams, ri.Start, ri.End, ri.Item.ID, recipes.resolvedMotion)
	if err != nil {
		return nil, err
	}
	if recipes.visualAccents != nil {
		layers, err := compileVisualAccentsImageLayers(ri, src, layer, recipes.visualAccents)
		if err != nil {
			return nil, err
		}
		return appendEntityCaption(ri, src, layers, nil)
	}
	layers, err := compilePremiumImageLayers(ri, src, layer, recipes.premium)
	if err != nil {
		return nil, err
	}
	return appendEntityCaption(ri, src, layers, recipes.premium)
}

// appendEntityCaption attaches the item's entity caption to an already-built
// image stack and applies the premium caption tracks. A premium recipe that
// requires a caption fails closed when the payload omits one; the visual
// accents path passes a nil premium because it has no caption contract.
func appendEntityCaption(ri resolvedItem, src *semanticPlan, layers []Layer, premium *motion.MotionDefinition) ([]Layer, error) {
	captionLayer, err := entityCaptionLayer(ri, src, layers)
	if err != nil {
		return nil, err
	}
	if captionLayer == nil {
		if premium != nil && premium.ImageRecipe.RequireCaption {
			return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
		}
		return layers, nil
	}
	if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
		premiumLayerActive(captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
	}
	return append(layers, *captionLayer), nil
}

// compileAssetlessLightLeak builds the transparent full-canvas surface a
// light_leak_v1 recipe decorates, then drops the surface itself: the item is
// the glow, not a frame.
func compileAssetlessLightLeak(ri resolvedItem, src *semanticPlan, visualAccents *motion.MotionDefinition) ([]Layer, error) {
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

// compileMultiImageItem lowers the card stacks: a certified group recipe, a
// premium multi-image recipe, or the sealed legacy replay when the plan
// carries its explicit marker.
func compileMultiImageItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry, recipes imageRecipeSelection) ([]Layer, error) {
	if recipes.group != nil {
		if !recipes.group.GroupRecipe.SupportsCount(len(ri.Item.ImageLayers)) {
			return nil, fmt.Errorf("overlay: motion %q does not support %d image layers", recipes.group.ID, len(ri.Item.ImageLayers))
		}
		return compileImageGroupRecipe(ri, src, registry, recipes.group)
	}
	if isMultiImageRecipe(recipes.premium) {
		if !motionDefinitionAdmitsTarget(recipes.premium, TargetImage) {
			return nil, fmt.Errorf("overlay: item %q motion %q is not supported for target %q", ri.Item.ID, ri.Item.MotionID, TargetImage)
		}
		return compilePremiumMultiImageRecipe(ri, src, registry, recipes.premium)
	}
	if recipes.premium != nil && recipes.premium.ImageRecipe.RequireCaption {
		for _, child := range ri.Item.ImageLayers {
			if strings.TrimSpace(child.Caption) == "" {
				return nil, fmt.Errorf("overlay: motion %q requires a caption on every image layer", recipes.premium.ID)
			}
		}
	}
	// A new plan always names one group choreography. Reaching this point with
	// that field set means the certified recipe is missing: refuse the plan
	// instead of silently lowering children through the legacy per-child path
	// (which would either animate a second owner or render a static group).
	if strings.TrimSpace(ri.Item.GroupMotionID) != "" {
		return nil, fmt.Errorf("overlay: item %q group_motion_id %q has no certified group recipe; legacy per-child replay refused", ri.Item.ID, ri.Item.GroupMotionID)
	}
	// Sealed replay path: a pre-group_motion_id plan keeps its per-child
	// motion_id only when it carries the explicit replay marker. A new plan
	// that forgot the group recipe (or a producer that regressed) fails closed
	// instead of silently taking a second animation path.
	if !src.LegacyCompositeReplay {
		return nil, fmt.Errorf("overlay: item %q has image_layers without group_motion_id; new plans must declare a certified group recipe (sealed plans replay only with legacy_composite_replay=true)", ri.Item.ID)
	}
	return compileLegacyCompositeImageLayers(ri, src, registry)
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

// compileLegacyCompositeImageLayers is the replay path for sealed plans that
// predate group_motion_id. It is a thin wrapper over the shared member
// lowering plus the legacy child-grid rule, and it is reachable ONLY behind
// legacy_composite_replay. New producer plans use compileImageGroupRecipe
// through the parent group_motion_id and entity groups use the same member
// lowering directly — neither needs this replay entry point.
func compileLegacyCompositeImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	_, imageRecipe, err := resolveImageRecipeMotion(ri.Item.MotionID, false)
	if err != nil {
		return nil, err
	}
	layers, gridSource, err := compileCompositeImageMembers(ri, src, registry, premiumImageRecipeDefinition(imageRecipe))
	if err != nil {
		return nil, err
	}
	if _, itemGrid := ri.Params["grid"]; !itemGrid && gridSource != nil {
		return addStandaloneImageGrid(*gridSource, src, layers)
	}
	return layers, nil
}

// compileCompositeImageMembers lowers every image_layers member through the
// shared single-layer path with its own motion, preset and caption. It is the
// modern multi-card primitive: both the sealed legacy replay and entity_group
// call it, and neither re-implements per-child lowering. The caller owns the
// item-level grid decision (returned as gridSource) because an item-level
// params.grid must win over a child-declared one exactly once.
func compileCompositeImageMembers(ri resolvedItem, src *semanticPlan, registry *assetRegistry, premium *motion.MotionDefinition) ([]Layer, *resolvedItem, error) {
	assets := resolveAssetPaths(registry, ri.Item.Assets)

	layers := make([]Layer, 0, len(ri.Item.ImageLayers)*2)
	// The grid is a full-canvas screen-space layer bound to the item lifetime,
	// so a composite must draw exactly one copy no matter how many children
	// declare it. Children are collected here and lowered once after the loop.
	var gridSource *resolvedItem
	for index, child := range ri.Item.ImageLayers {
		childResolved, assetPath, preset, err := resolveImageChild(ri, assets, child, src, imageChildOptions{index: index})
		if err != nil {
			return nil, nil, err
		}
		params := childResolved.Params
		imageStart, imageEnd := childResolved.Start, childResolved.End
		layer, err := compileSingleImageLayer(childResolved, src, assetPath, preset, childResolved.Item.MotionID, childResolved.Item.MotionParams, imageStart, imageEnd, childResolved.Item.ID)
		if err != nil {
			return nil, nil, err
		}
		decorated, err := compilePremiumImageLayers(childResolved, src, layer, premium)
		if err != nil {
			return nil, nil, err
		}
		if _, hasChildEffects := params["effects"]; hasChildEffects {
			decorated, err = applyImageItemEffects(childResolved, decorated)
			if err != nil {
				return nil, nil, err
			}
		}
		if _, hasChildGrid := params["grid"]; hasChildGrid && gridSource == nil {
			gridSource = &childResolved
		}
		layers = append(layers, decorated...)
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			imageIndex := premiumImageLayerIndex(decorated)
			captionLayer, err := compileImageCaptionLayer(ri, src, childResolved.Item, caption, &decorated[imageIndex], child.ID, textRoleImageCaption)
			if err != nil {
				return nil, nil, err
			}
			if child.CaptionEndMS > 0 {
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
	// One grid per item: the first child that asked is returned to the caller,
	// which lowers it only when the item itself declares no params.grid. This
	// keeps a multi-card item from stacking one grid per member.
	return layers, gridSource, nil
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
	if motionID == "" && preset.ID != "" {
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
	// Sized compile: resolve the source-derived geometry now. This is the same
	// point in the pipeline the historical post-compile fit mutated (after the
	// frame treatment), but it happens BEFORE the caller builds premium
	// components and captions, so those are derived from the final size. A
	// blind compile keeps the transient markers untouched.
	if err := applyProbedImageGeometry(&layer, src.assetSizes); err != nil {
		return Layer{}, err
	}
	return layer, nil
}

// runtimeDefaultImageMotionIDs is the canonical-catalog projection used when
// an image omits motion_id. Keep this automatic pool to the single-image
// families; recipe families (premium accents, stacks, captions) remain explicit
// choices because they require extra geometry or assets. The camera-pan family
// is the one group recipe admitted here: all four directions support a single
// image and settle at the neutral pose. IDs are sorted so selection is stable
// across builds.
var runtimeDefaultImageMotionIDs = func() []string {
	var ids []string
	for _, family := range []string{"overlay_v3_image", "image_25d_clean_v1", "editorial_image_v1"} {
		ids = append(ids, motion.Registry.SelectableCategoryMotionIDs(family)...)
	}
	ids = append(ids, cameraPanMotionIDs...)
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
// choice. Explicit motion_id and animation groups are handled before this
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
// the source aspect ratio. The CALLER supplies the decoded source dimensions:
// the processor reads them from the verified, content-addressed bytes after
// materialization, so this lowering path never opens a file.
func FitEntityImageLayerToAsset(layer *Layer, sourceWidth, sourceHeight int) error {
	if layer == nil || layer.BoxWidth <= 0 || layer.BoxHeight <= 0 {
		return fmt.Errorf("missing image geometry")
	}
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return fmt.Errorf("invalid source dimensions %dx%d", sourceWidth, sourceHeight)
	}
	maxW, maxH := layer.BoxWidth, layer.BoxHeight
	if int64(sourceWidth)*int64(maxH) > int64(sourceHeight)*int64(maxW) {
		layer.BoxWidth = maxW
		layer.BoxHeight = max(1, int(float64(maxW)*float64(sourceHeight)/float64(sourceWidth)+0.5))
	} else {
		layer.BoxHeight = maxH
		layer.BoxWidth = max(1, int(float64(maxH)*float64(sourceWidth)/float64(sourceHeight)+0.5))
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
	// The box now has the source's aspect ratio, so cover fills the complete
	// surface without cropping meaningful image content or leaving a dark
	// contain matte around the texture.
	layer.Fit = FitCover
	return nil
}

// The admission half of the image subsystem:
// composition cardinality, layer timing, caption policy and multi-asset
// rules, validated before any lowering happens.
// compileImageItem lowers one semantic image item. With no image_layers it uses
// the single-image path (one asset, item-level motion_id/motion_params); with
// image_layers it compiles each child independently (its own asset, time,
// preset, motion_id and motion_params). Multi-image recipes are a separate
// catalog-defined mode that coordinates the image layers as one motion.
// validateSemanticImageLayers uses the kind already resolved for this item so
// composition checks cannot reinterpret its template independently.
func validateSemanticImageLayers(item semanticItem, kind ItemKind) error {
	// Chain items carry one merged asset list for N segments and no
	// image_layers; each segment is validated against its own asset at
	// lowering time (see chain_compile.go), so the single/multi-asset rules
	// below do not apply to them.
	if isChainKind(kind) {
		return nil
	}
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
	// Entity groups reuse the composite member rules verbatim: members are
	// validated here, slot geometry is applied at lowering. The 2..5 group
	// cardinality itself is enforced by compileEntityGroup.
	if behavior, layers, assets := behaviorOf(kind), len(item.ImageLayers), len(item.Assets); (behavior != behaviorImage && behavior != behaviorEntity && behavior != behaviorGroup) || layers < 2 || assets < 2 {
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
	if composition.ImageCountRange != nil {
		if imageCount < composition.ImageCountRange.Minimum || imageCount > composition.ImageCountRange.Maximum {
			return fmt.Errorf("overlay: item %q composition_id %q requires between %d and %d images, got %d", item.ID, item.CompositionID, composition.ImageCountRange.Minimum, composition.ImageCountRange.Maximum, imageCount)
		}
	} else if imageCount != composition.ImageCount {
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
