//go:build certification

// editorial_v1_golden_harness_test.go owns the Editorial Visual Motion V1
// golden-plan harness: the plan/asset/item builders and golden-writer that
// editorial_v1_golden_plans_test.go compiles its projects with. It is the
// editorial lane's half of the certification harness, split per concern.
package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The Editorial Visual Motion V1 golden plans. Every project here is compiled
// through the full semantic pipeline (CompileSemantic → typed plan → wire
// JSON) exactly as a producer job would arrive, asserted against the V1
// contract, and written to out/editorial_v1/golden_plans/ for the render
// canaries. The golden plan is the deterministic artifact: the same compile
// twice must produce the same wire bytes.

const goldenOutDir = "../../out/editorial_v1/golden_plans"

func compileV1Plan(t *testing.T, planMap map[string]any) CompileResult {
	t.Helper()
	raw, err := json.Marshal(planMap)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	return result
}

func writeGoldenPlan(t *testing.T, result CompileResult, name string) string {
	t.Helper()
	dir := goldenOutDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".plan.json")
	if err := os.WriteFile(path, wire, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// uniquePortraitAsset gives every item its own logical path so the asset
// registry's one-path-one-identity rule is satisfied.
func uniquePortraitAsset(id string) map[string]any {
	return map[string]any{
		"asset_id":   id,
		"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"url":        "assets/canary/" + id + ".png",
		"media_type": "image/png",
	}
}

func uniqueBrowserAsset(id string) map[string]any {
	return map[string]any{
		"asset_id":   id,
		"sha256":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"url":        "assets/canary/" + id + ".png",
		"media_type": "image/png",
	}
}

func v1BasePlan(planID string, durationMS int64, items []any) map[string]any {
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        planID,
		"video_id":       planID,
		"project_id":     "editorial-visual-motion-v1",
		"width":          1280, "height": 720,
		"fps_num": 24, "fps_den": 1,
		"duration_ms": durationMS,
		"background": map[string]any{
			"kind": "color", "color": []any{0.071, 0.078, 0.098, 1.0},
		},
		"items": items,
	}
}

func entityCardItem(id, name string, startMS, endMS int64, motionID string) map[string]any {
	item := map[string]any{
		"id": id, "entity_id": "person:" + id, "kind": "entity_card",
		"template_id": "PERSON", "preset_id": "phrase_default",
		"text": name, "entity_caption": name,
		"image_preset_id": "image_scale_in",
		"motion_id":       motionID,
		"params":          map[string]any{"box_width": 420.0, "box_height": 420.0},
		"start_ms":        startMS, "end_ms": endMS, "duration_ms": endMS - startMS,
		"asset_refs": []any{uniquePortraitAsset(id)},
	}
	return item
}

// contentLayers drops the leading background layer the compiler emits from
// the plan background; the V1 canaries assert on item layers only.
func contentLayers(result CompileResult) []Layer {
	layers := result.Plan.Layers
	if len(layers) > 0 && layers[0].Type == "color" {
		return layers[1:]
	}
	return layers
}

func webFanCard(id string, x float64, motionID string) map[string]any {
	return map[string]any{
		"id": id, "kind": "image",
		"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
		"motion_id": motionID,
		"params":    map[string]any{"box_width": 340.0, "box_height": 220.0, "position_x": x, "position_y": 0.0},
		"start_ms":  0, "end_ms": 4000, "duration_ms": 4000,
		"asset_refs": []any{uniqueBrowserAsset(id)},
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
