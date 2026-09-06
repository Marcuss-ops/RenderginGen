package overlay

import (
	"encoding/json"
	"math"
	"testing"
)

func TestBurnASSIntoPlanTypedReportsCueCount(t *testing.T) {
	plan := &Plan{Schema: "chronon.render-plan.v2", Version: 2, JobID: "j",
		Canvas: Canvas{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, DurationFrames: 150},
		Layers: []Layer{{ID: "source", Type: "video", Source: "assets/source.mp4", DurationFrames: 150}},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
	ass := []byte(`[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,Prima
Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,Dopo
`)
	style := &LayerStyle{FontSize: 52, Fill: "#FFFFFF"}
	box := SubtitleStyleBox{Width: 1800, Height: 140, X: 60, Y: 758}

	// The processor relies on this count for the subtitle_layers metric and
	// the "lowered N ASS cues" log line: it must never silently be 0.
	count, err := BurnASSIntoPlanTyped(plan, ass, "assets/fonts/Poppins-Bold.ttf", style, box)
	if err != nil {
		t.Fatalf("BurnASSIntoPlanTyped: %v", err)
	}
	if count != 2 {
		t.Fatalf("cue count = %d, want 2", count)
	}
	layers := 0
	for _, l := range plan.Layers {
		if len(l.ID) >= len("subtitle_cue_") && l.ID[:len("subtitle_cue_")] == "subtitle_cue_" {
			layers++
		}
	}
	if layers != count {
		t.Fatalf("plan holds %d subtitle_cue_ layers but count is %d", layers, count)
	}
}

func TestBurnASSIntoPlanCreatesTimedGPUTextLayers(t *testing.T) {
	plan := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1920,"height":1080,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"source","type":"video","source":"assets/source.mp4","duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	ass := []byte(`[Script Info]
ScriptType: v4.00+
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,Prima\Nseconda
Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,Dopo
`)

	style := &LayerStyle{FontSize: 52, Fill: "#FFFFFF",
		Shadow: &LayerShadow{Color: "#000000", Opacity: 0.95, Blur: 8, Offset: []float64{0, 5}}}
	box := SubtitleStyleBox{Width: 1800, Height: 140, X: 60, Y: 758}
	out, count, err := BurnASSIntoPlan(plan, ass, "assets/fonts/Poppins-Bold.ttf", style, box)
	if err != nil {
		t.Fatalf("BurnASSIntoPlan: %v", err)
	}
	if count != 2 {
		t.Fatalf("subtitle layers = %d, want 2", count)
	}
	var decoded Plan
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Layers[1].StartFrame; got != 15 {
		t.Fatalf("first cue start = %d, want 15", got)
	}
	if got := decoded.Layers[1].DurationFrames; got != 45 {
		t.Fatalf("first cue duration = %d, want 45", got)
	}
	if decoded.Layers[1].Style == nil || decoded.Layers[1].Style.Font != "assets/fonts/Poppins-Bold.ttf" {
		t.Fatalf("first cue style = %+v", decoded.Layers[1].Style)
	}
	if decoded.Layers[1].Text != "Prima\nseconda" {
		t.Fatalf("ASS line break was not lowered: %q", decoded.Layers[1].Text)
	}
	// The safe-area box (X=60, Y=758, 1800x140 on 1920x1080) converts to a
	// Chronon centre offset of [0, 288]: x = 60+900-960 = 0,
	// y = 758+70-540 = 288. The position comes from the caller's typed box —
	// the compiler invents no geometry.
	if len(decoded.Layers[1].Position) != 2 || decoded.Layers[1].Position[0] != 0 || math.Abs(decoded.Layers[1].Position[1]-288) > 1e-6 {
		t.Fatalf("subtitle position = %#v, want caller-box Chronon centre offset [0 288]", decoded.Layers[1].Position)
	}
}
