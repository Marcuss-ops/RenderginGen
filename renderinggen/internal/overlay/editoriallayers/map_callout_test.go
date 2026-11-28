package editoriallayers

import (
	"os"
	"path/filepath"
	"testing"
)

func admittedMapCallout(t *testing.T) *Plan {
	t.Helper()
	root, _ := loadEditorialCorpus(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "lowered", "map_photo_callout.layers.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Admit(raw)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestResolveMapCalloutGeometryFollowsCameraProjectionInSafeArea(t *testing.T) {
	plan := admittedMapCallout(t)
	layerID := plan.Callouts["milano"]
	projection := []PinProjection{
		{Frame: 0, X: 310, Y: 245},
		{Frame: 12, X: 390, Y: 280},
		{Frame: 24, X: 470, Y: 315},
	}
	got, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "auto", Size{Width: 220, Height: 140}, projection,
		LayoutContext{CanvasWidth: 1280, CanvasHeight: 720, SafeInset: 48, Gap: 18})
	if err != nil {
		t.Fatal(err)
	}
	if got.Position != "right" || len(got.LeaderSamples) != len(projection) {
		t.Fatalf("position=%q leaders=%d, want right and %d camera samples", got.Position, len(got.LeaderSamples), len(projection))
	}
	if !containsRect(Rect{X: 48, Y: 48, Width: 1184, Height: 624}, got.Bounds) {
		t.Fatalf("photo card escaped safe area: %+v", got.Bounds)
	}
	for i, leader := range got.LeaderSamples {
		if leader.Frame != projection[i].Frame || leader.FromX != projection[i].X || leader.FromY != projection[i].Y {
			t.Fatalf("leader %d lost camera anchor: %+v", i, leader)
		}
		if leader.ToX < got.Bounds.X || leader.ToX > got.Bounds.X+got.Bounds.Width ||
			leader.ToY < got.Bounds.Y || leader.ToY > got.Bounds.Y+got.Bounds.Height {
			t.Fatalf("leader %d does not terminate on card edge: %+v card=%+v", i, leader, got.Bounds)
		}
	}
}

func TestResolveMapCalloutGeometryRejectsMissingOrUnsafeCameraLayout(t *testing.T) {
	plan := admittedMapCallout(t)
	layerID := plan.Callouts["milano"]
	validContext := LayoutContext{CanvasWidth: 1280, CanvasHeight: 720, SafeInset: 48, Gap: 18}
	projection := []PinProjection{{Frame: 0, X: 640, Y: 360}}
	if _, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "auto", Size{Width: 220, Height: 140}, nil, validContext); err == nil {
		t.Fatal("expected missing camera projection rejection")
	}
	if _, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "left", Size{Width: 220, Height: 140}, projection, validContext); err == nil {
		t.Fatal("expected caller placement that differs from the admitted declaration to fail closed")
	}
	if _, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "auto", Size{Width: 220, Height: 140}, projection,
		LayoutContext{CanvasWidth: 320, CanvasHeight: 180, SafeInset: 40, Gap: 18}); err == nil {
		t.Fatal("expected a callout that cannot fit the safe area to fail closed")
	}
	if _, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "auto", Size{Width: 220, Height: 140},
		[]PinProjection{{Frame: 1, X: 10, Y: 10}, {Frame: 1, X: 20, Y: 20}}, validContext); err == nil {
		t.Fatal("expected unordered camera projections to fail closed")
	}
	if _, err := plan.ResolveMapCalloutGeometry(layerID, "milano", "right", Size{Width: 220, Height: 140}, projection,
		LayoutContext{CanvasWidth: 1280, CanvasHeight: 720, SafeInset: 48, Gap: 0}); err == nil {
		t.Fatal("expected a zero-length leader to fail closed")
	}
}
