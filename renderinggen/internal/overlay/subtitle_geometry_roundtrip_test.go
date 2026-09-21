package overlay

import (
	"encoding/json"
	"math"
	"testing"
)

// TestSubtitleGeometryRoundTripEndToEnd locks the coordinate contract across
// the full burn chain for every supported subtitle position:
//
//	style.position → subtitleCueGeometry → SubtitleStyleBox
//	                 → BurnASSIntoPlan → Layer.Position
//
// The invariant verified here: for a text layer the plan's position field is
// the layer centre in ABSOLUTE canvas coordinates (the engine subtracts
// canvas/2 itself), so the burned cue's position must equal the safe-area box's
// absolute centre, and SubtitleStyleAsset's box must carry the absolute
// top-left anchor unchanged.
func TestSubtitleGeometryRoundTripEndToEnd(t *testing.T) {
	cases := []struct {
		name     string
		position string
		// Absolute top-left anchor expected from subtitleCueGeometry, derived
		// independently from the documented placement rules on a 1920x1080
		// canvas with the default 1800x70 box.
		wantAnchorX, wantAnchorY float64
	}{
		{"bottom_center", "bottom_center", (1920 - 1800) / 2, 1080*0.80 - 70/2},
		{"top_center", "top_center", (1920 - 1800) / 2, 1080 * 0.10},
		{"middle_center", "middle_center", (1920 - 1800) / 2, (1080 - 70) / 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{
				"schema_version":"renderinggen.overlay-plan.v1",
				"plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":30,"fps_den":1,
				"subtitles":{"asset_refs":[{"asset_id":"ass","sha256":"` + sixtyFourZeros() + `","url":"https://cdn.example/subs.ass","media_type":"text/x-ssa"}],
					"mode":"burn",
					"style":{"position":"` + tc.position + `","font_size_px":52,"color":"#FFFFFF"}}
			}`)
			style, box, err := SubtitleStyleAsset(raw)
			if err != nil || style == nil {
				t.Fatalf("SubtitleStyleAsset: style=%+v err=%v", style, err)
			}
			// Box must round-trip to the absolute top-left anchor.
			if math.Abs(float64(box.X)-tc.wantAnchorX) > 1e-6 || math.Abs(float64(box.Y)-tc.wantAnchorY) > 1e-6 {
				t.Fatalf("safe-area box = (%d,%d), want absolute anchor (%.0f,%.0f)",
					box.X, box.Y, tc.wantAnchorX, tc.wantAnchorY)
			}

			plan := &Plan{Schema: "chronon.render-plan.v2", Version: 2, JobID: "j",
				Canvas: Canvas{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, DurationFrames: 150},
				Layers: []Layer{{ID: "source", Type: "video", Source: "assets/source.mp4", DurationFrames: 150}},
				Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
			ass := []byte("Dialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,Cue\n")
			style2, box2, err := SubtitleStyleAsset(raw)
			if err != nil {
				t.Fatal(err)
			}
			count, err := BurnASSIntoPlanTyped(plan, ass, "assets/fonts/F.ttf", style2, box2)
			if err != nil || count != 1 {
				t.Fatalf("BurnASSIntoPlanTyped: count=%d err=%v", count, err)
			}
			delivered, err := plan.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			var decoded Plan
			if err := json.Unmarshal(delivered, &decoded); err != nil {
				t.Fatal(err)
			}
			layer := decoded.Layers[1]

			// Independent expectation from the geometry resolver: for a text
			// layer the plan's position IS the box centre in absolute canvas
			// coordinates (the engine subtracts canvas/2 itself).
			wantX := tc.wantAnchorX + 1800/2
			wantY := tc.wantAnchorY + 70/2
			if len(layer.Position) != 2 || math.Abs(layer.Position[0]-wantX) > 1e-6 || math.Abs(layer.Position[1]-wantY) > 1e-6 {
				t.Fatalf("burned layer position = %#v, want absolute canvas centre [%.0f %.0f]", layer.Position, wantX, wantY)
			}
			// For a text layer the position already IS the absolute centre.
			absCenterX := layer.Position[0]
			absCenterY := layer.Position[1]
			if absCenterX < float64(box2.X) || absCenterX > float64(box2.X+box2.Width) ||
				absCenterY < float64(box2.Y) || absCenterY > float64(box2.Y+box2.Height) {
				t.Fatalf("cue centre (%.0f,%.0f) escaped the safe-area box %+v", absCenterX, absCenterY, box2)
			}
		})
	}
}

// TestSubtitleBottomCenterOnCanvas is the minimal regression guard for the P0:
// a bottom_center cue on 1080p must place its centre at y=864 (829 + 35), well
// inside the canvas. The bug handed the absolute top-left anchor to the text
// branch, which subtracts canvas/2 a second time, parking the cue's centre at
// (0, 324) — the top-left corner, with the text clipped off the left edge.
func TestSubtitleBottomCenterOnCanvas(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":30,"fps_den":1,
		"subtitles":{"asset_refs":[{"asset_id":"ass","sha256":"` + sixtyFourZeros() + `","url":"https://cdn.example/subs.ass","media_type":"text/x-ssa"}],
			"mode":"burn",
			"style":{"position":"bottom_center","font_size_px":52,"color":"#FFFFFF"}}
	}`)
	style, box, err := SubtitleStyleAsset(raw)
	if err != nil || style == nil {
		t.Fatalf("SubtitleStyleAsset: style=%+v err=%v", style, err)
	}
	plan := &Plan{Schema: "chronon.render-plan.v2", Version: 2, JobID: "j",
		Canvas: Canvas{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, DurationFrames: 150},
		Layers: []Layer{},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
	ass := []byte("Dialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,Cue\n")
	count, err := BurnASSIntoPlanTyped(plan, ass, "assets/fonts/F.ttf", style, box)
	if err != nil || count != 1 {
		t.Fatalf("BurnASSIntoPlanTyped: count=%d err=%v", count, err)
	}
	delivered, err := plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Plan
	if err := json.Unmarshal(delivered, &decoded); err != nil {
		t.Fatal(err)
	}
	y := decoded.Layers[0].Position[1]
	// For a text layer the position IS the centre in absolute canvas
	// coordinates; the engine subtracts canvas/2 itself.
	absCenterY := y
	// The cue's centre must sit above the canvas bottom edge with margin for
	// the box height; 829 + 35 = 864 < 1080 is the correct absolute centre.
	if absCenterY <= 0 || absCenterY >= 1080 {
		t.Fatalf("bottom_center cue centre Y = %.0f is off-canvas (want 864)", absCenterY)
	}
	if absCenterY != 864 {
		t.Fatalf("bottom_center cue centre Y = %.0f, want 864", absCenterY)
	}
}

func sixtyFourZeros() string {
	const z = "0000000000000000000000000000000000000000000000000000000000000000"
	if len(z) != 64 {
		panic("bad fixture hash length")
	}
	return z
}
