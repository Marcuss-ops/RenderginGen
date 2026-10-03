package overlay

import (
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// EntityCardLayoutResolver resolves the geometry of an entity card — image
// bounds, caption bounds and the caption anchor — from the canvas, the image
// geometry and the caption length. It replaces the hardcoded caption offsets
// the two entity lowering paths used to restate: the caption is anchored to
// the image, never to a canvas constant, and the resolver is the only place
// that knows how.
//
// The resolver is pure geometry: it never touches presets, motions or assets.
// Callers hand it the resolved image rect in canvas coordinates and receive
// back the caption rect plus its anchor relative to the image, so an entity
// card and a composite image child lay out identically.
//
// Safe area: the caption must stay inside the canvas minus
// safeMarginPX on every side, inside the vertical title-safe band
// (safeTopPX..safeBottomPX), and horizontally centered on the image unless
// clamping against the safe area forces a shift.
const (
	// entityCaptionMarginPX is the gap between the image bottom edge and the
	// caption top edge. The caption anchors at image.bottom_center + margin.
	entityCaptionMarginPX = 28.0
	// entityCaptionSafeMarginPX is the minimum distance the caption box keeps
	// from the left/right canvas edges.
	entityCaptionSafeMarginPX = 24.0
	// entityCaptionSafeTopPX / entityCaptionSafeBottomPX bound the caption
	// center vertically so a card near the canvas edge never slides its label
	// into the letterbox or off-frame.
	entityCaptionSafeTopPX    = 72.0
	entityCaptionSafeBottomPX = 72.0
	// entityCaptionMaxHeightPX is the caption box height handed to the text
	// compiler; the box is the hit area, not the text run.
	entityCaptionMaxHeightPX = 88.0
	// entityCaptionMinFontPX / entityCaptionMaxFontPX bound the font fitting
	// for long names: the text scales down before it truncates, and it never
	// truncates.
	entityCaptionMinFontPX = 20.0
	entityCaptionMaxFontPX = 30.0
)

// EntityCardImageBounds is the resolved image rect of an entity card, in
// canvas pixel coordinates with the top-left origin Chronon render plans use.
type EntityCardImageBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// CenterX / CenterY / Bottom / Top are the derived edges the caption anchors
// against. Bottom is the image's bottom edge in canvas coordinates.
func (b EntityCardImageBounds) CenterX() float64 { return b.X + b.Width/2 }
func (b EntityCardImageBounds) CenterY() float64 { return b.Y + b.Height/2 }
func (b EntityCardImageBounds) Bottom() float64  { return b.Y + b.Height }
func (b EntityCardImageBounds) Top() float64     { return b.Y }

// EntityCardCaptionAnchor names where the caption sits relative to the image.
// It is emitted as semantic metadata so producers can assert the relationship
// without re-deriving geometry.
type EntityCardCaptionAnchor struct {
	// Reference is the image edge the caption hangs from.
	Reference string `json:"reference"` // "bottom_center"
	// Margin is the vertical gap between that edge and the caption box top.
	Margin float64 `json:"margin"`
}

// EntityCardCaptionBounds is the resolved caption rect in canvas coordinates.
type EntityCardCaptionBounds struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	CenterX  float64 `json:"center_x"`
	CenterY  float64 `json:"center_y"`
	FontSize float64 `json:"font_size"`
	Top      float64 `json:"top"`
}

// EntityCardSafeBounds is the area the caption must stay inside.
type EntityCardSafeBounds struct {
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
}

// EntityCardLayout is everything an entity lowering needs to place image and
// caption: the two rects, the anchor relation and the safe area they were
// resolved against.
type EntityCardLayout struct {
	ImageBounds      EntityCardImageBounds   `json:"image_bounds"`
	CaptionBounds    EntityCardCaptionBounds `json:"caption_bounds"`
	CaptionAnchor    EntityCardCaptionAnchor `json:"caption_anchor"`
	SafeBounds       EntityCardSafeBounds    `json:"safe_bounds"`
	CaptionPreserved bool                    `json:"caption_preserved"`
}

