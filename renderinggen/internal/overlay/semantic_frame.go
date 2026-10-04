package overlay

import (
	"fmt"
	"math"
)

// Bounds mirror the published semantic source-frame contract.
const (
	maxFrameBorderWidth  float64 = 512
	maxFrameRadius       float64 = 512
	maxFrameStrokeWidth  float64 = 64
	maxFrameShadowBlur   float64 = 256
	maxFrameShadowOffset float64 = 256
)

// applyFrameTreatment lowers a card declaration (plan-level source_frame or an
// item's frame) onto a media layer. It is the SINGLE owner of the translation:
//
//   - the border becomes the renderer-owned frame behind the media box, i.e.
//     style.background with the border colour, the border thickness as padding
//     and radius_px as the frame's outer corner radius;
//   - the media box itself takes the concentric inner radius
//     (radius_px - width_px), so the clip's corners nest inside the frame;
//   - clip_radius_px is a standalone bounding-box corner radius of the media
//     box. When a border IS present, the clip's inner radius is
//     max(radius_px - width_px, clip_radius_px); when no border is declared,
//     clip_radius_px directly rounds the clip's corners without painting any
//     visible frame behind it;
//   - the shadow becomes style.shadow, which the renderer attaches to the
//     frame when a frame exists and to the media layer otherwise;
//   - the stroke becomes a transparent, unfilled rounded rectangle above the
//     media, so it traces the visible clip perimeter rather than painting a
//     padded plate behind it.
//
// Every value comes from the declaration: the worker invents no colour, blur or
// geometry. A declaration with none of border, shadow, stroke, or
// clip_radius_px is rejected here (fail-closed).
func applyFrameTreatment(layer *Layer, frame *semanticSourceFrame) error {
	if frame == nil {
		return nil
	}
	if frame.Border == nil && frame.Shadow == nil && frame.Stroke == nil && frame.ClipRadiusPX == 0 {
		return fmt.Errorf("frame requires border, shadow, stroke, or clip_radius_px")
	}
	if frame.Stroke != nil {
		stroke := frame.Stroke
		if math.IsNaN(stroke.WidthPX) || math.IsInf(stroke.WidthPX, 0) || stroke.WidthPX < 0 || stroke.WidthPX > maxFrameStrokeWidth {
			return fmt.Errorf("frame stroke width_px must be within [0,%g]", maxFrameStrokeWidth)
		}
		if !isHexColor(stroke.Color) {
			return fmt.Errorf("frame stroke color %q must be #RRGGBB", stroke.Color)
		}
	}
	// Validate clip_radius_px range (same ceiling as border.radius_px).
	if math.IsNaN(frame.ClipRadiusPX) || math.IsInf(frame.ClipRadiusPX, 0) || frame.ClipRadiusPX < 0 || frame.ClipRadiusPX > maxFrameRadius {
		return fmt.Errorf("frame clip_radius_px must be within [0,%g]", maxFrameRadius)
	}
	// Start with the standalone clip bounding-box radius.
	clipRadius := frame.ClipRadiusPX
	if frame.Border != nil {
		border := frame.Border
		if math.IsNaN(border.WidthPX) || math.IsInf(border.WidthPX, 0) || border.WidthPX < 0 || border.WidthPX > maxFrameBorderWidth {
			return fmt.Errorf("frame border width_px must be within [0,%g]", maxFrameBorderWidth)
		}
		if math.IsNaN(border.RadiusPX) || math.IsInf(border.RadiusPX, 0) || border.RadiusPX < 0 || border.RadiusPX > maxFrameRadius {
			return fmt.Errorf("frame border radius_px must be within [0,%g]", maxFrameRadius)
		}
		if !isHexColor(border.Color) {
			return fmt.Errorf("frame border color %q must be #RRGGBB", border.Color)
		}
		if border.WidthPX > 0 {
			style := ensureLayerStyle(layer)
			style.Background = &LayerBackground{
				Color:   border.Color,
				Radius:  border.RadiusPX,
				Padding: []float64{border.WidthPX, border.WidthPX},
			}
		}
		// Concentric inner radius: the clip's corners nest inside the frame's.
		// When clip_radius_px is also declared, the larger value wins so the
		// caller can always guarantee a minimum bounding-box rounding.
		if inner := border.RadiusPX - border.WidthPX; inner > clipRadius {
			clipRadius = inner
		}
	}
	if clipRadius > 0 {
		layer.Radius = clipRadius
	}
	if frame.Stroke != nil && frame.Stroke.WidthPX > 0 {
		// The requested stroke follows the actual visible perimeter, including
		// an inset border's inner edge. Use the resolved clip radius, not the
		// optional standalone radius, to keep border+stroke concentric.
		layer.FrameStroke = &LayerStroke{Color: frame.Stroke.Color, Width: frame.Stroke.WidthPX}
	}
	if frame.Shadow != nil {
		shadow := frame.Shadow
		if !isHexColor(shadow.Color) {
			return fmt.Errorf("frame shadow color %q must be #RRGGBB", shadow.Color)
		}
		if math.IsNaN(shadow.Opacity) || shadow.Opacity < 0 || shadow.Opacity > 1 {
			return fmt.Errorf("frame shadow opacity must be within [0,1]")
		}
		if math.IsNaN(shadow.BlurPX) || shadow.BlurPX < 0 || shadow.BlurPX > maxFrameShadowBlur {
			return fmt.Errorf("frame shadow blur_px must be within [0,%g]", maxFrameShadowBlur)
		}
		if math.IsNaN(shadow.OffsetXP) || math.Abs(shadow.OffsetXP) > maxFrameShadowOffset ||
			math.IsNaN(shadow.OffsetYP) || math.Abs(shadow.OffsetYP) > maxFrameShadowOffset {
			return fmt.Errorf("frame shadow offsets must be within ±%g", maxFrameShadowOffset)
		}
		style := ensureLayerStyle(layer)
		style.Shadow = &LayerShadow{
			Color:   shadow.Color,
			Opacity: shadow.Opacity,
			Blur:    shadow.BlurPX,
			Offset:  []float64{shadow.OffsetXP, shadow.OffsetYP},
		}
	}
	return nil
}

