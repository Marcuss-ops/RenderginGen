package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func editorialSummaryPlan(composition any) map[string]any {
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "editorial-summary-plan", "video_id": "editorial-summary-video",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
		"editorial_compositions": []any{composition},
	}
}

func validEditorialSummaryComposition() map[string]any {
	return map[string]any{
		"schema_version": "editorial_composition.v1",
		"composition_id": "summary-scene-1", "scene_id": "scene-1",
		"window": map[string]any{"start_us": 0, "duration_us": 5_000_000},
		"members": []any{
			map[string]any{
				"member_id": "scene-1-micro-summary", "kind": "summary",
				"start_us": 1_000_000, "duration_us": 3_000_000,
				"reveal": "progressive",
				"points": []any{
					map[string]any{"text": "Japan population rose sharply", "start_us": 1_000_000, "duration_us": 1_000_000},
					map[string]any{"text": "Japan population reached a record level", "start_us": 2_500_000, "duration_us": 1_500_000},
				},
			},
		},
	}
}

func TestCompileSemanticEditorialSummaryUsesProgressiveRevealAndSourceTiming(t *testing.T) {
	raw, err := json.Marshal(editorialSummaryPlan(validEditorialSummaryComposition()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile planner summary sidecar: %v", err)
	}
	var textLayers []Layer
	for _, layer := range result.Plan.Layers {
		if layer.Type == "text" {
			textLayers = append(textLayers, layer)
		}
	}
	if len(textLayers) != 2 {
		t.Fatalf("lowered summary text layers = %d, want 2: %+v", len(textLayers), result.Plan.Layers)
	}
	wantText := []string{"Japan population rose sharply", "Japan population reached a record level"}
	wantStarts := []int64{24, 60}
	for i, layer := range textLayers {
		if strings.Join(strings.Fields(layer.Text), " ") != wantText[i] {
			t.Fatalf("summary layer %d text = %q, want exact grounded source", i, layer.Text)
		}
		if layer.StartFrame != wantStarts[i] {
			t.Fatalf("summary layer %q starts at frame %d, want %d", layer.ID, layer.StartFrame, wantStarts[i])
		}
		if layer.DurationFrames != 96-layer.StartFrame {
			t.Fatalf("summary layer %q duration=%d, want cumulative hold to frame 96", layer.ID, layer.DurationFrames)
		}
		if layer.Animation == nil && len(layer.TextAnimators) == 0 {
			t.Fatalf("summary layer %q has no resolved progressive motion", layer.ID)
		}
	}
}

func TestEditorialSummarySidecarFailsClosedOnUnsupportedOrForgedData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "unknown member field",
			mutate: func(comp map[string]any) {
				comp["unexpected"] = true
			},
			want: "unknown field",
		},
		{
			name: "unsupported member kind",
			mutate: func(comp map[string]any) {
				comp["members"].([]any)[0].(map[string]any)["kind"] = "chart"
			},
			want: "progressive summary",
		},
		{
			name: "out of scene source point",
			mutate: func(comp map[string]any) {
				points := comp["members"].([]any)[0].(map[string]any)["points"].([]any)
				points[1].(map[string]any)["start_us"] = 4_500_000
			},
			want: "scene window|ordered",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			composition := validEditorialSummaryComposition()
			test.mutate(composition)
			raw, err := json.Marshal(editorialSummaryPlan(composition))
			if err != nil {
				t.Fatal(err)
			}
			_, err = CompileSemantic(raw)
			if err == nil {
				t.Fatal("expected invalid editorial composition to be rejected")
			}
			for _, alternative := range strings.Split(test.want, "|") {
				if strings.Contains(err.Error(), alternative) {
					return
				}
			}
			t.Fatalf("error %q did not contain any of %q", err, test.want)
		})
	}
}
