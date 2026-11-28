package editoriallayers

import (
	"strings"
	"testing"
)

func attachmentPlan(position string) *Plan {
	return &Plan{Document: &Document{Layers: []Layer{
		{LayerID: "entity", Role: RoleImage},
		{LayerID: "entity#date", Role: RoleDate, ParentLayerID: "entity", Position: position},
	}}}
}

func TestResolveAttachmentPlacementsUsesSafeAreaAndFinalParentBounds(t *testing.T) {
	plan := attachmentPlan("auto")
	placements, err := plan.ResolveAttachmentPlacements(LayoutContext{
		CanvasWidth: 1280, CanvasHeight: 720, SafeInset: 48, Gap: 24,
		LayerBounds: map[string]Rect{"entity": {X: 700, Y: 160, Width: 420, Height: 420}},
		LayerSizes:  map[string]Size{"entity#date": {Width: 300, Height: 60}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Position != "below" || placements[0].Bounds != (Rect{X: 760, Y: 604, Width: 300, Height: 60}) {
		t.Fatalf("resolved placement = %+v, want a below placement from the measured image bounds", placements)
	}
}

func TestResolveAttachmentAutoPlacementMovesAroundOccupiedSpace(t *testing.T) {
	plan := attachmentPlan("auto")
	placements, err := plan.ResolveAttachmentPlacements(LayoutContext{
		CanvasWidth: 1280, CanvasHeight: 720, SafeInset: 48, Gap: 24,
		LayerBounds: map[string]Rect{"entity": {X: 700, Y: 160, Width: 420, Height: 420}},
		LayerSizes:  map[string]Size{"entity#date": {Width: 300, Height: 60}},
		Obstacles:   []Rect{{X: 740, Y: 604, Width: 340, Height: 70}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Position != "above" {
		t.Fatalf("resolved placement = %+v, want above after below collides", placements)
	}
	if placements[0].Bounds.Y < 48 {
		t.Fatalf("placement escaped top safe inset: %+v", placements[0])
	}
}

func TestResolveAttachmentBadgeMayOverlayParentWithinSafeArea(t *testing.T) {
	placements, err := attachmentPlan("badge").ResolveAttachmentPlacements(LayoutContext{
		CanvasWidth: 640, CanvasHeight: 360, SafeInset: 24, Gap: 12,
		LayerBounds: map[string]Rect{"entity": {X: 100, Y: 60, Width: 360, Height: 240}},
		LayerSizes:  map[string]Size{"entity#date": {Width: 96, Height: 42}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 352, Y: 72, Width: 96, Height: 42}
	if len(placements) != 1 || placements[0].Position != "badge" || placements[0].Bounds != want {
		t.Fatalf("badge placement = %+v, want %+v", placements, want)
	}
}

func TestResolveAttachmentPlacementFailsClosedWhenNoSafeSlotFits(t *testing.T) {
	_, err := attachmentPlan("auto").ResolveAttachmentPlacements(LayoutContext{
		CanvasWidth: 320, CanvasHeight: 180, SafeInset: 24, Gap: 16,
		LayerBounds: map[string]Rect{"entity": {X: 40, Y: 40, Width: 240, Height: 100}},
		LayerSizes:  map[string]Size{"entity#date": {Width: 260, Height: 80}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatalf("no safe slot error = %v", err)
	}
}

func TestResolveAttachmentPlacementRejectsUnresolvedGeometry(t *testing.T) {
	_, err := attachmentPlan("below").ResolveAttachmentPlacements(LayoutContext{CanvasWidth: 640, CanvasHeight: 360})
	if err == nil || !strings.Contains(err.Error(), "no resolved parent bounds") {
		t.Fatalf("missing parent geometry error = %v", err)
	}
}

func TestResolveAttachmentRequestedSlotsStayInsideSafeArea(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position string
		parent   Rect
		want     string
	}{
		{"below", "below", Rect{X: 200, Y: 100, Width: 200, Height: 100}, "below"},
		{"above at bottom edge", "auto", Rect{X: 200, Y: 250, Width: 200, Height: 100}, "above"},
		{"right", "right", Rect{X: 100, Y: 150, Width: 180, Height: 100}, "right"},
		{"left", "left", Rect{X: 300, Y: 150, Width: 180, Height: 100}, "left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := attachmentPlan(tc.position)
			placements, err := plan.ResolveAttachmentPlacements(LayoutContext{
				CanvasWidth: 640, CanvasHeight: 400, SafeInset: 20, Gap: 12,
				LayerBounds: map[string]Rect{"entity": tc.parent},
				LayerSizes:  map[string]Size{"entity#date": {Width: 80, Height: 32}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(placements) != 1 || placements[0].Position != tc.want {
				t.Fatalf("resolved placement = %+v, want %q", placements, tc.want)
			}
			safe := Rect{X: 20, Y: 20, Width: 600, Height: 360}
			if !containsRect(safe, placements[0].Bounds) {
				t.Fatalf("resolved placement escaped safe area: %+v", placements[0])
			}
		})
	}
}

func TestResolveAttachmentRejectsFullScreenParentWithoutAttachmentSlot(t *testing.T) {
	plan := attachmentPlan("auto")
	_, err := plan.ResolveAttachmentPlacements(LayoutContext{
		CanvasWidth: 640, CanvasHeight: 360, SafeInset: 24, Gap: 12,
		LayerBounds: map[string]Rect{"entity": {X: 24, Y: 24, Width: 592, Height: 312}},
		LayerSizes:  map[string]Size{"entity#date": {Width: 160, Height: 42}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatalf("full-screen parent should fail closed when every external slot is occupied: %v", err)
	}
}
