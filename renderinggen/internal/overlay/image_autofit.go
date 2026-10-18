package overlay

import (
	"fmt"
	"math"
)

// ImageSlot is a card slot in the 1920x1080 design space. CenterX/CenterY
// are canvas-center offsets; Width/Height is the supported card size for
// the image that owns the slot.
type ImageSlot struct {
	CenterX float64
	CenterY float64
	Width   float64
	Height  float64
}

// Certified group slot geometry, in 1920x1080 design space. These mirror
// the ChrononTemplate multiple_images showcase suites exactly (duo 620x720
// at x=480/1440, trio 500x680, quad 540x400 2x2 grid, penta 320x580 strip),
// so certified renders already land on their slot and auto-fit is a no-op
// for them.
func groupMemberSlot(count, index int) (ImageSlot, bool) {
	switch count {
	case 2:
		slots := []ImageSlot{
			{CenterX: -480, CenterY: 0, Width: 620, Height: 720},
			{CenterX: 480, CenterY: 0, Width: 620, Height: 720},
		}
		if index < 0 || index >= len(slots) {
			return ImageSlot{}, false
		}
		return slots[index], true
	case 3:
		slots := []ImageSlot{
			{CenterX: -540, CenterY: 0, Width: 500, Height: 680},
			{CenterX: 0, CenterY: 0, Width: 500, Height: 680},
			{CenterX: 540, CenterY: 0, Width: 500, Height: 680},
		}
		if index < 0 || index >= len(slots) {
			return ImageSlot{}, false
		}
		return slots[index], true
	case 4:
		slots := []ImageSlot{
			{CenterX: -360, CenterY: -225, Width: 540, Height: 400},
			{CenterX: 360, CenterY: -225, Width: 540, Height: 400},
			{CenterX: -360, CenterY: 225, Width: 540, Height: 400},
			{CenterX: 360, CenterY: 225, Width: 540, Height: 400},
		}
		if index < 0 || index >= len(slots) {
			return ImageSlot{}, false
		}
		return slots[index], true
	case 5:
		slots := []ImageSlot{
			{CenterX: -680, CenterY: 0, Width: 320, Height: 580},
			{CenterX: -340, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 0, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 340, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 680, CenterY: 0, Width: 320, Height: 580},
		}
		if index < 0 || index >= len(slots) {
			return ImageSlot{}, false
		}
		return slots[index], true
	}
	return ImageSlot{}, false
}

// entityPortraitRatio is the supported portrait ratio for an entity card
// image with text: landscape boxes are covered into it, keeping their
// height and center so the caption layout below/beside keeps working.
const entityPortraitRatio = 0.75

// autoFitLayerToSlot moves an image layer into its supported card slot,
// scaled proportionally to the render canvas. Non-vertical (landscape or
// square) boxes are covered into the slot so any nearby pair renders side
// by side. It reports whether it changed anything: layers that already sit
// on their slot are returned untouched, so certified renders are bit-stable.
func autoFitLayerToSlot(src *semanticPlan, layer *Layer, slot ImageSlot) bool {
	if src == nil || layer == nil || src.Width <= 0 || src.Height <= 0 {
		return false
	}
	if slot.Width <= 0 || slot.Height <= 0 {
		return false
	}
	sx := float64(src.Width) / 1920.0
	sy := float64(src.Height) / 1080.0
	wantSize := []float64{slot.Width * sx, slot.Height * sy}
	wantPosition := []float64{slot.CenterX * sx, slot.CenterY * sy}
	if layerSizeMatchesSlot(layer, wantSize) && layerPositionMatchesSlot(layer, wantPosition) && layer.Fit == FitCover {
		return false
	}
	layer.Size = wantSize
	layer.Position = wantPosition
	layer.Fit = FitCover
	return true
}

func layerSizeMatchesSlot(layer *Layer, want []float64) bool {
	if len(layer.Size) < 2 || len(want) < 2 {
		return false
	}
	return math.Abs(layer.Size[0]-want[0]) < 0.5 && math.Abs(layer.Size[1]-want[1]) < 0.5
}

func layerPositionMatchesSlot(layer *Layer, want []float64) bool {
	if len(layer.Position) < 2 || len(want) < 2 {
		return false
	}
	return math.Abs(layer.Position[0]-want[0]) < 0.5 && math.Abs(layer.Position[1]-want[1]) < 0.5
}

// autoFitGroupMember fits one composite image-group member into its recipe
// slot. Unknown counts or indices fail closed (no silent reflow).
func autoFitGroupMember(src *semanticPlan, layer *Layer, count, index int) (bool, error) {
	slot, ok := groupMemberSlot(count, index)
	if !ok {
		return false, fmt.Errorf("overlay: no supported card slot for group of %d images at index %d", count, index)
	}
	return autoFitLayerToSlot(src, layer, slot), nil
}

// autoFitEntityPortrait covers a non-vertical entity card image into the
// supported portrait box, keeping its height and center: the caption
// compiled from its bounds follows automatically. Already-vertical images
// are returned untouched.
func autoFitEntityPortrait(layer *Layer) bool {
	if layer == nil || len(layer.Size) < 2 || layer.Size[0] <= 0 || layer.Size[1] <= 0 {
		return false
	}
	if layer.Size[0] < layer.Size[1] {
		return false
	}
	layer.Size = []float64{layer.Size[1] * entityPortraitRatio, layer.Size[1]}
	layer.Fit = FitCover
	return true
}
