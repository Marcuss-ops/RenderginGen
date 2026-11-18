package overlay

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func stagePipelineFixture(t *testing.T) []byte {
	t.Helper()
	return []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"legacy_composite_replay":true,
		"plan_id":"stage-pipeline","video_id":"stage-pipeline",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"background":{"kind":"color","color":[0.02,0.02,0.02,1]},
		"items":[{
			"id":"pair","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"group_motion_id":"duo_split_reveal","start_ms":0,"end_ms":3000,
			"asset_refs":[
				{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/a.png","media_type":"image/png"},
				{"asset_id":"b","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/b.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"a","asset_id":"a","start_ms":0,"end_ms":1500,"preset_id":"image_focus_in","caption":"A"},
				{"id":"b","asset_id":"b","start_ms":1500,"end_ms":3000,"preset_id":"image_focus_in","caption":"B"}
			]
		}]
	}`)
}

func TestResolveItemStagesOnOneItem(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	item := src.Items[0]
	envelope, err := resolveItemEnvelope(item, &src)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Start != 0 || envelope.End <= envelope.Start {
		t.Fatalf("envelope = %+v, want a positive frame window", envelope)
	}
	spec, kind, err := classifyItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Family == "" || kind == "" {
		t.Fatalf("classification = %+v/%q, want template family and kind", spec, kind)
	}
	if err := validateResolvedItem(item, spec, kind, &src); err != nil {
		t.Fatalf("validateResolvedItem: %v", err)
	}
	presentation, err := resolveItemPresentation(item, spec, kind, &src)
	if err != nil {
		t.Fatalf("resolveItemPresentation: %v", err)
	}
	if presentation.PresetID == "" || presentation.Params == nil {
		t.Fatalf("presentation = %+v, want resolved preset and params", presentation)
	}
	mapSpec, err := resolveItemMap(item, kind, &src, envelope)
	if err != nil {
		t.Fatalf("resolveItemMap: %v", err)
	}
	if mapSpec != nil {
		t.Fatalf("non-map item resolved a map: %+v", mapSpec)
	}
}

func TestValidateSemanticPlanDoesNotMutateInput(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateSemanticPlan(&src)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("validateSemanticPlan mutated its input")
	}
	if validated.Source != &src || validated.PlanID != "stage-pipeline" ||
		validated.Canvas.Width != 640 || validated.FPS.Num != 24 {
		t.Fatalf("validated plan = %+v, want identity facts from the input", validated)
	}
}

func TestNormalizeCompatibilityMutatesOnlyTheCopy(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateSemanticPlan(&src)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeCompatibility(validated)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalized.Plan.Items[0].MotionID; got != "duo_split_reveal" {
		t.Fatalf("normalized group motion = %q, want the pinned group_motion_id", got)
	}
	if src.Items[0].MotionID != "" {
		t.Fatal("normalizeCompatibility mutated the validated source document")
	}
	after, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("normalizeCompatibility changed the source document")
	}
}

// TestStagePipelineMatchesCompileSemanticDecoded drives all six stages
// directly and proves the stage sequence is the one production uses.
func TestStagePipelineMatchesCompileSemanticDecoded(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	direct, directAssets, directStats, directUnknown, err := compileSemanticDecoded(&src)
	if err != nil {
		t.Fatal(err)
	}
	directJSON, err := direct.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	validated, err := validateSemanticPlan(&src)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeCompatibility(validated)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePlan(normalized)
	if err != nil {
		t.Fatal(err)
	}
	lowered, stats, err := lowerPlan(resolved)
	if err != nil {
		t.Fatal(err)
	}
	sealed, assets, sealedStats, unknown, err := sealPlan(lowered, stats, resolved)
	if err != nil {
		t.Fatal(err)
	}
	sealedJSON, err := sealed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(directJSON, sealedJSON) {
		t.Fatalf("stage pipeline plan differs from compileSemanticDecoded:\nstage=%s\ndirect=%s", sealedJSON, directJSON)
	}
	if len(directAssets) != len(assets) || len(directUnknown) != len(unknown) || !reflect.DeepEqual(directStats, sealedStats) {
		t.Fatalf("stage tuple = %d assets/%v unknown/%+v stats, direct = %d/%v/%+v",
			len(assets), unknown, sealedStats, len(directAssets), directUnknown, directStats)
	}
}
