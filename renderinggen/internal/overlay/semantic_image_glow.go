// semantic_image_glow.go lowers the payload's image.glow block onto the native
// Chronon glow effect. Image glow is deliberately an effect, not a text-style
// glow: it must decorate the photographic media layer and never a caption.
package overlay

import (
	"fmt"
	"strings"
)

// applyImageGlowToBaseLayers applies image.glow to every base image layer the
// item lowered. Base layers end in ":image", which keeps decorative recipe
// plates and backdrops out of the halo. The operation is a no-op when the
// payload has no image_glow_radius key, so existing plans are untouched.
func applyImageGlowToBaseLayers(ri resolvedItem, layers []Layer) ([]Layer, error) {
	raw, exists := ri.Params["image_glow_radius"]
	if !exists {
		return layers, nil
	}
	radius, ok := numericValue(raw)
	if !ok || !finite(radius) || radius < 0 || radius > maxRuntimeGlowSize {
		return nil, fmt.Errorf("overlay: image item %q image_glow_radius must be within [0,%g]", ri.Item.ID, maxRuntimeGlowSize)
	}
	if radius == 0 {
		return layers, nil
	}
	applied := false
	for index := range layers {
		layer := &layers[index]
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") {
			continue
		}
		if err := applyImageGlow(layer, ri.Params); err != nil {
			return nil, fmt.Errorf("overlay: image item %q: %w", ri.Item.ID, err)
		}
		applied = true
	}
	if !applied {
		return nil, fmt.Errorf("overlay: image item %q requests image.glow but lowered no base image layer", ri.Item.ID)
	}
	return layers, nil
}

// applyImageGlow validates and appends the native glow effect. Radius 0 or an
// explicit intensity of 0 disables the effect without emitting a zero-radius
// halo. Color is converted to Chronon's RGBA effect parameter.
func applyImageGlow(layer *Layer, params map[string]any) error {
	rawRadius, exists := params["image_glow_radius"]
	if !exists {
		return nil
	}
	radius, ok := numericValue(rawRadius)
	if !ok || !finite(radius) || radius < 0 || radius > maxRuntimeGlowSize {
		return fmt.Errorf("image_glow_radius must be within [0,%g]", maxRuntimeGlowSize)
	}
	if radius == 0 {
		return nil
	}
	intensity := 0.25
	if raw, exists := params["image_glow_intensity"]; exists {
		value, ok := numericValue(raw)
		if !ok || !finite(value) || value < 0 || value > maxRuntimeGlowIntensity {
			return fmt.Errorf("image_glow_intensity must be within [0,%g]", maxRuntimeGlowIntensity)
		}
		intensity = value
	}
	if intensity == 0 {
		return nil
	}
	color := "#FFFFFF"
	if raw, exists := params["image_glow_color"]; exists {
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("image_glow_color must be #RRGGBB")
		}
		color = value
	}
	rgba, err := parseHexColor(color)
	if err != nil {
		return fmt.Errorf("image_glow_color: %w", err)
	}
	layer.Effects = append(layer.Effects, LayerEffect{
		Type: "glow",
		Params: map[string]any{
			"radius":    radius,
			"intensity": intensity,
			"color":     rgba,
		},
	})
	return nil
}
