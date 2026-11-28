package editoriallayers

import (
	"fmt"
	"math"
)

// Rect is a resolved canvas-space layer bound. Coordinates use a top-left
// origin, matching the geometry supplied by the renderer's layout pass.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// Size is the measured, final size of one attachment after font and asset
// resolution. The resolver never guesses a size from its text or asset ID.
type Size struct {
	Width  float64
	Height float64
}

// LayoutContext is supplied after the renderer has resolved the canvas and
// parent layer geometry. Obstacles are already-placed, non-parent layers that
// an attachment must not cover.
type LayoutContext struct {
	CanvasWidth  float64
	CanvasHeight float64
	SafeInset    float64
	Gap          float64
	LayerBounds  map[string]Rect
	LayerSizes   map[string]Size
	Obstacles    []Rect
}

// PhysicalPlacement is a renderer-resolved placement for one attachment.
type PhysicalPlacement struct {
	LayerID       string
	ParentLayerID string
	Position      string
	Bounds        Rect
}

// ResolveAttachmentPlacements chooses physical positions only after final
// parent bounds, canvas dimensions, and attachment sizes are available. It
// respects the safe inset, avoids supplied obstacles and previously placed
// attachments, and fails closed when no candidate fits.
func (p *Plan) ResolveAttachmentPlacements(context LayoutContext) ([]PhysicalPlacement, error) {
	if p == nil || p.Document == nil {
		return nil, fmt.Errorf("editorial layers: plan is required")
	}
	if !finitePositive(context.CanvasWidth) || !finitePositive(context.CanvasHeight) ||
		!finiteNonNegative(context.SafeInset) || context.SafeInset*2 >= math.Min(context.CanvasWidth, context.CanvasHeight) ||
		!finiteNonNegative(context.Gap) {
		return nil, fmt.Errorf("editorial layers: invalid canvas or safe-area geometry")
	}
	safe := Rect{
		X: context.SafeInset, Y: context.SafeInset,
		Width: context.CanvasWidth - 2*context.SafeInset, Height: context.CanvasHeight - 2*context.SafeInset,
	}
	occupied := append([]Rect(nil), context.Obstacles...)
	placements := make([]PhysicalPlacement, 0)
	for _, layer := range p.Document.Layers {
		if _, attachment := attachmentRoles[layer.Role]; !attachment || layer.ParentLayerID == "" {
			continue
		}
		parent, ok := context.LayerBounds[layer.ParentLayerID]
		if !ok || !validRect(parent) {
			return nil, fmt.Errorf("editorial layers: attachment %q has no resolved parent bounds for %q", layer.LayerID, layer.ParentLayerID)
		}
		size, ok := context.LayerSizes[layer.LayerID]
		if !ok || !finitePositive(size.Width) || !finitePositive(size.Height) {
			return nil, fmt.Errorf("editorial layers: attachment %q has no valid measured size", layer.LayerID)
		}
		position, bounds, ok := chooseAttachmentPosition(layer.Position, parent, size, safe, occupied, context.Gap)
		if !ok {
			return nil, fmt.Errorf("editorial layers: attachment %q does not fit in the canvas safe area", layer.LayerID)
		}
		placements = append(placements, PhysicalPlacement{
			LayerID: layer.LayerID, ParentLayerID: layer.ParentLayerID, Position: position, Bounds: bounds,
		})
		occupied = append(occupied, bounds)
	}
	return placements, nil
}

func chooseAttachmentPosition(requested string, parent Rect, size Size, safe Rect, occupied []Rect, gap float64) (string, Rect, bool) {
	positions := []string{requested}
	if requested == "" || requested == "auto" {
		positions = []string{"below", "above", "right", "left"}
	}
	for _, position := range positions {
		candidate, ok := attachmentRect(position, parent, size, gap)
		if !ok || !containsRect(safe, candidate) || overlapsAny(candidate, occupied) {
			continue
		}
		return position, candidate, true
	}
	return "", Rect{}, false
}

func attachmentRect(position string, parent Rect, size Size, gap float64) (Rect, bool) {
	centerX, centerY := parent.X+parent.Width/2, parent.Y+parent.Height/2
	switch position {
	case "below", "auto", "":
		return Rect{X: centerX - size.Width/2, Y: parent.Y + parent.Height + gap, Width: size.Width, Height: size.Height}, true
	case "badge":
		return Rect{X: parent.X + parent.Width - size.Width - gap, Y: parent.Y + gap, Width: size.Width, Height: size.Height}, true
	case "above":
		return Rect{X: centerX - size.Width/2, Y: parent.Y - gap - size.Height, Width: size.Width, Height: size.Height}, true
	case "right":
		return Rect{X: parent.X + parent.Width + gap, Y: centerY - size.Height/2, Width: size.Width, Height: size.Height}, true
	case "left":
		return Rect{X: parent.X - gap - size.Width, Y: centerY - size.Height/2, Width: size.Width, Height: size.Height}, true
	default:
		return Rect{}, false
	}
}

func overlapsAny(candidate Rect, others []Rect) bool {
	for _, other := range others {
		if validRect(other) && candidate.X < other.X+other.Width && candidate.X+candidate.Width > other.X &&
			candidate.Y < other.Y+other.Height && candidate.Y+candidate.Height > other.Y {
			return true
		}
	}
	return false
}

func containsRect(outer, inner Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.X+inner.Width <= outer.X+outer.Width && inner.Y+inner.Height <= outer.Y+outer.Height
}

func validRect(rect Rect) bool {
	return finiteNonNegative(rect.X) && finiteNonNegative(rect.Y) && finitePositive(rect.Width) && finitePositive(rect.Height)
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}
