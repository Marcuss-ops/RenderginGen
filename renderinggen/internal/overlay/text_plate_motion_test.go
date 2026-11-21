package overlay

import (
	"math"
	"testing"
)

func TestTextPlateCarriesStrokeShadowAndRotatesMotions(t *testing.T) {
	background := &LayerBackground{
		Color:       "#12233A",
		Radius:      14,
		Padding:     []float64{16, 10},
		LiftToShape: true,
		Stroke:      &LayerStroke{Color: "#FFD159", Width: 3},
		Shadow:      &LayerShadow{Color: "#000000", Opacity: 0.55, Blur: 18, Offset: []float64{0, 8}},
		Motions:     []string{"fade", "slide_left"},
	}
	layers := []Layer{
		{
			ID: "cap-a", Type: "text", Size: []float64{200, 80}, Position: []float64{640, 300},
			DurationFrames: 24,
			Style:          &LayerStyle{Background: background},
			Animation:      &LayerAnimation{Tracks: []AnimationTrack{{Property: "position_y", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 60}, {Frame: 8, Value: 0}}}}},
		},
		{
			ID: "cap-b", Type: "text", Size: []float64{200, 80}, Position: []float64{640, 500},
			DurationFrames: 24,
			Style:          &LayerStyle{Background: background},
		},
	}
	plan := &Plan{Canvas: Canvas{Width: 1280, Height: 720}, Layers: layers}

	liftTextBackgroundPlates(plan)

	if len(plan.Layers) != 4 {
		t.Fatalf("layers = %d, want plate+text twice", len(plan.Layers))
	}
	first, second := plan.Layers[0], plan.Layers[2]
	if first.Shape == nil || first.Shape.Stroke == nil || first.Shape.Stroke.Color != "#FFD159" || first.Shape.Stroke.Width != 3 {
		t.Fatalf("plate stroke = %+v, want #FFD159 width 3", first.Shape)
	}
	if first.Style == nil || first.Style.Shadow == nil || first.Style.Shadow.Blur != 18 || len(first.Style.Shadow.Offset) != 2 {
		t.Fatalf("plate shadow = %+v, want blur 18 and a 2D offset", first.Style)
	}
	if first.Animation == nil || len(first.Animation.Tracks) != 1 || first.Animation.Tracks[0].Property != "opacity" {
		t.Fatalf("first plate animation = %+v, want the independent fade (not the caption position_y copy)", first.Animation)
	}
	if second.Animation == nil || len(second.Animation.Tracks) != 2 || second.Animation.Tracks[0].Property != "position_x" {
		t.Fatalf("second plate animation = %+v, want the rotated slide_left", second.Animation)
	}
	if plan.Layers[1].Style.Background != nil || plan.Layers[3].Style.Background != nil {
		t.Fatal("text layers still carry the lowered background")
	}
	// The label is the foreground of the same card: it must not run its old
	// entrance while the rectangle does something else.
	firstText := plan.Layers[1]
	if firstText.StartFrame != 12 || firstText.DurationFrames != 12 {
		t.Fatalf("first text lifetime = [%d,%d), want [12,24)", firstText.StartFrame, firstText.StartFrame+firstText.DurationFrames)
	}
	if firstText.Animation == nil || len(firstText.Animation.Tracks) != 1 || firstText.Animation.Tracks[0].Property != "opacity" {
		t.Fatalf("first text animation = %+v, want only the short reveal", firstText.Animation)
	}
	if hasAnimationTrack(firstText.Animation, "position_y") {
		t.Fatal("first text kept its old position_y entrance and will drift out of the plate")
	}
	secondText := plan.Layers[3]
	if secondText.StartFrame != 12 || secondText.DurationFrames != 12 {
		t.Fatalf("second text lifetime = [%d,%d), want [12,24)", secondText.StartFrame, secondText.StartFrame+secondText.DurationFrames)
	}
	if secondText.Animation == nil || hasAnimationTrack(secondText.Animation, "position_x") || !hasAnimationTrack(secondText.Animation, "opacity") {
		t.Fatalf("second text animation = %+v, want only the short reveal", secondText.Animation)
	}
}

