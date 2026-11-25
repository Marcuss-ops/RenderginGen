package overlay

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestPresentationMotionInventoryAndSemanticWiring(t *testing.T) {
	families := motion.Registry.PresentationFamilyIDs()

	total := 0
	for _, familyID := range families {
		ids := motion.Registry.PresentationMotionIDs(familyID)
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
				// Typewriter date/metric cards keep their layer entrance AND type
				// per glyph; every other presentation family must stay layer-only.
				if familyID == "date_typewriter_v1" || familyID == "metric_typewriter_v1" {
					if len(layer.TextAnimators) == 0 {
						t.Fatalf("motion_id %s dropped its glyph typing animator: %+v", id, layer)
					}
					for _, animator := range layer.TextAnimators {
						if len(animator.Selectors) == 0 || animator.Selectors[0].Unit != "glyph" || animator.Selectors[0].Start == nil {
							t.Fatalf("motion_id %s typing animator has no glyph selector sweep: %+v", id, animator)
						}
					}
				} else if len(layer.TextAnimators) != 0 {
					t.Fatalf("motion_id %s leaked per-glyph animators into a layer-only family: %+v", id, layer.TextAnimators)
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
	if total != 90 {
		t.Fatalf("presentation inventory contains %d motions, want 90 (metric_v1 20 + date_v1 20 + entity_card_v1 10 + metric_didone_v1 10 + date_didone_v1 10 + metric_typewriter_v1 10 + date_typewriter_v1 10)", total)
	}
}

// The typewriter families must be real typewriting, not a layer fade with a
// typewriter name: every preset carries a staggered glyph animator while its
// card entrance tracks stay authored. This is the catalog-side half of the
// guarantee; the wiring test above pins the lowered layer.
func TestDateAndMetricTypewriterFamiliesTypePerGlyph(t *testing.T) {
	for _, familyID := range []string{"date_typewriter_v1", "metric_typewriter_v1"} {
		ids := motion.Registry.PresentationMotionIDs(familyID)
		if len(ids) == 0 {
			t.Fatalf("%s has no presentation presets", familyID)
		}
		for _, id := range ids {
			plugin, err := motion.Registry.Resolve(id)
			if err != nil {
				t.Fatalf("resolve %s: %v", id, err)
			}
			definition := plugin.(motion.DeclarativePlugin).Definition
			if definition.Unit != "glyph" {
				t.Errorf("%s: unit = %q, want glyph", id, definition.Unit)
			}
			if len(definition.Tracks) == 0 {
				t.Errorf("%s: lost its layer entrance tracks", id)
			}
			if len(definition.TextAnimators) == 0 {
				t.Errorf("%s: has no per-glyph animator", id)
				continue
			}
			for _, animator := range definition.TextAnimators {
				if animator.Selector.Kind != "glyph" || animator.Selector.Stagger <= 0 {
					t.Errorf("%s/%s: selector kind=%q stagger=%d, want a staggered glyph selector", id, animator.ID, animator.Selector.Kind, animator.Selector.Stagger)
				}
				if len(animator.Properties) == 0 || animator.Properties[0].Property != "opacity" {
					t.Errorf("%s/%s: missing the opacity hide-unrevealed track", id, animator.ID)
				}
			}
		}
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
		t.Run(tc.template, func(t *testing.T) {
			spec := templateSpecFor(tc.template)
			if !spec.Registered || !spec.RequiresPreset || spec.Kind != tc.kind || spec.Family != PresetText {
				t.Fatalf("template registry row = %+v", spec)
			}
			wantTarget := "metric"
			if tc.template == "timeline_date_card" {
				wantTarget = "date"
			}
			if spec.MotionTarget != wantTarget {
				t.Fatalf("template motion target = %q, want %q", spec.MotionTarget, wantTarget)
			}
		})
	}
}
