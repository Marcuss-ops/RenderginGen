package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOverlongPhraseShrinksInsteadOfClipping pins the last-resort guard: when
// the wrapped text cannot fit the canvas at its authored size, the compiler
// lowers a smaller, valid shrink-only interval instead of emitting a box taller
// than the canvas that the renderer would clip.
func TestOverlongPhraseShrinksInsteadOfClipping(t *testing.T) {
	text := strings.Repeat("Una frase editoriale molto lunga che deve restare interamente leggibile. ", 40)
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "long-fit", "video_id": "v",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "long", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "text": text,
			"start_ms": 0, "end_ms": 12000, "duration_ms": 12000,
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile overlong phrase: %v", err)
	}
	if len(result.Plan.Layers) != 1 {
		t.Fatalf("compiled %d layers, want 1", len(result.Plan.Layers))
	}
	layer := result.Plan.Layers[0]
	if layer.BoxHeight > 1080 {
		t.Fatalf("text box height %d exceeds canvas", layer.BoxHeight)
	}
	if layer.Style == nil || layer.Style.MaxFontSize >= 112 {
		t.Fatalf("overlong phrase kept its authored size: %+v", layer.Style)
	}
	if layer.Style.MinFontSize <= 0 || layer.Style.MaxFontSize < layer.Style.MinFontSize {
		t.Fatalf("invalid shrink interval: %+v", layer.Style)
	}
	if layer.Style.FitMode == "" || layer.Style.FitMode == "none" {
		t.Fatalf("overlong phrase lost its fit mode: %+v", layer.Style)
	}
	if strings.TrimSpace(strings.ReplaceAll(layer.Text, "\n", " ")) != strings.TrimSpace(text) {
		t.Fatal("shrinking must not truncate the phrase")
	}
}
