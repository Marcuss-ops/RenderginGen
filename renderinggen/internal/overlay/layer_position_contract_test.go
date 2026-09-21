package overlay

import (
	"math"
	"testing"
)

// The render-plan `position` field carries TWO different meanings, one per
// layer type, and the engine's decoder (render_plan_compiler_animation.cpp,
// apply_layer_primitives) is the authority on which:
//
//   - text  — the value is the layer centre's ABSOLUTE canvas coordinate; the
//     engine subtracts canvas/2 itself.
//   - other — the value is an offset from the canvas centre.
//
// canvasBoxPosition is this repository's single owner of that conversion.
// Emitting the wrong form for a text layer is not a cosmetic error: the engine
// subtracts half a canvas a second time, so the layer lands half a canvas away
// from the box it was positioned against. That is exactly what shipped — every
// burned subtitle cue was authored at the bottom centre and rendered against
// the top-left corner of the frame, clipped off the left edge.

// TestCanvasBoxPositionMatchesEngineLayerSemantics pins both branches of the
// conversion, and the fact that they differ by half a canvas — the discrepancy
// that a single shared form used to hide.
func TestCanvasBoxPositionMatchesEngineLayerSemantics(t *testing.T) {
	const canvasW, canvasH = 1920, 1080
	// A 320x80 box anchored at (1552,48): a top_right watermark with a 48px
	// margin, which is the shape the watermark test compiles.
	got := struct{ x, y, w, h float64 }{1552, 48, 320, 80}

	text := canvasBoxPosition("text", got.x, got.y, got.w, got.h, canvasW, canvasH)
	if len(text) != 2 || text[0] != 1712 || text[1] != 88 {
		t.Fatalf("text position = %v, want the box centre in absolute canvas coordinates [1712 88]", text)
	}

	image := canvasBoxPosition("image", got.x, got.y, got.w, got.h, canvasW, canvasH)
	if len(image) != 2 || image[0] != 752 || image[1] != -452 {
		t.Fatalf("image position = %v, want a canvas-centre offset [752 -452]", image)
	}

	if math.Abs(text[0]-image[0]) != canvasW/2 || math.Abs(text[1]-image[1]) != canvasH/2 {
		t.Fatalf("text and non-text forms must differ by exactly half a canvas, got %v vs %v", text, image)
	}
}

// TestBurnedCuePositionLandsBottomCenter is the regression guard for the shipped
// P0, with the numbers taken from the plan that produced it: a bottom_center cue
// on 1920x1080 with the standard 1800x70 safe-area box anchored at (60, 829).
//
// The emitted position must be the box centre in absolute canvas coordinates —
// [960, 864] — because that is the form the engine reads for text layers and it
// subtracts canvas/2 itself. The bug emitted the canvas-centre offset [0, 324]
// instead, so the engine resolved the cue's centre to (0, 324): hard against the
// left edge, a third of the way down, which is precisely where the rendered
// frames showed it.
func TestBurnedCuePositionLandsBottomCenter(t *testing.T) {
	const canvasW, canvasH = 1920, 1080
	// The safe-area box subtitleCueGeometry resolves for bottom_center.
	const boxX, boxY, boxW, boxH = 60.0, 829.0, 1800.0, 70.0

	position := canvasBoxPosition("text", boxX, boxY, boxW, boxH, canvasW, canvasH)
	if len(position) != 2 || position[0] != 960 || position[1] != 864 {
		t.Fatalf("burned cue position = %v, want absolute canvas centre [960 864]", position)
	}

	// Replay the engine's text branch: it subtracts canvas/2 and the resulting
	// offset is measured from the canvas centre, so it must reproduce the box
	// centre. This is the arithmetic that failed in production.
	resolvedX := position[0] - canvasW/2 + canvasW/2
	resolvedY := position[1] - canvasH/2 + canvasH/2
	if resolvedX != boxX+boxW/2 || resolvedY != boxY+boxH/2 {
		t.Fatalf("engine resolves cue centre to (%.0f,%.0f), want the box centre (%.0f,%.0f)",
			resolvedX, resolvedY, boxX+boxW/2, boxY+boxH/2)
	}
	// And the cue must sit in the bottom band, horizontally centred.
	if resolvedY < canvasH*0.7 {
		t.Fatalf("bottom_center cue centre y = %.0f is not in the bottom band of a %dpx canvas", resolvedY, canvasH)
	}
	if math.Abs(resolvedX-canvasW/2) > 1 {
		t.Fatalf("bottom_center cue centre x = %.0f is not horizontally centred", resolvedX)
	}
}
