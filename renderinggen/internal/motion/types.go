package motion

import (
	"encoding/json"
	"fmt"
	"math"
)

import "github.com/Marcuss-ops/RenderingGen/renderinggen/countryflags"

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
	AbsolutePosition  bool                         `json:"absolute_position,omitempty"`
	AbsoluteRadius    bool                         `json:"absolute_radius,omitempty"`
	SyncTransform     bool                         `json:"sync_transform,omitempty"`
	SizeScale         []float64                    `json:"size_scale,omitempty"`
	PositionOffset    []float64                    `json:"position_offset,omitempty"`
	PositionScale     []float64                    `json:"position_scale,omitempty"`
	ZOffset           float64                      `json:"z_offset,omitempty"`
	RadiusScale       float64                      `json:"radius_scale,omitempty"`
	Fill              []float64                    `json:"fill,omitempty"`
	Stroke            *ImageMotionStroke           `json:"stroke,omitempty"`
	Gradient          *ImageMotionGradient         `json:"gradient,omitempty"`
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
// MotionDefinition is the declarative motion contract. ID is the ONLY
// identity: the historical `Name` alias (read as a fallback by the registry,
// the overlay resolver and the preset catalog) has been removed, because two
// fields that can each name the same motion let one producer set only the
// other and silently fail to resolve at render time.
type MotionDefinition struct {
	ID                  string                   `json:"id"`
	Category            string                   `json:"category,omitempty"`
	MapRenderer         string                   `json:"map_renderer,omitempty"`
	MapID               string                   `json:"map_id,omitempty"`
	MapAnimation        string                   `json:"map_animation,omitempty"`
	Deprecated          bool                     `json:"deprecated,omitempty"`
	RemoveAfter         string                   `json:"remove_after,omitempty"`
	DeprecationNote     string                   `json:"deprecation_note,omitempty"`
	ReplacementMotionID string                   `json:"replacement_motion_id,omitempty"`
	SupportedTemplate   string                   `json:"supported_template,omitempty"`
	Seeded              bool                     `json:"seeded,omitempty"`
	Targets             []string                 `json:"targets,omitempty"`
	Unit                string                   `json:"unit,omitempty"`
	SupportedContent    []string                 `json:"supported_content,omitempty"`
	DurationBounds      *DurationBounds          `json:"duration_bounds,omitempty"`
	RequiredProperties  []string                 `json:"required_properties,omitempty"`
	RenderSafe          *bool                    `json:"render_safe,omitempty"`
	Requires3D          *bool                    `json:"requires_3d,omitempty"`
	RequiresCamera      *bool                    `json:"requires_camera,omitempty"`
	Enter               int                      `json:"enter,omitempty"` // legacy preset timing
	Exit                int                      `json:"exit,omitempty"`
	Tracks              []TrackDefinition        `json:"tracks,omitempty"`
	TextAnimators       []TextAnimatorDefinition `json:"text_animators,omitempty"`
	LayerComponents     []ImageMotionComponent   `json:"layer_components,omitempty"`
	Selector            SelectorDefinition       `json:"selector,omitempty"`
	Stagger             StaggerDefinition        `json:"stagger,omitempty"`
	ImageRecipe         *ImageMotionRecipe       `json:"image_recipe,omitempty"`
}

type MotionPlugin interface {
	ID() string
	Validate(params MotionParams) error
	Compile(ctx MotionContext, params MotionParams) ([]AnimationTrack, error)
}

