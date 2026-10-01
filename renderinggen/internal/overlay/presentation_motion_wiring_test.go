package overlay

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestPresentationMotionInventoryAndSemanticWiring(t *testing.T) {
	inventory := PresentationMotionInventory()
	families := motion.Registry.PresentationFamilyIDs()
	if len(inventory) != len(families) {
		t.Fatalf("presentation inventory has %d families, want %d", len(inventory), len(families))
	}

	total := 0
	for _, familyID := range families {
		ids := inventory[familyID]
		if len(ids) != len(motion.Registry.PresentationMotionIDs(familyID)) {
			t.Fatalf("%s inventory differs from canonical catalog", familyID)
		}
		for _, id := range ids {
			t.Run(id, func(t *testing.T) {
				plugin, err := motion.Registry.Resolve(id)
				if err != nil {
					t.Fatal(err)
				}
				definition := plugin.(motion.DeclarativePlugin).Definition
				item := map[string]any{
					"id": "presentation-" + id, "template_id": definition.SupportedTemplate,
					"preset_id": PhraseDefaultPresetID, "motion_id": id,
					"text": "59.99", "start_ms": 0, "end_ms": 3000,
				}
				switch familyID {
				case "metric_v1":
					item["kind"] = string(KindMetricStat)
				case "date_v1":
					item["kind"] = string(KindTimelineDate)
				case "entity_card_v1":
					item["kind"] = string(KindEntityCard)
					item["entity_id"] = "entity:gpu-certification"
					item["duration_ms"] = 3000
				}
				raw, err := json.Marshal(map[string]any{
					"schema_version": SemanticSchema, "plan_id": "presentation-" + id,
					"video_id": "presentation-certification", "width": 1280, "height": 720,
					"fps_num": 24, "fps_den": 1, "items": []any{item},
				})
				if err != nil {
					t.Fatal(err)
				}
				result, err := CompileSemantic(raw)
				if err != nil {
					t.Fatalf("compile semantic motion_id %q: %v", id, err)
				}
				if len(result.Plan.Layers) != 1 {
					t.Fatalf("compiled %s to %d layers, want 1", id, len(result.Plan.Layers))
				}
				layer := result.Plan.Layers[0]
				if layer.Type != "text" || layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
					t.Fatalf("motion_id %s did not lower to a text layer with layer tracks: %+v", id, layer)
				}
				requires3D := definition.Requires3D != nil && *definition.Requires3D
				if layer.Enable3D != requires3D {
					t.Fatalf("motion_id %s enable_3d=%v, catalog requires_3d=%v", id, layer.Enable3D, requires3D)
				}
				wire, err := result.Plan.Marshal()
				if err != nil {
					t.Fatalf("marshal %s Chronon plan: %v", id, err)
				}
				var plan struct {
					Layers []struct {
						ID        string          `json:"id"`
						Enable3D  bool            `json:"enable_3d"`
						Animation *LayerAnimation `json:"animation"`
					} `json:"layers"`
				}
				if err := json.Unmarshal(wire, &plan); err != nil {
					t.Fatalf("decode %s Chronon plan: %v", id, err)
				}
				if len(plan.Layers) != 1 || plan.Layers[0].Animation == nil || plan.Layers[0].Enable3D != requires3D {
					t.Fatalf("motion_id %s lost its animation/routing on the wire: %+v", id, plan.Layers)
				}
			})
			total++
		}
	}
	if total != 50 {
		t.Fatalf("presentation inventory contains %d motions, want 50", total)
	}
}

func TestDateAndMetricProducerPresentationRoutesCompile(t *testing.T) {
	cases := []struct {
		name, template, motion string
		kind                   ItemKind
	}{
		{"date", "TIMELINE_DATE_CARD", "date_calendar_flip", KindNumber},
		{"metric", "METRIC_STAT_CARD", "metric_counter_rise", KindNumber},
	}
	items := make([]any, 0, len(cases))
	for index, tc := range cases {
		items = append(items, map[string]any{
			"id": tc.name, "kind": string(tc.kind), "template_id": tc.template,
			"preset_id": PhraseDefaultPresetID, "motion_id": tc.motion,
			"text": "2026 59.99", "start_ms": index * 1000, "end_ms": index*1000 + 2000,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": SemanticSchema, "plan_id": "date-metric-routing", "video_id": "date-metric-routing",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1, "items": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile dedicated DATE/METRIC presentation items: %v", err)
	}
	if len(compiled.Plan.Layers) != len(cases) {
		t.Fatalf("compiled layers=%d, want %d", len(compiled.Plan.Layers), len(cases))
	}
	for index, tc := range cases {
		layer := compiled.Plan.Layers[index]
		if layer.Type != "text" || layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Errorf("%s presentation did not lower to an animated text layer: %+v", tc.name, layer)
		}
	}
}

func TestPresentationTemplateRegistryMatchesCatalog(t *testing.T) {
	for _, tc := range []struct {
		template string
		kind     ItemKind
	}{
		{"metric_stat_card", KindNumber},
		{"timeline_date_card", KindNumber},
	} {
		t.Run(fmt.Sprintf("%s", tc.template), func(t *testing.T) {
			spec := templateSpecFor(tc.template)
			if !spec.Registered || !spec.RequiresPreset || spec.Kind != tc.kind || spec.Family != PresetText {
				t.Fatalf("template registry row = %+v", spec)
			}
		})
	}
}
