package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestBatchRender_InspectCompiledPlans(t *testing.T) {
	cases := []struct {
		id       string
		presetID string
		text     string
	}{
		{"01_kinetic_split_word", "kinetic_split_word", "KINETIC PERFORMANCE"},
		{"01_typewriter_clean", "phrase_typewriter_clean", "EVERY PIXEL MATTERS"},
	}

	for _, tc := range cases {
		raw := fmt.Sprintf(`{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id": %q,
			"video_id": %q,
			"width": 1920,
			"height": 1080,
			"fps_num": 24,
			"fps_den": 1,
			"duration_ms": 5000,
			"background": {
				"kind": "color",
				"color": [0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0]
			},
			"items": [
				{
					"id": "item_1",
					"template_id": "IMPORTANT_PHRASE",
					"preset_id": %q,
					"text": %q,
					"start_ms": 0,
					"end_ms": 5000
				}
			]
		}`, tc.id, tc.id, tc.presetID, tc.text)

		result, err := CompileSemantic([]byte(raw))
		if err != nil {
			t.Fatalf("compile %s: %v", tc.presetID, err)
		}
		data, err := json.MarshalIndent(result.Plan, "", "  ")
		if err != nil {
			t.Fatalf("marshal %s: %v", tc.presetID, err)
		}
		_ = os.WriteFile(fmt.Sprintf("/tmp/compiled_%s.json", tc.id), data, 0644)
		t.Logf("Compiled %s to /tmp/compiled_%s.json", tc.id, tc.id)
	}
}