// TextMotionPlugin is optional: layer/image plugins keep using MotionPlugin,
// while text plugins can lower selectors and per-glyph properties as well.
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
	if d.Deprecated && (d.RemoveAfter == "" || d.DeprecationNote == "") {
		return fmt.Errorf("motion %q: deprecated definitions require remove_after and deprecation_note", d.ID)
	}
	if d.MapRenderer != "" || d.MapID != "" || d.MapAnimation != "" {
		hasMapTarget := false
		for _, target := range d.Targets {
			hasMapTarget = hasMapTarget || target == "map_view"
		}
		if d.Category != "map_image_v1" || !hasMapTarget {
			return fmt.Errorf("motion %q: map renderer metadata requires the map_image_v1/map_view family", d.ID)
		}
		if (d.MapRenderer != "dark_map" && d.MapRenderer != "opencv") || d.MapID == "" || d.MapAnimation == "" {
			return fmt.Errorf("motion %q: map renderer metadata is incomplete or unsupported", d.ID)
		}
		if d.DurationBounds == nil || d.DurationBounds.MinimumFrames != 150 || d.DurationBounds.MaximumFrames != 150 {
			return fmt.Errorf("motion %q: paired map motions must have a fixed 150-frame duration", d.ID)
		}
		if _, ok := countryflags.LookupByName(d.MapID); !ok {
			return fmt.Errorf("motion %q: map_id %q has no bundled country flag", d.ID, d.MapID)
		}
	}
	if err := validateImagePremiumV1(d); err != nil {
		return err
	}
	if d.Category == "phrase_highlight_v1" {
		if d.Unit != "layer" || d.Enter <= 0 || d.Exit < 0 || d.DurationBounds != nil ||
			d.ImageRecipe != nil || len(d.Tracks) == 0 || len(d.LayerComponents) == 0 || len(d.Targets) == 0 ||
			!containsMotionString(d.Targets, "text") || !containsMotionString(d.Targets, "phrase") ||
			!containsMotionString(d.Targets, "important_phrase") {
			return fmt.Errorf("motion %q: phrase_highlight_v1 requires a layer entrance and native layer components", d.ID)
		}
		componentIDs := make(map[string]struct{}, len(d.LayerComponents))
		for _, component := range d.LayerComponents {
			if component.ID == "" {
				return fmt.Errorf("motion %q: phrase highlight component id is empty", d.ID)
			}
			if _, duplicate := componentIDs[component.ID]; duplicate {
				return fmt.Errorf("motion %q: phrase highlight repeats component id %q", d.ID, component.ID)
			}
			componentIDs[component.ID] = struct{}{}
			if component.ID == "" || component.Type != "shape" || component.Shape != "rect" ||
				component.Placement != "before_image" || component.AbsoluteRadius ||
				len(component.SizeScale) != 2 || len(component.Fill) != 4 ||
				(!component.AbsolutePosition && len(component.PositionScale) != 2) ||
				(component.AbsolutePosition && len(component.PositionOffset) != 2) ||
				component.Opacity == nil || *component.Opacity <= 0 || *component.Opacity > 1 || len(component.Tracks) == 0 {
				return fmt.Errorf("motion %q: phrase highlight component %q has incomplete native shape data", d.ID, component.ID)
			}
			if component.Stroke != nil && (component.Stroke.Color == "" || component.Stroke.Width <= 0 ||
				math.IsNaN(component.Stroke.Width) || math.IsInf(component.Stroke.Width, 0)) {
				return fmt.Errorf("motion %q: phrase highlight component %q has invalid native stroke data", d.ID, component.ID)
			}
			invalidRelativePosition := !component.AbsolutePosition &&
				(len(component.PositionScale) != 2 || component.PositionScale[0] < 0 || component.PositionScale[0] > 1 ||
					component.PositionScale[1] < 0 || component.PositionScale[1] > 1)
			if component.AbsolutePosition && (len(component.PositionScale) > 0 || len(component.PositionOffset) != 2) {
				return fmt.Errorf("motion %q: phrase highlight component %q with absolute position must carry position_offset, not position_scale", d.ID, component.ID)
			}
			if component.SizeScale[0] <= 0 || component.SizeScale[1] <= 0 || component.RadiusScale < 0 ||
				invalidRelativePosition || !finiteMotionValues(component.SizeScale) ||
				!finiteMotionValues(component.PositionOffset) || !finiteMotionValues(component.PositionScale) ||
				math.IsNaN(component.RadiusScale) || math.IsInf(component.RadiusScale, 0) {
				return fmt.Errorf("motion %q: phrase highlight component %q has invalid geometry", d.ID, component.ID)
			}
			for _, value := range component.Fill {
				if value < 0 || value > 1 || math.IsNaN(value) || math.IsInf(value, 0) {
					return fmt.Errorf("motion %q: phrase highlight component %q has invalid RGBA", d.ID, component.ID)
				}
			}
			seenProperties := make(map[string]struct{}, len(component.Tracks))
			for _, track := range component.Tracks {
				if err := validateTrackDefinition(d.ID, "component "+component.ID, track); err != nil {
					return err
				}
				switch track.Property {
				case "scale_x", "position_x", "opacity":
				default:
					return fmt.Errorf("motion %q: phrase highlight component %q uses unsupported property %q", d.ID, component.ID, track.Property)
				}
				if _, duplicate := seenProperties[track.Property]; duplicate {
					return fmt.Errorf("motion %q: phrase highlight component %q repeats property %q", d.ID, component.ID, track.Property)
				}
				seenProperties[track.Property] = struct{}{}
				if track.Property == "scale_x" {
					for _, key := range track.Keyframes {
						if _, ok := positiveMotionScalar(key.Value); !ok {
							return fmt.Errorf("motion %q: phrase highlight component %q scale_x keyframes must be positive finite scalars", d.ID, component.ID)
						}
					}
				}
			}
		}
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

func finiteMotionValues(values []float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func positiveMotionScalar(value any) (float64, bool) {
	var scalar float64
	switch number := value.(type) {
	case float64:
		scalar = number
	case float32:
		scalar = float64(number)
	case int:
		scalar = float64(number)
	case int64:
		scalar = float64(number)
	default:
		return 0, false
	}
	return scalar, scalar > 0 && !math.IsNaN(scalar) && !math.IsInf(scalar, 0)
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
