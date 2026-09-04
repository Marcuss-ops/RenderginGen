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
// The historical bug converted X to Chronon's centre-offset space but left Y
// absolute, so SubtitleStyleAsset (which assumes both axes are offsets) pushed
// bottom cues below the canvas. The invariant verified here: the burned text
// layer's Chronon centre offset must equal the position computed by
// subtitleCueGeometry, and SubtitleStyleAsset's box must round-trip back to
// the original absolute top-left anchor.
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

			plan := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1920,"height":1080,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"source","type":"video","source":"assets/source.mp4","duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
			ass := []byte("Dialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,Cue\n")
			style2, box2, err := SubtitleStyleAsset(raw)
			if err != nil {
				t.Fatal(err)
			}
			out, count, err := BurnASSIntoPlan(plan, ass, "assets/fonts/F.ttf", style2, box2)
			if err != nil || count != 1 {
				t.Fatalf("BurnASSIntoPlan: count=%d err=%v", count, err)
			}
			var decoded Plan
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatal(err)
			}
			layer := decoded.Layers[1]

			// Independent expectation from the (now symmetric) geometry
			// resolver: Chronon centre offset of the safe-area box.
			wantX := tc.wantAnchorX + 1800/2 - 1920/2
			wantY := tc.wantAnchorY + 70/2 - 1080/2
			if len(layer.Position) != 2 || math.Abs(layer.Position[0]-wantX) > 1e-6 || math.Abs(layer.Position[1]-wantY) > 1e-6 {
				t.Fatalf("burned layer position = %#v, want centre offset [%.0f %.0f]", layer.Position, wantX, wantY)
			}
			// The layer's absolute centre must land inside the safe-area box.
			absCenterX := layer.Position[0] + 1920/2
			absCenterY := layer.Position[1] + 1080/2
			if absCenterX < float64(box2.X) || absCenterX > float64(box2.X+box2.Width) ||
				absCenterY < float64(box2.Y) || absCenterY > float64(box2.Y+box2.Height) {
				t.Fatalf("cue centre (%.0f,%.0f) escaped the safe-area box %+v", absCenterX, absCenterY, box2)
			}
		})
	}
}

// TestSubtitleBottomCenterOnCanvas is the minimal regression guard for the
// P0: a bottom_center cue on 1080p must render on-canvas (its centre offset
// must be < 540, i.e. above the bottom edge). The bug produced 829+35-540+540
// centre = 864 — wait: the buggy Y produced an absolute anchor passed through
// the offset converter, putting the centre at ~1369px, 290px off-canvas.
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
	plan := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1920,"height":1080,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	ass := []byte("Dialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,Cue\n")
	out, count, err := BurnASSIntoPlan(plan, ass, "assets/fonts/F.ttf", style, box)
	if err != nil || count != 1 {
		t.Fatalf("BurnASSIntoPlan: count=%d err=%v", count, err)
	}
	var decoded Plan
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	y := decoded.Layers[0].Position[1]
	absCenterY := y + 1080/2
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
