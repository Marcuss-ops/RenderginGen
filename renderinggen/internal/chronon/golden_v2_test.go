package chronon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestGoldenOverlayJobV2Immutability locks the universal benchmark golden in
// place so the payload cannot drift from its fixtures or the on-disk
// canonical copy. The v2 job is the permanent benchmark/regression fixture
// for the whole chain (SoftwareBackend / VulkanBackend / CLI / daemon IPC /
// cold-warm cache / CPU / GPU / future Chronon and PipelineGen versions):
// every future evolution must keep this job rendering correctly.
func TestGoldenOverlayJobV2Immutability(t *testing.T) {
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
	if err := json.Unmarshal([]byte(GoldenOverlayJobV2), &env); err != nil {
		t.Fatalf("GoldenOverlayJobV2 does not decode: %v", err)
	}
	if env.ID != "golden-overlay-v2" || env.Schema != "renderinggen.job" || env.Version != 1 {
		t.Fatalf("unexpected golden envelope: %+v", env)
	}
	if len(env.Assets) != 5 {
		t.Fatalf("expected 5 assets (background video, globe, chart, logo, font), got %d", len(env.Assets))
	}

	// 2. The render plan must be a well-formed chronon.render-plan.v1 with
	//    the full benchmark layer set: video background, 2 phrases, 2 words,
	//    2 image overlays, 1 logo, and 4 animations.
	var plan struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
		Canvas  struct {
			Width          int `json:"width"`
			Height         int `json:"height"`
			FPSNum         int `json:"fps_num"`
			FPSDen         int `json:"fps_den"`
			DurationFrames int `json:"duration_frames"`
		} `json:"canvas"`
		Layers []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Text      string `json:"text"`
			Font      string `json:"font"`
			Preset    string `json:"preset"`
			Asset     string `json:"asset"`
			Source    string `json:"source"`
			Animation *struct {
				Preset string `json:"preset"`
			} `json:"animation"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(env.RenderPlan, &plan); err != nil {
		t.Fatalf("render plan does not decode: %v", err)
	}
	if plan.Schema != "chronon.render-plan" || plan.Version != 1 {
		t.Fatalf("unexpected render plan envelope: %+v", plan)
	}
	if plan.Canvas.Width != 1280 || plan.Canvas.Height != 720 || plan.Canvas.FPSNum != 30 || plan.Canvas.FPSDen != 1 || plan.Canvas.DurationFrames != 240 {
		t.Fatalf("unexpected canvas (want 1280x720@30, 240 frames): %+v", plan.Canvas)
	}
	// Preset-driven layers (phrases / words / image overlays) carry no `type`:
	// Chronon derives it from the preset's supported_layer (ADR-029). Only the
	// preset-less primitives (background video / logo) keep their type.
	wantLayers := map[string]string{
		"background_video":   "video",
		"important_phrase_1": "",
		"important_word_1":   "",
		"image_overlay_1":    "",
		"important_phrase_2": "",
		"important_word_2":   "",
		"image_overlay_2":    "",
		"logo":               "image",
	}
	if len(plan.Layers) != len(wantLayers) {
		t.Fatalf("expected %d layers, got %d", len(wantLayers), len(plan.Layers))
	}
	animationPresets := map[string]bool{}
	for _, layer := range plan.Layers {
		if wantType, ok := wantLayers[layer.ID]; !ok {
			t.Fatalf("unexpected layer id %q", layer.ID)
		} else if layer.Type != wantType {
			t.Fatalf("layer %s: want type %s, got %s", layer.ID, wantType, layer.Type)
		}
		// Preset-driven text layers carry no type; identify them by their
		// caption/word preset for the font + animation assertions.
		if layer.Preset == "caption_card" || layer.Preset == "active_word_pop" {
			if layer.Font != "" {
				t.Fatalf("layer %s font = %q, want empty (Chronon resolves it from the preset)", layer.ID, layer.Font)
			}
			if layer.Animation == nil || layer.Animation.Preset == "" {
				t.Fatalf("text layer %s must carry an animation preset", layer.ID)
			}
			animationPresets[layer.Animation.Preset] = true
		}
	}
	// The benchmark spec: 3-4 animations across the job.
	if len(animationPresets) < 3 {
		t.Fatalf("expected at least 3 distinct animation presets, got %v", animationPresets)
	}
	if plan.Layers[1].Preset != "caption_card" {
		t.Fatalf("important_phrase_1 preset = %q, want caption_card", plan.Layers[1].Preset)
	}
	if plan.Layers[2].Preset != "active_word_pop" {
		t.Fatalf("important_word_1 preset = %q, want active_word_pop", plan.Layers[2].Preset)
	}
	if plan.Layers[0].Source != "assets/background.mp4" {
		t.Fatalf("background_video source = %q, want assets/background.mp4", plan.Layers[0].Source)
	}

	// 3. The Go constant must be byte-identical to the canonical JSON file.
	fileData, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "golden-overlay-job-v2.json"))
	if err != nil {
		t.Fatalf("read canonical golden JSON: %v", err)
	}
	var fileJSON map[string]any
	if err := json.Unmarshal(fileData, &fileJSON); err != nil {
		t.Fatalf("canonical golden JSON does not decode: %v", err)
	}
	var goJSON map[string]any
	if err := json.Unmarshal([]byte(GoldenOverlayJobV2), &goJSON); err != nil {
		t.Fatalf("Go constant does not decode: %v", err)
	}
	if !jsonEqual(t, goJSON, fileJSON) {
		t.Fatalf("GoldenOverlayJobV2 constant diverges from testdata/golden/golden-overlay-job-v2.json: update BOTH copies together")
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
			t.Fatalf("fixture %s sha256=%s but payload hash=%s: assets are not deterministic or were regenerated (re-run infra/e2e/gen-golden-v2-assets.py and update both copies)", fixture, got, a.Hash)
		}
	}
}