// TestPlateEntranceKeepsLabelOutOfEveryIntermediateFrame pins the regression
// behind the glitch: during the plate entrance the label must not be active at
// all. Once it starts, no geometry track may move it outside the settled plate.
func TestPlateEntranceKeepsLabelOutOfEveryIntermediateFrame(t *testing.T) {
	for _, motion := range []string{"fade", "slide_left", "slide_right", "slide_up", "scale_in", "wipe_right"} {
		t.Run(motion, func(t *testing.T) {
			const startFrame = int64(30)
			layer := Layer{
				ID: "cap", Type: "text", Size: []float64{240, 90}, Position: []float64{640, 360},
				StartFrame: startFrame, DurationFrames: 48,
				Style: &LayerStyle{Background: &LayerBackground{
					Color: "#12233A", Padding: []float64{20, 10}, LiftToShape: true,
					Motions: []string{motion},
				}},
				Animation: &LayerAnimation{Tracks: []AnimationTrack{
					{Property: "position_y", Keyframes: []AnimationKeyframe{{Frame: startFrame, Value: 40}, {Frame: startFrame + 12, Value: 0}}},
					{Property: "opacity", Keyframes: []AnimationKeyframe{{Frame: startFrame, Value: 0}, {Frame: startFrame + 8, Value: 1}}},
				}},
			}
			plan := &Plan{Layers: []Layer{layer}}
			liftTextBackgroundPlates(plan)
			plate, text := plan.Layers[0], plan.Layers[1]
			enter := plateMotionDefaultEnterFrames

			for frame := startFrame; frame < startFrame+int64(enter); frame++ {
				if layerActiveAt(text, frame) {
					t.Fatalf("label is active at frame %d while the plate is still entering", frame)
				}
				if !layerActiveAt(plate, frame) {
					t.Fatalf("plate is inactive at frame %d of its own entrance", frame)
				}
			}
			if !layerActiveAt(text, startFrame+int64(enter)) {
				t.Fatalf("label does not start on the plate settle frame %d", startFrame+int64(enter))
			}
			for _, property := range []string{"position", "position_x", "position_y", "scale", "scale_x", "scale_y"} {
				if hasAnimationTrack(text.Animation, property) {
					t.Fatalf("label kept %q; it can drift outside the plate", property)
				}
			}
		})
	}
}

func layerActiveAt(layer Layer, frame int64) bool {
	return frame >= layer.StartFrame && frame < layer.StartFrame+layer.DurationFrames
}

func hasAnimationTrack(animation *LayerAnimation, property string) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		if track.Property == property {
			return true
		}
	}
	return false
}

func TestDecodeRuntimePlateStyle(t *testing.T) {
	style, err := decodeRuntimeTextStyle(map[string]any{
		"style": map[string]any{"background": map[string]any{
			"color": "#12233A", "radius": 12.0, "padding": []any{18.0, 10.0},
			"stroke":  map[string]any{"color": "#FFD159", "width": 2.5},
			"shadow":  map[string]any{"color": "#000000", "opacity": 0.4, "blur": 16.0, "offset": []any{0.0, 6.0}},
			"motions": []any{"fade", "slide_up"},
		}},
	})
	if err != nil {
		t.Fatalf("decode plate style: %v", err)
	}
	background := style.Background
	if background == nil || background.Stroke == nil || background.Stroke.Width != 2.5 {
		t.Fatalf("stroke = %+v, want width 2.5", background)
	}
	if background.Shadow == nil || background.Shadow.Blur != 16 || len(background.Shadow.Offset) != 2 {
		t.Fatalf("shadow = %+v, want blur 16 and [0,6]", background.Shadow)
	}
	if len(background.Motions) != 2 || background.Motions[0] != "fade" || background.Motions[1] != "slide_up" {
		t.Fatalf("motions = %v, want [fade slide_up]", background.Motions)
	}
}

