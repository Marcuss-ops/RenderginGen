// Package motion owns RenderingGen's renderer-neutral motion plugins.
// Motion IDs never cross into Chronon; text plugins lower to generic
// TextAnimator definitions (selector + property tracks).
package motion

import (
	"encoding/json"
	"fmt"
	"math"
)

type Params map[string]any

type MotionParams = Params

type MotionContext struct {
	Target         string
	Text           string
	DurationFrames int64
	CanvasWidth    int
	CanvasHeight   int
}

type AnimationTrack struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type AnimationKeyframe struct {
	Frame int64 `json:"frame"`
	Value any   `json:"value"`
}

type TrackDefinition struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type SelectorDefinition struct {
	Kind       string   `json:"kind,omitempty"`  // glyph, grapheme, character, word, line
	Shape      string   `json:"shape,omitempty"` // square, ramp_up, ramp_down, triangle, round, smooth
	Order      string   `json:"order,omitempty"` // forward, reverse, from_center, to_center, random
	Stagger    int64    `json:"stagger,omitempty"`
	RangeStart *float64 `json:"range_start,omitempty"`
	RangeEnd   *float64 `json:"range_end,omitempty"`
}

type TextAnimatorDefinition struct {
	ID         string             `json:"id,omitempty"`
	Selector   SelectorDefinition `json:"selector"`
	Properties []TrackDefinition  `json:"properties"`
}

type StaggerDefinition struct {
	Frames int64 `json:"frames,omitempty"`
}

type DurationBounds struct {
	MinimumFrames int64 `json:"minimum_frames"`
	MaximumFrames int64 `json:"maximum_frames"`
}

// ImageMotionRecipe describes renderer-neutral additions around one image
// layer. Its components lower to ordinary Chronon image/shape/color layers;
// masks, path operators and effect parameter tracks remain native render-plan
// primitives rather than a second motion renderer.
type ImageMotionRecipe struct {
	RequireCaption   bool                   `json:"require_caption,omitempty"`
	Stack            bool                   `json:"stack,omitempty"`
	ActiveLayerParam string                 `json:"active_layer_param,omitempty"`
	InactiveTracks   []TrackDefinition      `json:"inactive_tracks,omitempty"`
	CaptionTracks    []TrackDefinition      `json:"caption_tracks,omitempty"`
	Mask             *ImageMaskDefinition   `json:"mask,omitempty"`
	Components       []ImageMotionComponent `json:"components,omitempty"`
}

type ImageMotionComponent struct {
	ID                string                       `json:"id"`
	Type              string                       `json:"type,omitempty"`
	Shape             string                       `json:"shape,omitempty"`
	PathKind          string                       `json:"path_kind,omitempty"`
	Placement         string                       `json:"placement,omitempty"`
	CanvasSize        bool                         `json:"canvas_size,omitempty"`
	SyncTransform     bool                         `json:"sync_transform,omitempty"`
	SizeScale         []float64                    `json:"size_scale,omitempty"`
	PositionOffset    []float64                    `json:"position_offset,omitempty"`
	ZOffset           float64                      `json:"z_offset,omitempty"`
	RadiusScale       float64                      `json:"radius_scale,omitempty"`
	Fill              []float64                    `json:"fill,omitempty"`
	Gradient          *ImageMotionGradient         `json:"gradient,omitempty"`
	Stroke            *ImageMotionStroke           `json:"stroke,omitempty"`
	Opacity           *float64                     `json:"opacity,omitempty"`
	Tracks            []TrackDefinition            `json:"tracks,omitempty"`
	Effects           []ImageMotionEffect          `json:"effects,omitempty"`
	EffectParamTracks []EffectParamTrackDefinition `json:"effect_param_tracks,omitempty"`
	Trim              *ImageMotionTrim             `json:"trim,omitempty"`
}

type ImageMotionGradient struct {
	Type  string                    `json:"type"`
	Stops []ImageMotionGradientStop `json:"color_stops"`
	Start []float64                 `json:"start,omitempty"`
	End   []float64                 `json:"end,omitempty"`
}

type ImageMotionGradientStop struct {
	Position float64   `json:"position"`
	Color    []float64 `json:"color"`
}

type ImageMotionStroke struct {
	Color string  `json:"color"`
	Width float64 `json:"width"`
}

