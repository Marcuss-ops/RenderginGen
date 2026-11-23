package overlay

import (
	"fmt"
	"math"
	"strings"
)

// Bounds mirror the published semantic source-frame contract.
const (
	maxFrameBorderWidth  float64 = 512
	maxFrameRadius       float64 = 512
	maxFrameStrokeWidth  float64 = 64
	maxFrameShadowBlur   float64 = 256
	maxFrameShadowOffset float64 = 256
	// maxFrameShadows mirrors the renderer's style contract, which accepts at
	// most three drop shadows per layer.
	maxFrameShadows = 3
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
	if frame.Shadow != nil && len(frame.Shadows) > 0 {
		return fmt.Errorf("frame accepts either shadow or shadows, not both")
	}
	if len(frame.Shadows) > maxFrameShadows {
		return fmt.Errorf("frame shadows accepts at most %d entries", maxFrameShadows)
	}
	if frame.Border == nil && frame.Shadow == nil && len(frame.Shadows) == 0 &&
		frame.Stroke == nil && frame.ClipRadiusPX == 0 {
		return fmt.Errorf("frame requires border, shadow, shadows, stroke, or clip_radius_px")
	}
	if frame.Stroke != nil {
		stroke := frame.Stroke
		if !finite(stroke.WidthPX) || stroke.WidthPX < 0 || stroke.WidthPX > maxFrameStrokeWidth {
			return fmt.Errorf("frame stroke width_px must be within [0,%g]", maxFrameStrokeWidth)
		}
		if !isHexColor(stroke.Color) {
			return fmt.Errorf("frame stroke color %q must be #RRGGBB", stroke.Color)
		}
	}
	// Validate clip_radius_px range (same ceiling as border.radius_px).
	if !finite(frame.ClipRadiusPX) || frame.ClipRadiusPX < 0 || frame.ClipRadiusPX > maxFrameRadius {
		return fmt.Errorf("frame clip_radius_px must be within [0,%g]", maxFrameRadius)
	}
	// Start with the standalone clip bounding-box radius.
	clipRadius := frame.ClipRadiusPX
	if frame.Border != nil {
		border := frame.Border
		if !finite(border.WidthPX) || border.WidthPX < 0 || border.WidthPX > maxFrameBorderWidth {
			return fmt.Errorf("frame border width_px must be within [0,%g]", maxFrameBorderWidth)
		}
		if !finite(border.RadiusPX) || border.RadiusPX < 0 || border.RadiusPX > maxFrameRadius {
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
		lowered, err := lowerFrameShadow(frame.Shadow, "frame shadow")
		if err != nil {
			return err
		}
		ensureLayerStyle(layer).Shadow = lowered
	}
	if len(frame.Shadows) > 0 {
		lowered := make([]LayerShadow, 0, len(frame.Shadows))
		for index := range frame.Shadows {
			label := fmt.Sprintf("frame shadows[%d]", index)
			shadow, err := lowerFrameShadow(&frame.Shadows[index], label)
			if err != nil {
				return err
			}
			lowered = append(lowered, *shadow)
		}
		ensureLayerStyle(layer).Shadows = lowered
	}
	return nil
}

// lowerFrameShadow validates one semantic shadow declaration and lowers it to
// the renderer's style contract. It is the single owner of the per-shadow
// bounds: the singular `shadow` and every entry of `shadows` go through it, so
// a stack entry can never be validated more loosely than the single form.
func lowerFrameShadow(shadow *semanticFrameShadow, label string) (*LayerShadow, error) {
	if !isHexColor(shadow.Color) {
		return nil, fmt.Errorf("%s color %q must be #RRGGBB", label, shadow.Color)
	}
	if !finite(shadow.Opacity) || shadow.Opacity < 0 || shadow.Opacity > 1 {
		return nil, fmt.Errorf("%s opacity must be within [0,1]", label)
	}
	if !finite(shadow.BlurPX) || shadow.BlurPX < 0 || shadow.BlurPX > maxFrameShadowBlur {
		return nil, fmt.Errorf("%s blur_px must be within [0,%g]", label, maxFrameShadowBlur)
	}
	if !finite(shadow.OffsetXP) || math.Abs(shadow.OffsetXP) > maxFrameShadowOffset ||
		!finite(shadow.OffsetYP) || math.Abs(shadow.OffsetYP) > maxFrameShadowOffset {
		return nil, fmt.Errorf("%s offsets must be within ±%g", label, maxFrameShadowOffset)
	}
	return &LayerShadow{
		Color:   shadow.Color,
		Opacity: shadow.Opacity,
		Blur:    shadow.BlurPX,
		Offset:  []float64{shadow.OffsetXP, shadow.OffsetYP},
	}, nil
}

// frameFromImageParams synthesizes a card Frame from the flat image runtime
// overrides PipelineGen emits (overlay_style.image.* trimmed of its image_
// prefix). An explicit Frame block stays authoritative; flat keys fill only
// the fields the Frame leaves empty, so payloads can tune border width/color,
// stroke, shadow (color/opacity/blur/offsets) and clip radius without
// resending the whole card declaration. Bounded/fit/positions are geometry
// and ride the image layer directly, not the Frame.
func frameFromImageParams(params map[string]any, existing *semanticSourceFrame) *semanticSourceFrame {
	if len(params) == 0 {
		return existing
	}
	has := func(key string) bool {
		_, ok := params[key]
		return ok
	}
	needsFrame := existing != nil
	for _, key := range []string{"border_width", "border_color", "border_radius", "stroke_width", "stroke_color", "shadow_enabled", "shadow_color", "shadow_opacity", "shadow_blur", "shadow_offset_x", "shadow_offset_y", "radius"} {
		if has(key) {
			needsFrame = true
			break
		}
	}
	if !needsFrame {
		return existing
	}
	out := &semanticSourceFrame{}
	if existing != nil {
		*out = *existing
	}
	if width, ok := numericValue(params["border_width"]); ok || has("border_color") || has("border_radius") {
		if out.Border == nil {
			out.Border = &semanticFrameBorder{}
		}
		if w, ok := numericValue(params["border_width"]); ok {
			out.Border.WidthPX = w
		}
		_ = width
		if color, ok := params["border_color"].(string); ok && color != "" {
			out.Border.Color = color
		}
		if radius, ok := numericValue(params["border_radius"]); ok {
			out.Border.RadiusPX = radius
		}
		if out.Border.Color == "" {
			out.Border.Color = "#111827"
		}
	}
	if _, ok := numericValue(params["stroke_width"]); ok || has("stroke_color") {
		if out.Stroke == nil {
			out.Stroke = &semanticFrameStroke{}
		}
		if w, ok := numericValue(params["stroke_width"]); ok {
			out.Stroke.WidthPX = w
		}
		if color, ok := params["stroke_color"].(string); ok && color != "" {
			out.Stroke.Color = color
		}
		if out.Stroke.Color == "" {
			out.Stroke.Color = "#FFFFFF"
		}
	}
	// An explicit `frame.shadows` stack is authoritative: the flat single-shadow
	// keys cannot describe more than one shadow, and mixing the two forms would
	// produce a declaration the renderer rejects outright.
	if len(out.Shadows) > 0 && (has("shadow_enabled") || has("shadow_color") ||
		has("shadow_opacity") || has("shadow_blur") || has("shadow_offset_x") ||
		has("shadow_offset_y")) {
		return out
	}
	if has("shadow_enabled") || has("shadow_color") || has("shadow_opacity") || has("shadow_blur") || has("shadow_offset_x") || has("shadow_offset_y") {
		if out.Shadow == nil {
			out.Shadow = &semanticFrameShadow{Color: "#000000"}
		}
		if enabled, ok := params["shadow_enabled"].(bool); ok && !enabled {
			return &semanticSourceFrame{ClipRadiusPX: out.ClipRadiusPX, Border: out.Border, Stroke: out.Stroke}
		}
		if color, ok := params["shadow_color"].(string); ok && color != "" {
			out.Shadow.Color = color
		}
		if opacity, ok := numericValue(params["shadow_opacity"]); ok {
			out.Shadow.Opacity = opacity
		}
		if blur, ok := numericValue(params["shadow_blur"]); ok {
			out.Shadow.BlurPX = blur
		}
		if offsetX, ok := numericValue(params["shadow_offset_x"]); ok {
			out.Shadow.OffsetXP = offsetX
		}
		if offsetY, ok := numericValue(params["shadow_offset_y"]); ok {
			out.Shadow.OffsetYP = offsetY
		}
		if out.Shadow.Color == "" {
			out.Shadow.Color = "#000000"
		}
	}
	if radius, ok := numericValue(params["radius"]); ok && out.ClipRadiusPX == 0 {
		out.ClipRadiusPX = radius
	}
	if out.Border == nil && out.Shadow == nil && out.Stroke == nil && out.ClipRadiusPX == 0 {
		return existing
	}
	return out
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
	if scaleX <= 0 || scaleY <= 0 || !finite(scaleX) || !finite(scaleY) {
		return Layer{}, fmt.Errorf("media frame stroke scale must be finite and positive")
	}
	width, height := media.Size[0]*scaleX, media.Size[1]*scaleY
	if !finite(width) || !finite(height) || width <= 0 || height <= 0 {
		return Layer{}, fmt.Errorf("media frame stroke dimensions must be finite and positive")
	}
	strokeWidth := media.FrameStroke.Width
	strokeRadius := media.Radius
	position := make([]float64, 2)
	if len(media.Position) >= 2 {
		position[0], position[1] = media.Position[0], media.Position[1]
	}
	if media.Type == "video" {
		// Video positions name the top-left of the unscaled source box in
		// canvas-centred coordinates; normalise to the box centre.
		position[0] += media.Size[0] * 0.5
		position[1] += media.Size[1] * 0.5
	}
	// Shape positions are absolute canvas coordinates naming the box
	// top-left. The clip is centred on the canvas, so the outline starts at
	// the canvas centre minus half the displayed (already scaled) box.
	position[0] += float64(canvasWidth)*0.5 - width*0.5
	position[1] += float64(canvasHeight)*0.5 - height*0.5
	if !finite(strokeWidth) || !finite(strokeRadius) {
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
		// Screen-space keeps the outline in the same compositing domain as the
		// clip; shape positions are then absolute canvas coordinates.
		ScreenSpace: true,
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

// Background plate lowering. The plate
// is top-left anchored and screen-space. Position is explicit because the three
// plate kinds do not share one position contract (see canvasBoxPosition):
// image/color values are canvas-centre offsets, while a video value names the
// box top-left in canvas-centred coordinates and the engine adds half the box.
// Extracted from compileSemanticDecoded so the plan stages stay one call each.

// maxBackgroundBlur bounds the gaussian blur a background plate may declare.
// It is the same ceiling as the shared layer-effect catalog, so a plan the
// schema accepts can never be rejected by the effect resolver.
const maxBackgroundBlur = 256.0

// maxBackgroundZoom bounds the plate scale. It matches the clip-render
// contract's own ceiling, so a request accepted there is always renderable.
const maxBackgroundZoom = 10.0

// compileBackgroundLayer returns the background plate when the plan declares
// one. Fit is validated against the closed render contract and never silently
// downgraded.
func compileBackgroundLayer(src *semanticPlan, registry *assetRegistry) (Layer, bool, error) {
	bg := src.Background
	if bg == nil {
		return Layer{}, false, nil
	}
	kind := strings.ToLower(strings.TrimSpace(bg.Kind))
	// blur_cover is a clip-render-specific fit hint; store it as "video"
	// type in the layer with the fit preserved.
	layerKind := kind
	if kind == "blur_cover" {
		layerKind = "video"
	}
	if layerKind != "color" && layerKind != "image" && layerKind != "video" {
		return Layer{}, false, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
	}
	if layerKind == "color" {
		if len(bg.Color) != 4 {
			return Layer{}, false, fmt.Errorf("overlay: background color requires RGBA[4]")
		}
	} else if len(bg.AssetRefs) == 0 {
		return Layer{}, false, fmt.Errorf("overlay: %s background requires asset_refs", kind)
	}
	if !finite(bg.BlurPX) || bg.BlurPX < 0 || bg.BlurPX > maxBackgroundBlur {
		return Layer{}, false, fmt.Errorf("overlay: background blur_px must be within [0,%g]", maxBackgroundBlur)
	}
	if layerKind == "color" && bg.BlurPX > 0 {
		return Layer{}, false, fmt.Errorf("overlay: background blur_px is only valid for image/video plates")
	}
	if !finite(bg.Zoom) || bg.Zoom < 0 || bg.Zoom > maxBackgroundZoom {
		return Layer{}, false, fmt.Errorf("overlay: background zoom must be within [0,%g]", maxBackgroundZoom)
	}
	if bg.Zoom > 0 && bg.Zoom < 1 {
		return Layer{}, false, fmt.Errorf("overlay: background zoom must be >= 1 when declared")
	}
	if layerKind == "color" && bg.Zoom > 0 {
		return Layer{}, false, fmt.Errorf("overlay: background zoom is only valid for image/video plates")
	}
	for _, ref := range bg.AssetRefs {
		if _, err := registry.Register(ref); err != nil {
			return Layer{}, false, fmt.Errorf("overlay: background asset: %w", err)
		}
	}
	// Every plate is anchored at the canvas top-left in its own contract.
	// Image and color layers take a centre offset, so [0,0] is the full
	// canvas. A video layer takes the box top-left in canvas-centred
	// coordinates and the engine adds half the box, so a full-canvas video
	// needs [-W/2,-H/2]; [0,0] puts its centre at (+W/2,+H/2), i.e. the
	// plate covers only the bottom-right quadrant. Keep the plate in screen
	// space as well: when any text layer enables the 2.5D camera, the
	// background must not enter the projected layer/depth bin.
	position := []float64{0, 0}
	if layerKind == "video" {
		position = canvasBoxPosition("video", 0, 0,
			float64(src.Width), float64(src.Height), src.Width, src.Height)
	}
	layer := Layer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
		Size:     []float64{float64(src.Width), float64(src.Height)},
		Position: position, ScreenSpace: true,
		Fit: bg.Fit, StartFrame: 0}
	if layerKind != "color" {
		// One gate for the background fit: validated against the closed set
		// the render contract honours, defaulted to the deterministic
		// crop-to-fill, never silently downgraded.
		fit, err := resolveBackgroundFit(bg.Fit)
		if err != nil {
			return Layer{}, false, err
		}
		layer.Fit = fit
	}
	if bg.Opacity != nil {
		// Declared opacity (including an explicit 0) is carried verbatim as
		// a pointer so the wire cannot lose the "invisible" contract.
		opacity := *bg.Opacity
		layer.Opacity = &opacity
	}
	if layerKind == "color" {
		layer.Color = append([]float64(nil), bg.Color...)
	} else {
		ref := bg.AssetRefs[0]
		if layerKind == "video" {
			layer.Source = registry.Path(ref.ID)
		} else {
			layer.Asset = registry.Path(ref.ID)
		}
		layer.Loop = bg.Loop
		if bg.Zoom > 0 {
			// Scale is the renderer's transform for a plate anchored at the
			// canvas top-left, exactly like the verified image_backdrop path.
			// Without it a zoomed request would render invisibly unchanged.
			layer.Scale = []float64{bg.Zoom, bg.Zoom}
		}
		if bg.BlurPX > 0 {
			// `blur_source` is expressed as the source plate plus this real
			// compositor effect. Without it the plate is a sharp, differently
			// scaled copy of the foreground and the card edge reads as a
			// duplicated/glitched frame.
			layer.Effects = append(layer.Effects, LayerEffect{
				Type:   "gaussian_blur",
				Params: map[string]any{"radius": bg.BlurPX},
			})
		}
	}
	return layer, true, nil
}
