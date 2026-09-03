package overlay

import (
	"encoding/json"
	"math"
	"testing"
)

func TestBurnASSIntoPlanCreatesTimedGPUTextLayers(t *testing.T) {
	plan := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1920,"height":1080,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"source","type":"video","source":"assets/source.mp4","duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	ass := []byte(`[Script Info]
ScriptType: v4.00+
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,Prima\Nseconda
Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,Dopo
`)

	out, count, err := BurnASSIntoPlan(plan, ass, "assets/fonts/Poppins-Bold.ttf")
	if err != nil {
		t.Fatalf("BurnASSIntoPlan: %v", err)
	}
	if count != 2 {
		t.Fatalf("subtitle layers = %d, want 2", count)
	}
	var decoded concretePlan
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
	if len(decoded.Layers[1].Position) != 2 || decoded.Layers[1].Position[0] != 0 || math.Abs(decoded.Layers[1].Position[1]-394) > 1e-6 {
		t.Fatalf("subtitle position = %#v, want lower-safe-area Chronon offset [0 394]", decoded.Layers[1].Position)
	}
}
