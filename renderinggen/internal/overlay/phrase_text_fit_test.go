package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPhrasePresetUsesShrinkOnlyTextFit(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-fit","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"O maior arrependimento da minha vida","start_ms":0,"end_ms":6000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile six-word phrase: %v", err)
	}
	if len(result.Plan.Layers) != 1 {
		t.Fatalf("compiled layers = %d, want 1", len(result.Plan.Layers))
	}
	layer := result.Plan.Layers[0]
	if layer.Style == nil {
		t.Fatal("phrase layer has no style")
	}
	if layer.Style.FitMode != "shrink_only" {
		t.Fatalf("text fit mode = %q, want shrink_only", layer.Style.FitMode)
	}
	if layer.Style.MinFontSize <= 0 || layer.Style.MaxFontSize <= 0 || layer.Style.MaxFontSize < layer.Style.MinFontSize {
		t.Fatalf("invalid font fit range: min=%v max=%v", layer.Style.MinFontSize, layer.Style.MaxFontSize)
	}
	if layer.Style.MaxFontSize != layer.Style.FontSize {
		t.Fatalf("fit max font size = %v, want authored size %v", layer.Style.MaxFontSize, layer.Style.FontSize)
	}
	if layer.Style.FontSize != 112 {
		t.Fatalf("authored font size = %v, want 112 (shrink-only fit must not grow the preset)", layer.Style.FontSize)
	}
	// The phrase wraps against estimated glyph advances and grows its box by
	// the selected font's line height so every line remains inside the layer.
	unwrapped := strings.Join(strings.Fields(strings.ReplaceAll(layer.Text, "\n", " ")), " ")
	if unwrapped != "O maior arrependimento da minha vida" {
		t.Fatalf("phrase text unwrapped = %q, want original", unwrapped)
	}
	if strings.Count(layer.Text, "\n") != 1 {
		t.Fatalf("phrase wrapped lines = %q, want one break", layer.Text)
	}
	for _, line := range strings.Split(layer.Text, "\n") {
		if estimatedTextWidth(line, layer.Style.FontSize) > float64(layer.BoxWidth)*0.9+1 {
			t.Fatalf("phrase line %q exceeds its pixel width budget", line)
		}
		if line != strings.TrimSpace(line) {
			t.Fatalf("phrase line %q has border whitespace", line)
		}
	}
	if layer.BoxWidth != 1920 || layer.BoxHeight < 260 || layer.Size[0] != 1920 || layer.Size[1] != float64(layer.BoxHeight) {
		t.Fatalf("phrase fit box = %dx%d (%v), want measured multiline height in a 1920px box", layer.BoxWidth, layer.BoxHeight, layer.Size)
	}
	if len(layer.Position) != 2 || layer.Position[0] != 960 || layer.Position[1] != 540 {
		t.Fatalf("phrase position after wrap = %v, want centred transform [960 540]", layer.Position)
	}
	wire, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal compiled phrase layer: %v", err)
	}
	var decoded struct {
		Style struct {
			FitMode     string  `json:"fit_mode"`
			MinFontSize float64 `json:"min_font_size"`
			MaxFontSize float64 `json:"max_font_size"`
		} `json:"style"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode compiled phrase style: %v", err)
	}
	if decoded.Style.FitMode != "shrink_only" || decoded.Style.MinFontSize != 64 || decoded.Style.MaxFontSize != 112 {
		t.Fatalf("serialized text fit = %+v, want Chronon shrink_only 64..112 contract", decoded.Style)
	}
}
