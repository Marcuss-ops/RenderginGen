// cue_burn_test.go locks the word-aligned cue lowering contract: schema
// validation, frame-grid conversion, typed-style requirement, geometry
// round-trip and idempotency.
package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func validCueDoc() []byte {
	return []byte(`{
		"schema_version": "renderinggen.cues.v1",
		"language": "it",
		"cues": [
			{"start_ms": 500, "end_ms": 950, "text": "Ciao",
			 "words": [{"start_ms": 500, "end_ms": 950, "text": "Ciao", "probability": 0.97}]},
			{"start_ms": 1000, "end_ms": 2000, "text": "mondo bellissimo",
			 "words": [
				{"start_ms": 1000, "end_ms": 1500, "text": "mondo", "probability": 0.95},
				{"start_ms": 1500, "end_ms": 2000, "text": "bellissimo", "probability": 0.93}]}
		]
	}`)
}

func TestParseCueDocumentValid(t *testing.T) {
	cues, err := ParseCueDocument(validCueDoc())
	if err != nil {
		t.Fatalf("ParseCueDocument: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2", len(cues))
	}
	if cues[1].Words[0].Text != "mondo" {
		t.Fatalf("word timestamps lost: %+v", cues[1].Words)
	}
}

func TestParseCueDocumentFailClosed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"wrong schema", `{"schema_version":"renderinggen.cues.v0","cues":[{"start_ms":1,"end_ms":2,"text":"x"}]}`},
		{"no cues", `{"schema_version":"renderinggen.cues.v1","cues":[]}`},
		{"empty text", `{"schema_version":"renderinggen.cues.v1","cues":[{"start_ms":1,"end_ms":2,"text":""}]}`},
		{"inverted timing", `{"schema_version":"renderinggen.cues.v1","cues":[{"start_ms":5,"end_ms":5,"text":"x"}]}`},
		{"garbage", `not-json`},
	}
	for _, tc := range cases {
		if _, err := ParseCueDocument([]byte(tc.raw)); err == nil {
			t.Fatalf("%s: expected an error, got nil", tc.name)
		}
	}
}

func cueTestPlan() *Plan {
	return newPlan("cue-test", 1280, 720, 30, 1, 90)
}

func TestBurnCuesIntoPlanTyped(t *testing.T) {
	cues, err := ParseCueDocument(validCueDoc())
	if err != nil {
		t.Fatal(err)
	}
	plan := cueTestPlan()
	style := &LayerStyle{FontSize: 38, Fill: "#FFFFFF"}
	box := SubtitleStyleBox{Width: 900, Height: 90, X: 190, Y: 575}

	count, err := BurnCuesIntoPlanTyped(plan, cues, "assets/font.ttf", style, box)
	if err != nil {
		t.Fatalf("BurnCuesIntoPlanTyped: %v", err)
	}
	if count != 2 {
		t.Fatalf("cue layers = %d, want 2", count)
	}

	// Frame conversion: 500ms @30fps starts at frame 15, 950ms ends at 29
	// (ceil of 28.5); the word is never on screen before it is spoken.
	l0 := plan.Layers[len(plan.Layers)-2]
	if l0.StartFrame != 15 {
		t.Fatalf("cue 0 start_frame = %d, want 15", l0.StartFrame)
	}
	if l0.DurationFrames != 14 { // 29 - 15
		t.Fatalf("cue 0 duration = %d, want 14", l0.DurationFrames)
	}
	// Second cue: 1000ms→frame 30, 2000ms→frame 60.
	l1 := plan.Layers[len(plan.Layers)-1]
	if l1.StartFrame != 30 || l1.DurationFrames != 30 {
		t.Fatalf("cue 1 = start %d dur %d, want 30/30", l1.StartFrame, l1.DurationFrames)
	}

	// Geometry must match the subtitle centre-offset contract.
	wantX := float64(box.X) + float64(box.Width)*0.5 - 640
	if l0.Position[0] != wantX {
		t.Fatalf("cue 0 x = %.1f, want %.1f", l0.Position[0], wantX)
	}
	if l0.Style.Font != "assets/font.ttf" || l0.Style.FontSize != 38 {
		t.Fatalf("cue style not applied: %+v", l0.Style)
	}
}

func TestBurnCuesIdempotent(t *testing.T) {
	cues, _ := ParseCueDocument(validCueDoc())
	plan := cueTestPlan()
	style := &LayerStyle{FontSize: 38, Fill: "#FFFFFF"}
	box := SubtitleStyleBox{Width: 900, Height: 90, X: 190, Y: 575}

	if _, err := BurnCuesIntoPlanTyped(plan, cues, "assets/font.ttf", style, box); err != nil {
		t.Fatal(err)
	}
	count, err := BurnCuesIntoPlanTyped(plan, cues, "assets/font.ttf", style, box)
	if err != nil {
		t.Fatalf("second burn: %v", err)
	}
	if count != 2 {
		t.Fatalf("idempotent burn count = %d, want 2 (no duplicates)", count)
	}
	if len(plan.Layers) != 2 {
		t.Fatalf("plan grew to %d layers, want 2", len(plan.Layers))
	}
}

func TestBurnCuesFailClosed(t *testing.T) {
	cues, _ := ParseCueDocument(validCueDoc())
	box := SubtitleStyleBox{Width: 900, Height: 90, X: 190, Y: 575}
	cases := []struct {
		name  string
		plan  *Plan
		font  string
		style *LayerStyle
	}{
		{"nil plan", nil, "f.ttf", &LayerStyle{FontSize: 10}},
		{"no font", cueTestPlan(), " ", &LayerStyle{FontSize: 10}},
		{"no style", cueTestPlan(), "f.ttf", nil},
		{"style without size", cueTestPlan(), "f.ttf", &LayerStyle{Fill: "#FFF"}},
		{"zero box", cueTestPlan(), "f.ttf", &LayerStyle{FontSize: 10}},
	}
	for _, tc := range cases {
		b := box
		if tc.name == "zero box" {
			b = SubtitleStyleBox{}
		}
		if _, err := BurnCuesIntoPlanTyped(tc.plan, cues, tc.font, tc.style, b); err == nil {
			t.Fatalf("%s: expected an error, got nil", tc.name)
		}
	}
}

func TestCuePlanMarshalsForChronon(t *testing.T) {
	cues, _ := ParseCueDocument(validCueDoc())
	plan := cueTestPlan()
	style := &LayerStyle{FontSize: 38, Fill: "#FFFFFF"}
	box := SubtitleStyleBox{Width: 900, Height: 90, X: 190, Y: 575}
	if _, err := BurnCuesIntoPlanTyped(plan, cues, "assets/font.ttf", style, box); err != nil {
		t.Fatal(err)
	}
	out, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Plan
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if !strings.HasPrefix(decoded.Layers[0].ID, "cue_") {
		t.Fatalf("first layer id = %q", decoded.Layers[0].ID)
	}
	// BoxWidth/BoxHeight are Go-side only and must never leak into Chronon's JSON.
	if strings.Contains(string(out), `"box_width"`) {
		t.Fatal("box_width leaked into Chronon JSON")
	}
}
