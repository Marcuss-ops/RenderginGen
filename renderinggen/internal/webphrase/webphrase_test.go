package webphrase

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func TestDefaultScenesCompile(t *testing.T) {
	for _, scene := range []Scene{DefaultLong(), DefaultShort()} {
		t.Run(scene.Mode, func(t *testing.T) {
			raw, err := BuildPlan(scene)
			if err != nil {
				t.Fatalf("build plan: %v", err)
			}
			var document struct {
				Width      int   `json:"width"`
				Height     int   `json:"height"`
				FPSNum     int   `json:"fps_num"`
				DurationMS int64 `json:"duration_ms"`
			}
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatalf("decode plan: %v", err)
			}
			if document.Width != Width || document.Height != Height {
				t.Fatalf("canvas = %dx%d, want %dx%d", document.Width, document.Height, Width, Height)
			}
			if document.FPSNum != FPS {
				t.Fatalf("fps = %d, want %d", document.FPSNum, FPS)
			}
			if document.DurationMS > 10000 {
				t.Fatalf("duration = %dms, want at most ten seconds", document.DurationMS)
			}

			result, err := overlay.CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile semantic plan: %v", err)
			}
			if result.Plan.Canvas.DurationFrames != int64(scene.Frames) {
				t.Fatalf("compiled duration = %d frames, want %d", result.Plan.Canvas.DurationFrames, scene.Frames)
			}

			marker, phrase := -1, -1
			for index, layer := range result.Plan.Layers {
				switch layer.ID {
				case "web_phrase_marker":
					marker = index
					if layer.Type != "shape" || layer.Shape == nil || layer.Shape.Type != "rect" {
						t.Fatalf("marker layer = %+v, want a rect shape", layer)
					}
				case "web_phrase_text":
					phrase = index
					if layer.Type != "text" || !strings.Contains(layer.Text, strings.Split(scene.Phrase, " ")[0]) {
						t.Fatalf("phrase layer = %+v, want the authored phrase text", layer)
					}
					if layer.Animation == nil && len(layer.TextAnimators) == 0 {
						t.Fatal("phrase layer has no entrance animation")
					}
				}
			}
			if marker < 0 || phrase < 0 {
				t.Fatalf("compiled layers missing marker or phrase (marker=%d phrase=%d)", marker, phrase)
			}
			if marker > phrase {
				t.Fatalf("marker layer at %d comes after phrase layer at %d", marker, phrase)
			}
			if len(result.Plan.Layers) < 10 {
				t.Fatalf("compiled %d layers, want the full page grammar", len(result.Plan.Layers))
			}
		})
	}
}

func TestRejectsMoreThanTenSeconds(t *testing.T) {
	scene := DefaultLong()
	scene.Frames = MaxFrames + 1
	if _, err := BuildPlan(scene); err == nil {
		t.Fatal("accepted a clip longer than ten seconds")
	}
}

func TestRejectsUnknownMode(t *testing.T) {
	scene := DefaultLong()
	scene.Mode = "medium"
	if _, err := BuildPlan(scene); err == nil {
		t.Fatal("accepted an unknown mode")
	}
}

func TestWrapPhraseKeepsEveryWord(t *testing.T) {
	phrase := "The February deadline does not just reshape one roster. It redraws the entire Western Conference."
	lines := wrapPhrase(phrase, 52, 4)
	if got, want := strings.Join(lines, " "), phrase; got != want {
		t.Fatalf("wrapPhrase dropped or reordered words:\n got %q\nwant %q", got, want)
	}
}