func TestDecodeRuntimePlateStyleRejectsUnknownMotionAndOversizedShadow(t *testing.T) {
	base := func(background map[string]any) map[string]any {
		return map[string]any{"style": map[string]any{"background": background}}
	}
	for name, background := range map[string]map[string]any{
		"unknown motion": {"color": "#12233A", "motions": []any{"explode"}},
		"shadow blur":    {"color": "#12233A", "shadow": map[string]any{"blur": 65.0}},
		"stroke width":   {"color": "#12233A", "stroke": map[string]any{"color": "#FFFFFF", "width": 65.0}},
	} {
		if _, err := decodeRuntimeTextStyle(base(background)); err == nil {
			t.Errorf("%s: expected a decode error", name)
		}
	}
}

func TestPlateSlideUpEntersFromBelowAndShiftsToLayerStart(t *testing.T) {
	animation, ok := plateEntranceAnimation("slide_up", 200, 12, 50)
	if !ok || animation == nil || len(animation.Tracks) != 2 {
		t.Fatalf("slide_up animation = %+v, want position_y and opacity tracks", animation)
	}
	lift := animation.Tracks[0]
	if lift.Property != "position_y" || len(lift.Keyframes) != 2 {
		t.Fatalf("slide_up lift = %+v, want a position_y track", lift)
	}
	if lift.Keyframes[0].Frame != 50 || lift.Keyframes[1].Frame != 62 {
		t.Fatalf("slide_up frames = %v..%v, want 50..62", lift.Keyframes[0].Frame, lift.Keyframes[1].Frame)
	}
	if got, _ := lift.Keyframes[0].Value.(float64); got != 40 {
		t.Fatalf("slide_up start y = %v, want +40 (below the resting position in y-down coordinates)", got)
	}
}

func TestPlateWipeRightAnchorsTheLeftEdge(t *testing.T) {
	width := 200.0
	layer := Layer{
		ID: "cap", Type: "text", Size: []float64{width, 80}, Position: []float64{640, 300},
		DurationFrames: 24,
		Style: &LayerStyle{Background: &LayerBackground{
			Color: "#12233A", Padding: []float64{20, 10}, LiftToShape: true,
			Motions: []string{"wipe_right"},
		}},
	}
	plan := &Plan{Layers: []Layer{layer}}

	liftTextBackgroundPlates(plan)

	plate := plan.Layers[0]
	if plate.Animation == nil || len(plate.Animation.Tracks) < 2 {
		t.Fatalf("plate animation = %+v, want scale_x and position_x", plate.Animation)
	}
	scale := plate.Animation.Tracks[0]
	if scale.Property != "scale_x" || len(scale.Keyframes) != 2 || scale.Keyframes[0].Value != 0.01 {
		t.Fatalf("wipe scale = %+v, want 0.01 -> 1", scale)
	}
	shift := plate.Animation.Tracks[1]
	want := -(width + 40) * 0.495
	if shift.Property != "position_x" || len(shift.Keyframes) != 2 {
		t.Fatalf("wipe shift = %+v, want a position_x track", shift)
	}
	if got, _ := shift.Keyframes[0].Value.(float64); math.Abs(got-want) > 1e-9 {
		t.Fatalf("wipe start offset = %v, want %v", got, want)
	}
	text := plan.Layers[1]
	if text.StartFrame != 12 || text.DurationFrames != 12 {
		t.Fatalf("wipe text lifetime = [%d,%d), want [12,24) (start when the bar is complete)", text.StartFrame, text.StartFrame+text.DurationFrames)
	}
	if text.Animation == nil || len(text.Animation.Tracks) != 1 || text.Animation.Tracks[0].Property != "opacity" {
		t.Fatalf("wipe text animation = %+v, want only the short reveal", text.Animation)
	}
	if hasAnimationTrack(text.Animation, "scale_x") || hasAnimationTrack(text.Animation, "position_x") {
		t.Fatalf("wipe text kept a geometric entrance = %+v, want a clean start", text.Animation)
	}
}