// ResolveEntityCardLayout derives the entity card geometry. imageRect is the
// image's placed rect in canvas coordinates (Chronon layers are positioned by
// center, so callers use EntityCardImageBoundsFromCenter). canvasWidth and
// canvasHeight are the plan canvas. caption is the preserved entity name; an
// empty caption resolves the image-only layout and reports
// CaptionPreserved=false. The caption fits long names by shrinking the font
// toward entityCaptionMinFontPX before the box width is clamped by the safe
// area — the name is never cut.
func ResolveEntityCardLayout(canvasWidth, canvasHeight int, imageRect EntityCardImageBounds, caption string) (EntityCardLayout, error) {
	if canvasWidth <= 0 || canvasHeight <= 0 {
		return EntityCardLayout{}, fmt.Errorf("overlay: entity card layout needs a positive canvas, got %dx%d", canvasWidth, canvasHeight)
	}
	if imageRect.Width <= 0 || imageRect.Height <= 0 {
		return EntityCardLayout{}, fmt.Errorf("overlay: entity card layout needs positive image geometry, got %vx%v", imageRect.Width, imageRect.Height)
	}
	safe := EntityCardSafeBounds{
		Left:   entityCaptionSafeMarginPX,
		Right:  float64(canvasWidth) - entityCaptionSafeMarginPX,
		Top:    entityCaptionSafeTopPX,
		Bottom: float64(canvasHeight) - entityCaptionSafeBottomPX,
	}
	if safe.Right <= safe.Left || safe.Bottom <= safe.Top {
		return EntityCardLayout{}, fmt.Errorf("overlay: entity card layout canvas %dx%d leaves no safe area", canvasWidth, canvasHeight)
	}

	layout := EntityCardLayout{
		ImageBounds:      imageRect,
		SafeBounds:       safe,
		CaptionPreserved: caption != "",
		CaptionAnchor: EntityCardCaptionAnchor{
			Reference: "bottom_center",
			Margin:    entityCaptionMarginPX,
		},
	}
	if caption == "" {
		return layout, nil
	}

	// Long names fit by shrinking the font first, then by clamping the box
	// width to the safe area. The text compiler wraps within the box, so a
	// clamped box keeps every rune on screen.
	fontSize := entityCaptionMaxFontPX
	maxWidth := safe.Right - safe.Left
	width := captionBoxWidth(caption, fontSize)
	for width > maxWidth && fontSize > entityCaptionMinFontPX {
		fontSize -= 1
		width = captionBoxWidth(caption, fontSize)
	}
	if width > maxWidth {
		width = maxWidth
	}

	// Anchor: image.bottom_center + margin. The caption box top starts one
	// margin below the image bottom edge; its horizontal center tracks the
	// image center unless clamping keeps the box inside the safe area.
	captionTop := imageRect.Bottom() + entityCaptionMarginPX
	captionHeight := entityCaptionMaxHeightPX
	// Vertical fallback: when image + margin + caption runs past the bottom
	// limit, the IMAGE moves up — the card is the pair, so the caption never
	// slides off-frame and the anchor relation never breaks. The shift is
	// clamped so the image never rises above the safe top band; a card that
	// cannot fit either way keeps the caption's bottom on the limit.
	bottomLimit := float64(canvasHeight) - entityCaptionSafeMarginPX
	if captionTop+captionHeight > bottomLimit {
		// The image rises as far as needed (never above the safe top band) so
		// image + margin + caption fits; when even safe.Top cannot free enough
		// space, the caption keeps its clamped position on the bottom limit.
		bestTop := bottomLimit - imageRect.Height - entityCaptionMarginPX - captionHeight
		imageTop := math.Min(imageRect.Top(), bestTop)
		if imageTop >= safe.Top {
			imageRect.Y = imageTop
			layout.ImageBounds.Y = imageTop
			captionTop = imageTop + imageRect.Height + entityCaptionMarginPX
		}
	}

	centerX := imageRect.CenterX()
	halfWidth := width / 2
	centerX = math.Max(safe.Left+halfWidth, math.Min(safe.Right-halfWidth, centerX))

	layout.CaptionBounds = EntityCardCaptionBounds{
		X:        centerX - halfWidth,
		Y:        captionTop,
		Width:    width,
		Height:   captionHeight,
		CenterX:  centerX,
		CenterY:  captionTop + captionHeight/2,
		FontSize: fontSize,
		Top:      captionTop,
	}
	return layout, nil
}

// EntityCardImageBoundsFromCenter converts Chronon's center-positioned image
// layer (position relative to the canvas center) into the resolver's top-left
// rect. position may be nil, which means the centered default.
func EntityCardImageBoundsFromCenter(canvasWidth, canvasHeight int, position []float64, width, height float64) EntityCardImageBounds {
	if width <= 0 || height <= 0 {
		return EntityCardImageBounds{}
	}
	if len(position) < 2 {
		position = []float64{0, 0}
	}
	return EntityCardImageBounds{
		X:      float64(canvasWidth)/2 + position[0] - width/2,
		Y:      float64(canvasHeight)/2 + position[1] - height/2,
		Width:  width,
		Height: height,
	}
}

// captionBoxWidth estimates the rendered caption box width from the caption
// text and font size. It is an upper bound in the Inter/Poppins metrics the
// entity presets use: a wide average advance keeps the box honest for both
// short and long names, and rune counting (not byte counting) keeps Unicode
// names — CJK, Cyrillic, Arabic — measured by what is displayed.
func captionBoxWidth(caption string, fontSize float64) float64 {
	runes := utf8.RuneCountInString(caption)
	if runes == 0 {
		return 0
	}
	// 0.62em average advance plus the rounded background padding (2x16).
	return float64(runes)*fontSize*0.62 + 32
}

// EntityCaptionMotionID returns the caption's motion id, resolving the shared
// default when the producer did not pick one. The caption is a first-class
// animated layer: it may carry its own motion (text_word_rise, text_fade_up,
// …) through the same canonical catalog the image uses.
func EntityCaptionMotionID(requested string) string {
	if requested != "" {
		return requested
	}
	return "text_fade_up"
}

// entityCaptionMotionAllowed is the closed set of text motions a caption may
// ride. The catalog is the vocabulary authority; this gate only refuses
// motions that are not text targets at all (an image motion on a caption
// would lower to tracks the text layer cannot honor).
func entityCaptionMotionAllowed(id string) bool {
	for _, candidate := range motion.Registry.EntityCaptionV1MotionIDs() {
		if candidate == id {
			return true
		}
	}
	return false
}
