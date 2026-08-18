package chronon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestGoldenOverlayJobV1Immutability locks the golden in place so the payload
// cannot drift from its fixtures or the on-disk canonical copy. If any of the
// pieces (Go constant, testdata JSON, fixture bytes) changes without updating
// the others, this test fails — that is the point of a golden reference.
func TestGoldenOverlayJobV1Immutability(t *testing.T) {
	// 1. The Go constant must decode as a valid renderinggen.job envelope.
	var env struct {
		ID         string          `json:"id"`
		Schema     string          `json:"schema"`
		Version    int             `json:"version"`
		RenderPlan json.RawMessage `json:"render_plan"`
		Assets     []struct {
			Hash        string `json:"hash"`
			LogicalPath string `json:"logical_path"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(GoldenOverlayJobV1), &env); err != nil {
		t.Fatalf("GoldenOverlayJobV1 does not decode: %v", err)
	}
	if env.ID != "golden-overlay-v1" || env.Schema != "renderinggen.job" || env.Version != 1 {
		t.Fatalf("unexpected golden envelope: %+v", env)
	}
	if len(env.Assets) != 3 {
		t.Fatalf("expected 3 assets (background, apple, font), got %d", len(env.Assets))
	}

	// 2. The render plan must be a well-formed chronon.render-plan.v1 with the
	//    four real-workload layers.
	var plan struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
		Canvas  struct {
			Width          int `json:"width"`
			Height         int `json:"height"`
			FPS            int `json:"fps"`
			DurationFrames int `json:"duration_frames"`
		} `json:"canvas"`
		Layers []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Text   string `json:"text"`
			Font   string `json:"font"`
			Preset string `json:"preset"`
			Asset  string `json:"asset"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(env.RenderPlan, &plan); err != nil {
		t.Fatalf("render plan does not decode: %v", err)
	}
	if plan.Schema != "chronon.render-plan" || plan.Version != 1 {
		t.Fatalf("unexpected render plan envelope: %+v", plan)
	}
	if plan.Canvas.Width != 1280 || plan.Canvas.Height != 720 || plan.Canvas.FPS != 30 || plan.Canvas.DurationFrames != 150 {
		t.Fatalf("unexpected canvas (want 1280x720@30, 150 frames): %+v", plan.Canvas)
	}
	// Preset-driven layers (phrase / word / image overlay) carry no `type`:
	// Chronon derives it from the preset's supported_layer (ADR-029). Only the
	// preset-less background primitive keeps its image type.
	wantLayers := map[string]string{
		"background":       "image",
		"important_phrase": "",
		"important_word":   "",
		"image_overlay":    "",
	}
	if len(plan.Layers) != len(wantLayers) {
		t.Fatalf("expected %d layers, got %d", len(wantLayers), len(plan.Layers))
	}
	for _, layer := range plan.Layers {
		if wantType, ok := wantLayers[layer.ID]; !ok {
			t.Fatalf("unexpected layer id %q", layer.ID)
		} else if layer.Type != wantType {
			t.Fatalf("layer %s: want type %s, got %s", layer.ID, wantType, layer.Type)
		}
	}
	if plan.Layers[1].Preset != "caption_card" {
		t.Fatalf("important_phrase preset = %q, want caption_card", plan.Layers[1].Preset)
	}
	if plan.Layers[2].Preset != "active_word_pop" {
		t.Fatalf("important_word preset = %q, want active_word_pop", plan.Layers[2].Preset)
	}
	// Text layers carry no font: Chronon's VisualPresetRegistry resolves the
	// preset's canonical font asset (assets/fonts/DejaVuSans.ttf) via
	// StyleResolver. The font bytes must still be in the payload's assets
	// (checked below via the fixture-hash loop).
	if plan.Layers[1].Font != "" {
		t.Fatalf("important_phrase font = %q, want empty (Chronon resolves it from the preset)", plan.Layers[1].Font)
	}
	if plan.Layers[2].Font != "" {
		t.Fatalf("important_word font = %q, want empty (Chronon resolves it from the preset)", plan.Layers[2].Font)
	}

	// 3. The Go constant must be byte-identical to the canonical JSON file.
	fileData, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "golden-overlay-job-v1.json"))
	if err != nil {
		t.Fatalf("read canonical golden JSON: %v", err)
	}
	var fileJSON map[string]any
	if err := json.Unmarshal(fileData, &fileJSON); err != nil {
		t.Fatalf("canonical golden JSON does not decode: %v", err)
	}
	var goJSON map[string]any
	if err := json.Unmarshal([]byte(GoldenOverlayJobV1), &goJSON); err != nil {
		t.Fatalf("Go constant does not decode: %v", err)
	}
	if !jsonEqual(t, goJSON, fileJSON) {
		t.Fatalf("GoldenOverlayJobV1 constant diverges from testdata/golden/golden-overlay-job-v1.json: update BOTH copies together")
	}

	// 4. Each fixture on disk must hash to the payload hash (deterministic
	//    assets; regenerating them without updating the payload breaks the
	//    golden — exactly what we want to catch).
	for _, a := range env.Assets {
		fixture := filepath.Join("..", "..", "..", "testdata", "golden", filepath.Base(a.LogicalPath))
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		if got := storage.Hash(data); got != a.Hash {
			t.Fatalf("fixture %s sha256=%s but payload hash=%s: assets are not deterministic or were regenerated (re-run infra/e2e/gen-golden-assets.py and update both copies)", fixture, got, a.Hash)
		}
	}
}

// jsonEqual reports deep equality between two decoded JSON documents.
func jsonEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	return string(ab) == string(bb)
}