// ImageMotionEffect mirrors Chronon's flattened built-in effect objects, e.g.
// {"type":"glow","radius":12,"intensity":0.2,"color":[...]}. The extra
// properties stay in Params so the recipe can lower catalog-native effects
// without hard-coding a parallel effect schema here.
type ImageMotionEffect struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"-"`
}

func (e *ImageMotionEffect) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if value, ok := fields["type"].(string); ok {
		e.Type = value
	} else {
		return fmt.Errorf("image motion effect requires a string type")
	}
	delete(fields, "type")
	e.Params = fields
	return nil
}

type EffectParamTrackDefinition struct {
	EffectID string          `json:"effect_id"`
	Param    string          `json:"param"`
	Track    TrackDefinition `json:"track"`
}

type ImageMotionTrim struct {
	Start     float64         `json:"start"`
	End       float64         `json:"end"`
	Animation TrackDefinition `json:"animation"`
}

type ImageMaskDefinition struct {
	Kind     string          `json:"kind"`
	Feather  float64         `json:"feather,omitempty"`
	Progress TrackDefinition `json:"progress"`
	// Generator/Seed/Field carry the paint-family field mask. Kind "field"
	// lowers to Chronon's native Field2D mask stack entry; Kind "wipe_left"
	// (the image_premium_v1 recipe) keeps using the morphing path mask.
	Generator string                      `json:"generator,omitempty"`
	Seed      uint32                      `json:"seed,omitempty"`
	Field     *ImageMotionFieldDefinition `json:"field,omitempty"`
}

// ImageMotionFieldDefinition mirrors the renderer's Field2D plan: a generator
// vocabulary, a seed, and a bounded scalar operator chain applied in order.
type ImageMotionFieldDefinition struct {
	Generator string                     `json:"generator"`
	Seed      uint32                     `json:"seed,omitempty"`
	Frequency float64                    `json:"frequency"`
	Octaves   uint32                     `json:"octaves,omitempty"`
	Operators []ImageMotionFieldOperator `json:"operators,omitempty"`
}

// ImageMotionFieldOperator is one bounded scalar operator in a field chain.
// The Params stay open so the lowering can pass renderer-native operator
// parameters (edge, softness, amount, …) without a second schema here.
type ImageMotionFieldOperator struct {
	Kind   string         `json:"kind"`
	Params map[string]any `json:"-"`
}

func (o *ImageMotionFieldOperator) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if value, ok := fields["kind"].(string); ok {
		o.Kind = value
	} else {
		return fmt.Errorf("field operator requires a string kind")
	}
	delete(fields, "kind")
	o.Params = fields
	return nil
}

// MotionDefinition is the declarative motion contract. ID is the ONLY
// identity: the historical `Name` alias (read as a fallback by the registry,
// the overlay resolver and the preset catalog) has been removed, because two
// fields that can each name the same motion let one producer set only the
// other and silently fail to resolve at render time.
type MotionDefinition struct {
	ID                 string                   `json:"id"`
	Category           string                   `json:"category,omitempty"`
	SupportedTemplate  string                   `json:"supported_template,omitempty"`
	Seeded             bool                     `json:"seeded,omitempty"`
	Targets            []string                 `json:"targets,omitempty"`
	Unit               string                   `json:"unit,omitempty"`
	SupportedContent   []string                 `json:"supported_content,omitempty"`
	DurationBounds     *DurationBounds          `json:"duration_bounds,omitempty"`
	RequiredProperties []string                 `json:"required_properties,omitempty"`
	RenderSafe         *bool                    `json:"render_safe,omitempty"`
	Requires3D         *bool                    `json:"requires_3d,omitempty"`
	RequiresCamera     *bool                    `json:"requires_camera,omitempty"`
	Enter              int                      `json:"enter,omitempty"` // legacy preset timing
	Exit               int                      `json:"exit,omitempty"`
	Tracks             []TrackDefinition        `json:"tracks,omitempty"`
	TextAnimators      []TextAnimatorDefinition `json:"text_animators,omitempty"`
	Selector           SelectorDefinition       `json:"selector,omitempty"`
	Stagger            StaggerDefinition        `json:"stagger,omitempty"`
	ImageRecipe        *ImageMotionRecipe       `json:"image_recipe,omitempty"`
}

type MotionPlugin interface {
	ID() string
	Validate(params MotionParams) error
	Compile(ctx MotionContext, params MotionParams) ([]AnimationTrack, error)
}

// TextMotionPlugin is optional: layer/image plugins keep using MotionPlugin,
// while text plugins can lower selectors and per-glyph properties as well.
type TextMotionPlugin interface {
	MotionPlugin
	CompileText(ctx MotionContext, params MotionParams) ([]TextAnimatorDefinition, error)
}

func ValidateDefinition(d MotionDefinition) error {
	if d.ID == "" {
		return fmt.Errorf("motion: definition has no id")
	}
	if err := validateImagePremiumV1(d); err != nil {
		return err
	}
	if err := validateEditorialV1Family(d); err != nil {
		return err
	}
	if err := validateVisualAccentsV1Family(d); err != nil {
		return err
	}
	if err := validateEntityPresentationMotion(d); err != nil {
		return err
	}
	if d.Category == "web" {
		if d.RenderSafe == nil || !*d.RenderSafe || d.Requires3D == nil || d.RequiresCamera == nil {
			return fmt.Errorf("motion %q: web catalog metadata must declare render_safe, requires_3d and requires_camera", d.ID)
		}
		if d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames {
			return fmt.Errorf("motion %q: web duration_bounds are invalid", d.ID)
		}
		properties := make(map[string]bool)
		for _, track := range d.Tracks {
			properties[track.Property] = true
		}
		for _, required := range d.RequiredProperties {
			if !properties[required] {
				return fmt.Errorf("motion %q: required property %q has no layer track", d.ID, required)
			}
		}
		has3D := false
		for property := range properties {
			has3D = has3D || IsCameraBacked3DProperty(property)
		}
		if *d.Requires3D != has3D || *d.RequiresCamera != has3D {
			return fmt.Errorf("motion %q: 3D/camera metadata does not match its camera-backed properties", d.ID)
		}
		if len(d.Targets) == 0 || len(d.SupportedContent) == 0 || len(d.RequiredProperties) == 0 {
			return fmt.Errorf("motion %q: web catalog metadata is incomplete", d.ID)
		}
	}
	for _, t := range d.Tracks {
		if err := validateTrackDefinition(d.ID, "track", t); err != nil {
			return err
		}
	}
	for _, animator := range d.TextAnimators {
		if len(animator.Properties) == 0 {
			return fmt.Errorf("motion %q: text animator %q has no properties", d.ID, animator.ID)
		}
		for _, t := range animator.Properties {
			if err := validateTrackDefinition(d.ID, "text animator "+animator.ID, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateTrackDefinition protects the renderer boundary, where an empty or
// malformed track otherwise becomes a late render-plan failure. All authored
// tracks are layer-relative, so they must begin at frame zero and use strictly
// increasing frame numbers. A separate pack-level gate checks that the
// authored value curves are actually varying where a reveal is expected; this
// structural check keeps malformed timelines from reaching the renderer.
// validateEditorialV1Family enforces the Editorial Visual Motion V1 contract
// on the two families the milestone certifies: an editorial image entrance
// settles exactly (scale/opacity rest at 1, camera-backed transforms rest at
// the neutral 0), and a text 3D motion always carries camera-backed motion.
// Scoped to the V1 categories so the legacy families keep their historical
// freedoms while every new motion is born under the stricter gate.
func validateEditorialV1Family(d MotionDefinition) error {
	if d.Category != "editorial_image_v1" && d.Category != "text_3d_v1" {
		return nil
	}
	if d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames {
		return fmt.Errorf("motion %q: %s duration_bounds are invalid", d.ID, d.Category)
	}
	if d.RenderSafe == nil || !*d.RenderSafe {
		return fmt.Errorf("motion %q: %s motions must declare render_safe", d.ID, d.Category)
	}
	has3D := false
	for _, track := range d.Tracks {
		if err := validateV1RestingTrack(d, "track", track); err != nil {
			return err
		}
		if IsCameraBacked3DProperty(track.Property) {
			has3D = true
		}
	}
	for _, animator := range d.TextAnimators {
		for _, track := range animator.Properties {
			if err := validateV1RestingTrack(d, "text animator "+animator.ID, track); err != nil {
				return err
			}
			if IsCameraBacked3DProperty(track.Property) {
				has3D = true
			}
		}
	}
	if d.Category == "text_3d_v1" && !has3D {
		return fmt.Errorf("motion %q: text_3d_v1 motions must carry camera-backed 3D motion", d.ID)
	}
	if d.Requires3D != nil && *d.Requires3D != has3D {
		return fmt.Errorf("motion %q: requires_3d=%v does not match its camera-backed tracks (%v)", d.ID, *d.Requires3D, has3D)
	}
	return nil
}

// validateV1RestingTrack pins the final resting pose of one V1 track: an
// entrance ends at the authored resting value, so opacity and scale return
// to 1 and camera-backed transforms return to the neutral 0 — a motion that
// rests elsewhere leaves every rendered card subtly rotated or faded.
func validateV1RestingTrack(d MotionDefinition, owner string, t TrackDefinition) error {
	if len(t.Keyframes) < 2 {
		return fmt.Errorf("motion %q: %s needs at least two keyframes", d.ID, owner)
	}
	last := t.Keyframes[len(t.Keyframes)-1]
	value, ok := last.Value.(float64)
	if !ok {
		return fmt.Errorf("motion %q: %s resting value has type %T, want number", d.ID, owner, last.Value)
	}
	switch t.Property {
	case "scale", "scale_x", "scale_y", "opacity":
		if value != 1 {
			return fmt.Errorf("motion %q: %s must rest at 1, got %v", d.ID, t.Property, value)
		}
	case "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z":
		if value != 0 {
			return fmt.Errorf("motion %q: %s must rest at the neutral 0, got %v", d.ID, t.Property, value)
		}
	}
	return nil
}

// validateVisualAccentsV1Family enforces the Visual Accents V1 contract on the
// four official families: V1 metadata completeness (duration bounds, render
// safety, 3D/camera truthfulness) and, for the families that lower through the
// image recipe, exactly the recipe shapes each family admits — brush traits
// are stroked paths, web cards are rounded panels, paint reveals are field
// masks, light leaks are additive glow plates. A wrong shape in a family is a
// catalog authoring bug and fails closed here, before it can reach a render.
func validateVisualAccentsV1Family(d MotionDefinition) error {
	if !VisualAccentsV1CategoriesContains(d.Category) {
		return nil
	}
	if d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames {
		return fmt.Errorf("motion %q: %s duration_bounds are invalid", d.ID, d.Category)
	}
	if d.RenderSafe == nil || !*d.RenderSafe {
		return fmt.Errorf("motion %q: %s motions must declare render_safe", d.ID, d.Category)
	}
	if d.Unit != "layer" || d.Enter <= 0 || d.Exit < 0 {
		return fmt.Errorf("motion %q: %s motions must be layer motions with positive enter", d.ID, d.Category)
	}
	if len(d.Targets) == 0 {
		return fmt.Errorf("motion %q: %s motions must declare their targets", d.ID, d.Category)
	}
	has3D := false
	for _, track := range d.Tracks {
		if IsCameraBacked3DProperty(track.Property) {
			has3D = true
		}
	}
	if d.Requires3D != nil && *d.Requires3D != has3D {
		return fmt.Errorf("motion %q: requires_3d=%v does not match its camera-backed tracks (%v)", d.ID, *d.Requires3D, has3D)
	}
	if d.RequiresCamera != nil && *d.RequiresCamera != has3D {
		return fmt.Errorf("motion %q: requires_camera=%v does not match its camera-backed tracks (%v)", d.ID, *d.RequiresCamera, has3D)
	}
	if d.ImageRecipe == nil {
		return fmt.Errorf("motion %q: %s motions must lower through an image_recipe", d.ID, d.Category)
	}
	recipe := d.ImageRecipe
	if recipe.Stack || recipe.RequireCaption || recipe.ActiveLayerParam != "" || len(recipe.CaptionTracks) > 0 || len(recipe.InactiveTracks) > 0 {
		return fmt.Errorf("motion %q: %s recipes do not support multi-image compositions or captions", d.ID, d.Category)
	}
	switch d.Category {
	case "paint_v1":
		if recipe.Mask == nil || len(recipe.Components) > 0 {
			return fmt.Errorf("motion %q: paint_v1 motions lower through a field mask, not components", d.ID)
		}
		if recipe.Mask.Kind != "field" || recipe.Mask.Field == nil {
			return fmt.Errorf("motion %q: paint_v1 mask must be a field mask", d.ID)
		}
		return validateVisualAccentsFieldMask(d, *recipe.Mask.Field)
	case "brush_v1":
		if recipe.Mask != nil || len(recipe.Components) == 0 {
			return fmt.Errorf("motion %q: brush_v1 motions lower through stroked path components", d.ID)
		}
		for _, component := range recipe.Components {
			if component.Type != "shape" || component.Shape != "path" || component.Stroke == nil {
				return fmt.Errorf("motion %q: brush_v1 component %q must be a stroked path", d.ID, component.ID)
			}
			if component.Trim == nil {
				return fmt.Errorf("motion %q: brush_v1 component %q must trim-animate its stroke", d.ID, component.ID)
			}
		}
		return nil
	case "web_rect_v1":
		if recipe.Mask != nil || len(recipe.Components) == 0 {
			return fmt.Errorf("motion %q: web_rect_v1 motions lower through rounded panel components", d.ID)
		}
		for _, component := range recipe.Components {
			if component.Type != "shape" || (component.Shape != "rounded_rect" && component.Shape != "path") {
				return fmt.Errorf("motion %q: web_rect_v1 component %q must be a rounded panel", d.ID, component.ID)
			}
		}
		return nil
	case "light_leak_v1":
		if recipe.Mask != nil || len(recipe.Components) == 0 {
			return fmt.Errorf("motion %q: light_leak_v1 motions lower through glow plate components", d.ID)
		}
		for _, component := range recipe.Components {
			if component.Type != "shape" || component.Shape != "ellipse" {
				return fmt.Errorf("motion %q: light_leak_v1 component %q must be an ellipse glow plate", d.ID, component.ID)
			}
		}
		return nil
	}
	return nil
}

// validateVisualAccentsFieldMask bounds the field recipes the paint family may
// author: bounded frequency and feather, a bounded operator chain, and a
// progress track that starts closed and ends open.
func validateVisualAccentsFieldMask(d MotionDefinition, field ImageMotionFieldDefinition) error {
	if field.Frequency <= 0 || field.Frequency > 4096 {
		return fmt.Errorf("motion %q: paint_v1 field frequency must be in (0, 4096]", d.ID)
	}
	if len(field.Operators) > 8 {
		return fmt.Errorf("motion %q: paint_v1 field operator chain exceeds 8", d.ID)
	}
	return nil
}

func validateImagePremiumV1(d MotionDefinition) error {
	if d.Category != "image_premium_v1" {
		// Visual Accents V1 families are recipe-driven by design and carry their
		// own family-shaped gate in validateVisualAccentsV1Family; every other
		// category must stay recipe-free so a recipe cannot silently ship
		// outside a certified lowering path.
		if d.ImageRecipe != nil && !VisualAccentsV1CategoriesContains(d.Category) {
			return fmt.Errorf("motion %q: image_recipe is only valid in image_premium_v1", d.ID)
		}
		return nil
	}
	if d.ImageRecipe == nil || d.Unit != "layer" || d.RenderSafe == nil || !*d.RenderSafe ||
		d.Requires3D == nil || d.RequiresCamera == nil || d.DurationBounds == nil ||
		d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames ||
		d.Enter <= 0 || int64(d.Enter) > d.DurationBounds.MaximumFrames || d.Exit < 0 ||
		int64(d.Exit) >= d.DurationBounds.MinimumFrames ||
		len(d.Tracks) == 0 || len(d.ImageRecipe.Components) == 0 ||
		len(d.SupportedContent) == 0 || !containsMotionString(d.SupportedContent, "image") ||
		len(d.RequiredProperties) == 0 {
		return fmt.Errorf("motion %q: image_premium_v1 requires a layer recipe, render safety, enter and valid duration bounds", d.ID)
	}
	imageTarget := false
	for _, target := range d.Targets {
		imageTarget = imageTarget || target == "image"
	}
	if !imageTarget {
		return fmt.Errorf("motion %q: image_premium_v1 must target image", d.ID)
	}
	for _, target := range d.Targets {
		if target != "image" {
			return fmt.Errorf("motion %q: image_premium_v1 has unsupported target %q", d.ID, target)
		}
	}
	has3D := false
	trackProperties := make(map[string]bool, len(d.Tracks))
	for _, track := range d.Tracks {
		if err := validateImageMotionTrack(d.ID, "image track", track); err != nil {
			return err
		}
		trackProperties[track.Property] = true
		has3D = has3D || IsCameraBacked3DProperty(track.Property)
	}
	required := make(map[string]bool, len(d.RequiredProperties))
	for _, property := range d.RequiredProperties {
		if !trackProperties[property] || required[property] {
			return fmt.Errorf("motion %q: required property %q is missing or duplicated", d.ID, property)
		}
		required[property] = true
	}
	if len(required) != len(trackProperties) {
		return fmt.Errorf("motion %q: required_properties must list every image track property", d.ID)
	}
	recipe := d.ImageRecipe
	if recipe.Stack && recipe.ActiveLayerParam == "" {
		return fmt.Errorf("motion %q: multi-image recipes must name the active_layer_id parameter", d.ID)
	}
	if !recipe.Stack && recipe.ActiveLayerParam != "" {
		return fmt.Errorf("motion %q: active_layer_param requires a multi-image recipe", d.ID)
	}
	if recipe.Stack && recipe.ActiveLayerParam != "active_layer_id" {
		return fmt.Errorf("motion %q: multi-image recipe must use active_layer_id", d.ID)
	}
	if recipe.RequireCaption && (len(recipe.CaptionTracks) == 0 || !containsMotionString(d.Targets, "image")) {
		return fmt.Errorf("motion %q: required image captions need an image target and caption_tracks", d.ID)
	}
	var err error
	has3D, err = validateImageMotionTracks(d.ID, "inactive image track", recipe.InactiveTracks, has3D)
	if err != nil {
		return err
	}
	has3D, err = validateImageMotionTracks(d.ID, "caption track", recipe.CaptionTracks, has3D)
	if err != nil {
		return err
	}
	componentIDs := make(map[string]bool, len(recipe.Components))
	for _, component := range recipe.Components {
		if component.ID == "" || componentIDs[component.ID] {
			return fmt.Errorf("motion %q: image recipe component ids must be non-empty and unique", d.ID)
		}
		componentIDs[component.ID] = true
		if component.Type != "shape" && component.Type != "color" {
			return fmt.Errorf("motion %q: component %q has unsupported type %q", d.ID, component.ID, component.Type)
		}
		if component.Type == "shape" && component.Shape != "rounded_rect" && component.Shape != "ellipse" && component.Shape != "path" && component.Shape != "rect" {
			return fmt.Errorf("motion %q: component %q has unsupported shape %q", d.ID, component.ID, component.Shape)
		}
		if component.Placement != "" && component.Placement != "before_image" && component.Placement != "after_image" {
			return fmt.Errorf("motion %q: component %q placement must be before_image or after_image", d.ID, component.ID)
		}
		if len(component.SizeScale) != 0 && len(component.SizeScale) != 2 {
			return fmt.Errorf("motion %q: component %q size_scale must contain two values", d.ID, component.ID)
		}
		for _, value := range component.SizeScale {
			if !finiteMotionValue(value) || value <= 0 {
				return fmt.Errorf("motion %q: component %q size_scale values must be finite and positive", d.ID, component.ID)
			}
		}
		if len(component.PositionOffset) != 0 && len(component.PositionOffset) != 2 {
			return fmt.Errorf("motion %q: component %q position_offset must contain two values", d.ID, component.ID)
		}
		for _, value := range component.PositionOffset {
			if !finiteMotionValue(value) {
				return fmt.Errorf("motion %q: component %q position_offset values must be finite", d.ID, component.ID)
			}
		}
		if !finiteMotionValue(component.ZOffset) || !finiteMotionValue(component.RadiusScale) || component.RadiusScale < 0 {
			return fmt.Errorf("motion %q: component %q offsets and radius scale must be finite; radius scale cannot be negative", d.ID, component.ID)
		}
		if len(component.Fill) != 0 {
			if len(component.Fill) != 4 {
				return fmt.Errorf("motion %q: component %q fill must be RGBA", d.ID, component.ID)
			}
			for _, value := range component.Fill {
				if !finiteMotionValue(value) || value < 0 || value > 1 {
					return fmt.Errorf("motion %q: component %q fill channels must be in [0, 1]", d.ID, component.ID)
				}
			}
		}
		if component.Opacity != nil && (!finiteMotionValue(*component.Opacity) || *component.Opacity < 0 || *component.Opacity > 1) {
			return fmt.Errorf("motion %q: component %q opacity must be in [0, 1]", d.ID, component.ID)
		}
		if component.SyncTransform && component.Type != "shape" {
			return fmt.Errorf("motion %q: only shape components can synchronize an image transform", d.ID)
		}
		if component.SyncTransform && len(component.Tracks) > 0 {
			return fmt.Errorf("motion %q: synchronized frame %q cannot override the source transform with component tracks", d.ID, component.ID)
		}
		if component.Type == "color" && component.CanvasSize && component.Shape != "" {
			return fmt.Errorf("motion %q: canvas color component %q cannot declare a shape", d.ID, component.ID)
		}
		if component.Type == "color" && len(component.Fill) != 4 {
			return fmt.Errorf("motion %q: color component %q requires RGBA fill", d.ID, component.ID)
		}
		if component.Type == "shape" && len(component.Fill) == 0 && component.Gradient == nil && component.Stroke == nil {
			return fmt.Errorf("motion %q: shape component %q needs a fill, gradient or stroke", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape == "path" && component.Stroke == nil {
			return fmt.Errorf("motion %q: path component %q requires a stroke", d.ID, component.ID)
		}
		if component.CanvasSize && component.Type != "color" {
			return fmt.Errorf("motion %q: only color components may cover the full canvas", d.ID)
		}
		if component.Type == "shape" && component.Shape == "path" && component.PathKind != "rounded_rect" && component.PathKind != "line" {
			return fmt.Errorf("motion %q: component %q path requires a supported path_kind", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape != "path" && component.PathKind != "" {
			return fmt.Errorf("motion %q: component %q path_kind is only valid on path shapes", d.ID, component.ID)
		}
		if component.Type == "shape" && component.Shape == "rect" && component.RadiusScale != 0 {
			return fmt.Errorf("motion %q: rect component %q cannot set a corner radius scale", d.ID, component.ID)
		}
		if component.ZOffset != 0 {
			has3D = true
		}
		if component.Stroke != nil {
			if component.Stroke.Width < 0 || !finiteMotionValue(component.Stroke.Width) || !validMotionHexColor(component.Stroke.Color) {
				return fmt.Errorf("motion %q: component %q stroke needs a hex color and finite non-negative width", d.ID, component.ID)
			}
		}
		if component.Gradient != nil {
			if component.Gradient.Type != "linear" {
				return fmt.Errorf("motion %q: component %q has unsupported gradient type %q (only linear is currently lowered)", d.ID, component.ID, component.Gradient.Type)
			}
			if len(component.Gradient.Start) != 2 || len(component.Gradient.End) != 2 {
				return fmt.Errorf("motion %q: component %q linear gradient requires start and end points", d.ID, component.ID)
			}
			for _, point := range [][]float64{component.Gradient.Start, component.Gradient.End} {
				for _, value := range point {
					if !finiteMotionValue(value) || math.Abs(value) > 1000000 {
						return fmt.Errorf("motion %q: component %q gradient geometry is invalid", d.ID, component.ID)
					}
				}
			}
			if len(component.Gradient.Stops) < 2 {
				return fmt.Errorf("motion %q: component %q gradient needs at least two color stops", d.ID, component.ID)
			}
			previousStop := -1.0
			for _, stop := range component.Gradient.Stops {
				if !finiteMotionValue(stop.Position) || stop.Position < 0 || stop.Position > 1 || stop.Position < previousStop || len(stop.Color) != 4 {
					return fmt.Errorf("motion %q: component %q gradient stop is invalid", d.ID, component.ID)
				}
				for _, value := range stop.Color {
					if !finiteMotionValue(value) || value < 0 || value > 1 {
						return fmt.Errorf("motion %q: component %q gradient color channels must be in [0, 1]", d.ID, component.ID)
					}
				}
				previousStop = stop.Position
			}
		}
		has3D, err = validateImageMotionTracks(d.ID, "component "+component.ID, component.Tracks, has3D)
		if err != nil {
			return err
		}
		for _, paramTrack := range component.EffectParamTracks {
			if paramTrack.EffectID == "" || paramTrack.Param == "" {
				return fmt.Errorf("motion %q: component %q effect parameter tracks require effect_id and param", d.ID, component.ID)
			}
			if err := validateImageMotionTrack(d.ID, "component "+component.ID+" effect parameter", paramTrack.Track); err != nil {
				return err
			}
			if !imageRecipeHasEffect(component, paramTrack.EffectID) {
				return fmt.Errorf("motion %q: component %q effect parameter track targets undeclared effect %q", d.ID, component.ID, paramTrack.EffectID)
			}
			if paramTrack.EffectID != "light.glow" || paramTrack.Param != "intensity" && paramTrack.Param != "radius" {
				return fmt.Errorf("motion %q: component %q uses unsupported animatable effect parameter %s.%s", d.ID, component.ID, paramTrack.EffectID, paramTrack.Param)
			}
			for _, keyframe := range paramTrack.Track.Keyframes {
				value, ok := motionNumericValue(keyframe.Value)
				maximum := 4.0
				if paramTrack.Param == "radius" {
					maximum = 256
				}
				if !ok || value < 0 || value > maximum {
					return fmt.Errorf("motion %q: component %q %s.%s samples must be in [0, %g]", d.ID, component.ID, paramTrack.EffectID, paramTrack.Param, maximum)
				}
			}
		}
		if component.Trim != nil {
			if !finiteMotionValue(component.Trim.Start) || !finiteMotionValue(component.Trim.End) || component.Trim.Start < 0 || component.Trim.Start > component.Trim.End || component.Trim.End > 1 {
				return fmt.Errorf("motion %q: component %q trim bounds must satisfy 0 <= start <= end <= 1", d.ID, component.ID)
			}
			if component.Shape != "path" {
				return fmt.Errorf("motion %q: component %q trim requires a path shape", d.ID, component.ID)
			}
			if err := validateImageMotionTrack(d.ID, "component "+component.ID+" trim", component.Trim.Animation); err != nil {
				return err
			}
			for _, keyframe := range component.Trim.Animation.Keyframes {
				values, ok := motionNumericVector(keyframe.Value)
				if !ok || len(values) < 2 || len(values) > 3 || values[0] < 0 || values[0] > 1 || values[1] < values[0] || values[1] > 1 || (len(values) > 2 && math.Abs(values[2]) > 1000000) {
					return fmt.Errorf("motion %q: component %q trim animation requires bounded [start,end,offset?] values", d.ID, component.ID)
				}
			}
		}
		for _, effect := range component.Effects {
			if effect.Type == "" || !imageRecipeEffectSupported(effect.Type) {
				return fmt.Errorf("motion %q: component %q has unsupported effect type %q", d.ID, component.ID, effect.Type)
			}
			if err := validateImageRecipeEffect(d.ID, component.ID, effect); err != nil {
				return err
			}
		}
		for _, track := range component.EffectParamTracks {
			has3D = has3D || IsCameraBacked3DProperty(track.Track.Property)
		}
	}
	if recipe.Mask != nil {
		if recipe.Mask.Kind != "wipe_left" || !finiteMotionValue(recipe.Mask.Feather) || recipe.Mask.Feather < 0 || recipe.Mask.Feather > 1000000 || !containsMotionString(d.Targets, "image") {
			return fmt.Errorf("motion %q: image mask must be a bounded wipe_left recipe", d.ID)
		}
		if err := validateTrackDefinition(d.ID, "image mask progress", recipe.Mask.Progress); err != nil {
			return err
		}
		if recipe.Mask.Progress.Property != "mask_morph" || recipe.Mask.Progress.Easing != "" && !validMotionEasing(recipe.Mask.Progress.Easing) {
			return fmt.Errorf("motion %q: image mask progress must use mask_morph with a supported easing", d.ID)
		}
		for _, keyframe := range recipe.Mask.Progress.Keyframes {
			value, ok := motionNumericValue(keyframe.Value)
			if !ok || value < 0 || value > 1 {
				return fmt.Errorf("motion %q: image mask progress keyframes must be in [0, 1]", d.ID)
			}
		}
	}
	if *d.Requires3D != has3D {
		return fmt.Errorf("motion %q: requires_3d=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.Requires3D, has3D)
	}
	if *d.RequiresCamera != has3D {
		return fmt.Errorf("motion %q: requires_camera=%v does not match its camera-backed recipe tracks (%v)", d.ID, *d.RequiresCamera, has3D)
	}
	return nil
}

// validateImageMotionTracks validates a recipe track collection and accumulates
// whether any property requires camera-backed 3D routing. Collection order and
// the first validation error are preserved by this shared helper.
func validateImageMotionTracks(id, owner string, tracks []TrackDefinition, has3D bool) (bool, error) {
	for _, track := range tracks {
		if err := validateImageMotionTrack(id, owner, track); err != nil {
			return has3D, err
		}
		has3D = has3D || IsCameraBacked3DProperty(track.Property)
	}
	return has3D, nil
}

func finiteMotionValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func motionNumericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, finiteMotionValue(number)
	case float32:
		result := float64(number)
		return result, finiteMotionValue(result)
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	default:
		return 0, false
	}
}

func motionNumericVector(value any) ([]float64, bool) {
	switch values := value.(type) {
	case []any:
		out := make([]float64, len(values))
		for i, item := range values {
			number, ok := motionNumericValue(item)
			if !ok {
				return nil, false
			}
			out[i] = number
		}
		return out, true
	case []float64:
		for _, number := range values {
			if !finiteMotionValue(number) {
				return nil, false
			}
		}
		return values, true
	default:
		return nil, false
	}
}

func validateImageMotionTrack(id, owner string, track TrackDefinition) error {
	if err := validateTrackDefinition(id, owner, track); err != nil {
		return err
	}
	propertyAllowed := false
	for _, property := range []string{"position", "position_x", "position_y", "position_z", "scale", "scale_x", "scale_y", "rotation", "rotation_x", "rotation_y", "rotation_z", "opacity", "blur", "stroke_width", "stroke_color", "fill_color", "trim", "intensity", "radius"} {
		propertyAllowed = propertyAllowed || track.Property == property
	}
	if !propertyAllowed {
		return fmt.Errorf("motion %q: %s uses unsupported image-renderer property %q", id, owner, track.Property)
	}
	if track.Easing != "" && !validMotionEasing(track.Easing) {
		return fmt.Errorf("motion %q: %s has unsupported easing %q", id, owner, track.Easing)
	}
	for _, keyframe := range track.Keyframes {
		switch track.Property {
		case "position", "rotation", "stroke_color", "fill_color", "trim":
			vector, ok := motionNumericVector(keyframe.Value)
			if !ok {
				return fmt.Errorf("motion %q: %s %s keyframe values must be numeric vectors", id, owner, track.Property)
			}
			switch track.Property {
			case "position":
				if len(vector) != 2 && len(vector) != 3 {
					return fmt.Errorf("motion %q: %s position vectors must have two or three values", id, owner)
				}
			case "rotation":
				if len(vector) != 3 {
					return fmt.Errorf("motion %q: %s rotation vectors must have three values", id, owner)
				}
			case "stroke_color", "fill_color":
				if len(vector) != 4 {
					return fmt.Errorf("motion %q: %s %s values must be RGBA vectors", id, owner, track.Property)
				}
				for _, value := range vector {
					if value < 0 || value > 1 {
						return fmt.Errorf("motion %q: %s %s channels must be in [0, 1]", id, owner, track.Property)
					}
				}
			case "trim":
				if len(vector) < 2 || len(vector) > 3 || vector[0] < 0 || vector[0] > 1 || vector[1] < vector[0] || vector[1] > 1 {
					return fmt.Errorf("motion %q: %s trim values require [start,end,offset?] with 0 <= start <= end <= 1", id, owner)
				}
			}
			for _, value := range vector {
				if value < -1000000 || value > 1000000 {
					return fmt.Errorf("motion %q: %s vector is outside supported bounds", id, owner)
				}
			}
		default:
			number, ok := motionNumericValue(keyframe.Value)
			if !ok || number < -1000000 || number > 1000000 {
				return fmt.Errorf("motion %q: %s %s keyframe values must be finite bounded numbers", id, owner, track.Property)
			}
		}
	}
	return nil
}

func validMotionEasing(easing string) bool {
	switch easing {
	case "linear", "in_quad", "out_quad", "in_out_quad", "in_cubic", "out_cubic", "in_out_cubic",
		"in_expo", "out_expo", "in_out_expo", "in_sine", "out_sine", "in_out_sine",
		"in_back", "out_back", "in_out_back", "in_elastic", "out_elastic", "in_out_elastic",
		"in_bounce", "out_bounce", "in_out_bounce", "smoothstep", "hold":
		return true
	default:
		return false
	}
}

func finiteMotionValueTree(value any) bool {
	switch item := value.(type) {
	case float64:
		return finiteMotionValue(item)
	case float32:
		return finiteMotionValue(float64(item))
	case []any:
		for _, child := range item {
			if !finiteMotionValueTree(child) {
				return false
			}
		}
	case []float64:
		for _, child := range item {
			if !finiteMotionValue(child) {
				return false
			}
		}
	case map[string]any:
		for _, child := range item {
			if !finiteMotionValueTree(child) {
				return false
			}
		}
	}
	return true
}

func imageRecipeHasEffect(component ImageMotionComponent, id string) bool {
	for _, effect := range component.Effects {
		if imageRecipeEffectID(effect.Type) == id {
			return true
		}
	}
	return false
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

func imageRecipeEffectSupported(kind string) bool { return imageRecipeEffectID(kind) != "" }

func validateImageRecipeEffect(motionID, componentID string, effect ImageMotionEffect) error {
	allowed := map[string]map[string]bool{
		"glow":          {"radius": true, "intensity": true, "color": true},
		"bloom":         {"radius": true, "intensity": true, "threshold": true},
		"drop_shadow":   {"radius": true, "offset": true, "color": true},
		"gaussian_blur": {"radius": true},
	}[effect.Type]
	for key, raw := range effect.Params {
		if !allowed[key] || !finiteMotionValueTree(raw) {
			return fmt.Errorf("motion %q: component %q has unsupported or non-finite %s parameter %q", motionID, componentID, effect.Type, key)
		}
		switch key {
		case "radius":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 256 {
				return fmt.Errorf("motion %q: component %q effect radius must be in [0, 256]", motionID, componentID)
			}
		case "intensity":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 4 {
				return fmt.Errorf("motion %q: component %q effect intensity must be in [0, 4]", motionID, componentID)
			}
		case "threshold":
			value, ok := motionNumericValue(raw)
			if !ok || value < 0 || value > 1 {
				return fmt.Errorf("motion %q: component %q effect threshold must be in [0, 1]", motionID, componentID)
			}
		case "color":
			values, ok := motionNumericVector(raw)
			if !ok || len(values) != 4 {
				return fmt.Errorf("motion %q: component %q effect color must be RGBA", motionID, componentID)
			}
			for _, value := range values {
				if value < 0 || value > 1 {
					return fmt.Errorf("motion %q: component %q effect color must be in [0, 1]", motionID, componentID)
				}
			}
		case "offset":
			values, ok := motionNumericVector(raw)
			if !ok || len(values) != 2 {
				return fmt.Errorf("motion %q: component %q shadow offset must be [x,y]", motionID, componentID)
			}
			for _, value := range values {
				if math.Abs(value) > 10000 {
					return fmt.Errorf("motion %q: component %q shadow offset exceeds supported bounds", motionID, componentID)
				}
			}
		}
	}
	return nil
}

func containsMotionString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validMotionHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func validateTrackDefinition(motionID, owner string, t TrackDefinition) error {
	if t.Property == "" || len(t.Keyframes) < 2 {
		return fmt.Errorf("motion %q: %s requires a property and at least two keyframes", motionID, owner)
	}
	if t.Keyframes[0].Frame != 0 {
		return fmt.Errorf("motion %q: %s must start at frame 0, got %d", motionID, owner, t.Keyframes[0].Frame)
	}
	for i, keyframe := range t.Keyframes {
		if keyframe.Frame < 0 || keyframe.Frame > 1000000 {
			return fmt.Errorf("motion %q: %s keyframe %d is outside [0, 1000000]", motionID, owner, keyframe.Frame)
		}
		if i > 0 && keyframe.Frame <= t.Keyframes[i-1].Frame {
			return fmt.Errorf("motion %q: %s keyframes are not strictly increasing at index %d", motionID, owner, i)
		}
	}
	return nil
}