// compileMediaStrokeLayer expands transient frame-stroke metadata into a
// Chronon rounded-rectangle path which sits above its media layer. Its fill is
// transparent, while the visible line follows the same radius as the clip.
func compileMediaStrokeLayer(media Layer, canvasWidth, canvasHeight int) (Layer, error) {
	if media.FrameStroke == nil || media.FrameStroke.Width <= 0 {
		return Layer{}, fmt.Errorf("media frame stroke is missing a positive width")
	}
	if len(media.Size) != 2 || media.Size[0] <= 0 || media.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("media frame stroke requires a resolved two-dimensional box")
	}
	scaleX, scaleY := 1.0, 1.0
	if len(media.Scale) >= 2 {
		scaleX, scaleY = math.Abs(media.Scale[0]), math.Abs(media.Scale[1])
	}
	if scaleX <= 0 || scaleY <= 0 || math.IsNaN(scaleX) || math.IsNaN(scaleY) || math.IsInf(scaleX, 0) || math.IsInf(scaleY, 0) {
		return Layer{}, fmt.Errorf("media frame stroke scale must be finite and positive")
	}
	width, height := media.Size[0]*scaleX, media.Size[1]*scaleY
	if math.IsNaN(width) || math.IsInf(width, 0) || math.IsNaN(height) || math.IsInf(height, 0) || width <= 0 || height <= 0 {
		return Layer{}, fmt.Errorf("media frame stroke dimensions must be finite and positive")
	}
	strokeWidth := media.FrameStroke.Width
	strokeRadius := media.Radius
	position := append([]float64(nil), media.Position...)
	if media.Type == "video" && len(position) >= 2 {
		if canvasWidth <= 0 || canvasHeight <= 0 {
			return Layer{}, fmt.Errorf("video frame stroke requires positive canvas dimensions")
		}
		// Render-plan video positions name the source surface origin; shape
		// positions name the box center. The source clip geometry is transformed
		// by size×scale and then centered on the canvas for inset foregrounds.
		position[0] += (width - float64(canvasWidth)) * 0.5
		position[1] += (height - float64(canvasHeight)) * 0.5
	}
	if math.IsNaN(strokeWidth) || math.IsInf(strokeWidth, 0) || math.IsNaN(strokeRadius) || math.IsInf(strokeRadius, 0) {
		return Layer{}, fmt.Errorf("media frame stroke width/radius must be finite")
	}
	style := *media.FrameStroke
	style.Width = strokeWidth
	// Stroke width and radius are declared in output pixels. Bake the media
	// scale into the shape bounds and keep the shape itself at unit scale so
	// Chronon does not shrink the outline (or corner radius) with the clip.
	strokeLayer := Layer{
		ID:             media.ID + "__stroke",
		Type:           "shape",
		Size:           []float64{width, height},
		Position:       position,
		Scale:          []float64{1, 1},
		Radius:         strokeRadius,
		Color:          []float64{0, 0, 0, 0},
		StartFrame:     media.StartFrame,
		DurationFrames: media.DurationFrames,
		Opacity:        media.Opacity,
		Shape: &LayerShape{
			Type: "rounded_rect", Radius: strokeRadius,
			Stroke: &style,
		},
		Animation: media.Animation,
	}
	return strokeLayer, nil
}
func ensureLayerStyle(layer *Layer) *LayerStyle {
	if layer.Style == nil {
		layer.Style = &LayerStyle{}
	}
	return layer.Style
}

// isHexColor accepts exactly the #RRGGBB form the render plan's colour
// properties declare (the engine parses no other spelling).
func isHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, digit := range value[1:] {
		switch {
		case digit >= '0' && digit <= '9':
		case digit >= 'a' && digit <= 'f':
		case digit >= 'A' && digit <= 'F':
		default:
			return false
		}
	}
	return true
}
