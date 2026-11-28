package editoriallayers

import (
	"fmt"
	"math"
)

// PinProjection is the resolved screen-space projection of a map pin after
// the renderer's camera pass. One sample is supplied per rendered camera
// sample so a leader can remain attached while the map moves.
type PinProjection struct {
	Frame float64
	X     float64
	Y     float64
}

// LeaderSample is one endpoint pair for the callout's screen-space leader.
type LeaderSample struct {
	Frame float64
	FromX float64
	FromY float64
	ToX   float64
	ToY   float64
}

// MapCalloutPlacement is the stable photo-card box plus the camera-following
// leader samples that connect it to the projected geographic pin.
type MapCalloutPlacement struct {
	LayerID       string
	AnchorPinID   string
	Position      string
	Bounds        Rect
	LeaderSamples []LeaderSample
}

// ResolveMapCalloutGeometry places verified photo callouts after camera
// projection. The card occupies one stable safe-area slot for the full move;
// its leader endpoint follows each projected pin sample. A placement is
// rejected if the map lacks camera samples, a sample is invalid, no safe slot
// fits, or the line would have no visible length.
func (p *Plan) ResolveMapCalloutGeometry(layerID, pinID, requested string, size Size, projections []PinProjection, context LayoutContext) (MapCalloutPlacement, error) {
	if p == nil || p.Document == nil || layerID == "" || pinID == "" {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout plan, layer id, and pin id are required")
	}
	if resolved, ok := p.Callouts[pinID]; !ok || resolved != layerID {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q is not admitted for pin %q", layerID, pinID)
	}
	declared := false
	for _, layer := range p.Document.Layers {
		if layer.LayerID == layerID && layer.Role == RoleMapCallout && layer.AnchorPinID == pinID && layer.Position == requested {
			declared = true
			break
		}
	}
	if !declared {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q placement or anchor differs from its admitted declaration", layerID)
	}
	if len(projections) == 0 || !finitePositive(size.Width) || !finitePositive(size.Height) {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q requires measured size and camera projections", layerID)
	}
	if !finitePositive(context.CanvasWidth) || !finitePositive(context.CanvasHeight) ||
		!finiteNonNegative(context.SafeInset) || context.SafeInset*2 >= math.Min(context.CanvasWidth, context.CanvasHeight) ||
		!finiteNonNegative(context.Gap) {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: invalid camera-aware callout canvas geometry")
	}
	minX, maxX := projections[0].X, projections[0].X
	minY, maxY := projections[0].Y, projections[0].Y
	for i, sample := range projections {
		if math.IsNaN(sample.Frame) || math.IsInf(sample.Frame, 0) || sample.Frame < 0 ||
			!finiteNonNegative(sample.X) || !finiteNonNegative(sample.Y) ||
			sample.X > context.CanvasWidth || sample.Y > context.CanvasHeight ||
			(i > 0 && sample.Frame <= projections[i-1].Frame) {
			return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q has invalid or unordered camera projection at index %d", layerID, i)
		}
		minX, maxX = math.Min(minX, sample.X), math.Max(maxX, sample.X)
		minY, maxY = math.Min(minY, sample.Y), math.Max(maxY, sample.Y)
	}
	trackBounds := Rect{X: minX, Y: minY, Width: math.Max(maxX-minX, 0.000001), Height: math.Max(maxY-minY, 0.000001)}
	safe := Rect{X: context.SafeInset, Y: context.SafeInset, Width: context.CanvasWidth - 2*context.SafeInset, Height: context.CanvasHeight - 2*context.SafeInset}
	positions := []string{requested}
	if requested == "" || requested == "auto" {
		positions = []string{"right", "left", "above", "below"}
	}
	var chosen Rect
	chosenPosition := ""
	for _, position := range positions {
		candidate, ok := calloutRect(position, trackBounds, size, context.Gap)
		if ok && containsRect(safe, candidate) && !overlapsAny(candidate, context.Obstacles) {
			chosen, chosenPosition = candidate, position
			break
		}
	}
	if chosenPosition == "" {
		return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q has no camera-aware safe-area slot", layerID)
	}
	leaders := make([]LeaderSample, 0, len(projections))
	for _, sample := range projections {
		toX, toY := nearestRectPoint(chosen, sample.X, sample.Y)
		if math.Hypot(sample.X-toX, sample.Y-toY) < 1 {
			return MapCalloutPlacement{}, fmt.Errorf("editorial layers: callout %q leader collapses at camera frame %.3f", layerID, sample.Frame)
		}
		leaders = append(leaders, LeaderSample{Frame: sample.Frame, FromX: sample.X, FromY: sample.Y, ToX: toX, ToY: toY})
	}
	return MapCalloutPlacement{LayerID: layerID, AnchorPinID: pinID, Position: chosenPosition, Bounds: chosen, LeaderSamples: leaders}, nil
}

func calloutRect(position string, pinTrack Rect, size Size, gap float64) (Rect, bool) {
	centerX, centerY := pinTrack.X+pinTrack.Width/2, pinTrack.Y+pinTrack.Height/2
	switch position {
	case "right", "auto", "":
		return Rect{X: pinTrack.X + pinTrack.Width + gap, Y: centerY - size.Height/2, Width: size.Width, Height: size.Height}, true
	case "left":
		return Rect{X: pinTrack.X - gap - size.Width, Y: centerY - size.Height/2, Width: size.Width, Height: size.Height}, true
	case "above":
		return Rect{X: centerX - size.Width/2, Y: pinTrack.Y - gap - size.Height, Width: size.Width, Height: size.Height}, true
	case "below":
		return Rect{X: centerX - size.Width/2, Y: pinTrack.Y + pinTrack.Height + gap, Width: size.Width, Height: size.Height}, true
	default:
		return Rect{}, false
	}
}

func nearestRectPoint(rect Rect, x, y float64) (float64, float64) {
	return math.Max(rect.X, math.Min(rect.X+rect.Width, x)), math.Max(rect.Y, math.Min(rect.Y+rect.Height, y))
}
