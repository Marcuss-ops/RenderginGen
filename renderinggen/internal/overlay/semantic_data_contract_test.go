package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// The metric/date blocks are the wire half of the runtime catalog's data_fields
// entries. These tests pin the two directions that matter:
//
//   - a DECLARED block is complete and belongs to the item's data target, so a
//     half-filled or mis-targeted payload fails closed with a named field;
//   - a plan that declares NO block keeps compiling, so the extension does not
//     silently invalidate the presentation route or any stored plan.
func dataProbePlan(t *testing.T, item map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": SemanticSchema, "plan_id": "data-probe", "video_id": "data-probe",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
		"items": []any{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func dataProbeItem(id, kind, template string) map[string]any {
	return map[string]any{
		"id": id, "kind": kind, "template_id": template,
		"preset_id": PhraseDefaultPresetID, "text": "59.99",
		"start_ms": 0, "end_ms": 3000,
	}
}

func TestDeclaredMetricAndDateBlocksCompile(t *testing.T) {
	metric := dataProbeItem("metric", string(KindMetricStat), "METRIC_STAT_CARD")
	metric["metric"] = map[string]any{
		"value": "42", "unit": "%", "label": "Growth", "delta": "+4", "precision": 1,
	}
	date := dataProbeItem("date", string(KindTimelineDate), "TIMELINE_DATE_CARD")
	date["date"] = map[string]any{
		"value": "2026-10-07", "end": "2026-10-09", "format": "d MMMM yyyy", "timezone": "Europe/Rome", "ordering": 3,
	}
	items := []any{metric, date}
	raw, err := json.Marshal(map[string]any{
		"schema_version": SemanticSchema, "plan_id": "data-blocks", "video_id": "data-blocks",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
		"items": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("declared metric/date blocks must compile: %v", err)
	}
	if len(compiled.Plan.Layers) != 2 {
		t.Fatalf("compiled layers = %d, want one per data card", len(compiled.Plan.Layers))
	}
	for _, layer := range compiled.Plan.Layers {
		if layer.Type != "text" {
			t.Errorf("data card lowered to %q, want a text layer", layer.Type)
		}
	}
}

// TestDataBlocksStayOptionalForPlansWithoutStructuredValues is the backward
// compatibility half: the presentation route transports the displayed text
// only, and those plans must not start failing because the contract grew a
// block they never sent.
func TestDataBlocksStayOptionalForPlansWithoutStructuredValues(t *testing.T) {
	for _, item := range []map[string]any{
		dataProbeItem("metric", string(KindMetricStat), "METRIC_STAT_CARD"),
		dataProbeItem("date", string(KindTimelineDate), "TIMELINE_DATE_CARD"),
		dataProbeItem("number", string(KindNumber), "NUMBER"),
	} {
		if _, err := CompileSemantic(dataProbePlan(t, item)); err != nil {
			t.Errorf("plan without a structured data block must keep compiling: %v", err)
		}
	}
}

func TestDeclaredDataBlocksFailClosedOnIncompleteOrMistargetedPayload(t *testing.T) {
	withMetric := func(block map[string]any) map[string]any {
		item := dataProbeItem("metric", string(KindMetricStat), "METRIC_STAT_CARD")
		item["metric"] = block
		return item
	}
	cases := []struct {
		name string
		item map[string]any
		want string
	}{
		{
			name: "metric without unit",
			item: withMetric(map[string]any{"value": "42"}),
			want: "metric.unit",
		},
		{
			name: "metric without value",
			item: withMetric(map[string]any{"unit": "%"}),
			want: "metric.value",
		},
		{
			name: "metric with blank unit",
			item: withMetric(map[string]any{"value": "42", "unit": "   "}),
			want: "metric.unit",
		},
		{
			name: "metric precision out of bounds",
			item: withMetric(map[string]any{"value": "42", "unit": "%", "precision": 13}),
			want: "metric.precision",
		},
	}
	{
		item := dataProbeItem("date", string(KindTimelineDate), "TIMELINE_DATE_CARD")
		item["date"] = map[string]any{"end": "2026-10-09"}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"date without value", item, "date.value"})
	}
	{
		item := dataProbeItem("date", string(KindTimelineDate), "TIMELINE_DATE_CARD")
		item["date"] = map[string]any{"value": "2026-10-07", "ordering": dataOrderingLimit + 1}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"date ordering out of bounds", item, "date.ordering"})
	}
	{
		item := dataProbeItem("metric-on-phrase", string(KindImportantPhrase), "IMPORTANT_PHRASE")
		item["metric"] = map[string]any{"value": "42", "unit": "%"}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"metric block on a phrase card", item, "not \"metric\""})
	}
	{
		// A metric card must not accept a date payload: the two compositions
		// share the wire shape, so the target is what keeps them apart.
		item := dataProbeItem("date-on-metric", string(KindMetricStat), "METRIC_STAT_CARD")
		item["date"] = map[string]any{"value": "2026-10-07"}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"date block on a metric card", item, "not \"date\""})
	}
	{
		item := dataProbeItem("both", string(KindMetricStat), "METRIC_STAT_CARD")
		item["metric"] = map[string]any{"value": "42", "unit": "%"}
		item["date"] = map[string]any{"value": "2026-10-07"}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"both blocks on one card", item, "both metric and date"})
	}
	{
		item := dataProbeItem("unknown-block-field", string(KindMetricStat), "METRIC_STAT_CARD")
		item["metric"] = map[string]any{"value": "42", "unit": "%", "mystery": 1}
		cases = append(cases, struct {
			name string
			item map[string]any
			want string
		}{"unknown field inside metric", item, "unknown field"})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileSemantic(dataProbePlan(t, tc.item))
			if err == nil {
				t.Fatal("declared data block must fail closed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// TestDataBlockTargetsMatchTheCatalog keeps the compiler's block validation and
// the runtime catalog from drifting: the two data compositions the picker
// publishes are exactly the two targets a declared block is accepted for.
func TestDataBlockTargetsMatchTheCatalog(t *testing.T) {
	if got := dataCompositionTarget("metric_stat"); got != dataMetricTarget {
		t.Errorf("metric_stat catalog target = %q, want %q", got, dataMetricTarget)
	}
	if got := dataCompositionTarget("timeline_date"); got != dataDateTarget {
		t.Errorf("timeline_date catalog target = %q, want %q", got, dataDateTarget)
	}
	// The catalog entry names the motion target; the template is the one the
	// presentation route uses for that target.
	templateFor := map[string]string{
		"metric_stat":   "METRIC_STAT_CARD",
		"timeline_date": "TIMELINE_DATE_CARD",
	}
	for _, definition := range dataCompositionCatalog {
		template, ok := templateFor[definition.ID]
		if !ok {
			t.Fatalf("data composition %q has no template probe; the cross-check would silently pass", definition.ID)
		}
		item := dataProbeItem("probe-"+definition.ID, string(definition.Kind), template)
		switch definition.MotionTarget {
		case dataMetricTarget:
			item["metric"] = map[string]any{"value": "42", "unit": "%"}
		case dataDateTarget:
			item["date"] = map[string]any{"value": "2026-10-07"}
		default:
			t.Fatalf("data composition %q declares unknown motion target %q", definition.ID, definition.MotionTarget)
		}
		if _, err := CompileSemantic(dataProbePlan(t, item)); err != nil {
			t.Errorf("catalog composition %q does not accept its own declared block: %v", definition.ID, err)
		}
	}
}
