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

// TestDocumentContractsPinGroupMotionInPlace pins the post-DEMOL-11 ownership
// contract: the live document contracts run on the owned decoded document, with
// no compatibility copy, and replaying the same bytes produces the same pinned
// document. Determinism therefore never depends on keeping the decoded tree
// pristine.
func TestDocumentContractsPinGroupMotionInPlace(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateSemanticPlan(&src)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePlan(validated, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Items[0].Item.MotionID; got != "duo_split_reveal" {
		t.Fatalf("resolved group motion = %q, want the pinned group_motion_id", got)
	}
	if got := resolved.Document.Items[0].MotionID; got != "duo_split_reveal" {
		t.Fatalf("owned document group motion = %q, want the pinned group_motion_id", got)
	}
	first, err := json.Marshal(resolved.Document)
	if err != nil {
		t.Fatal(err)
	}

	replay, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	replayValidated, err := validateSemanticPlan(&replay)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := resolvePlan(replayValidated, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(replayed.Document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("document contracts are not replay-deterministic:\nfirst=%s\nsecond=%s", first, second)
	}
}

// TestStagePipelineMatchesCompileSemanticDecoded drives all six stages
// directly and proves the stage sequence is the one production uses.
func TestStagePipelineMatchesCompileSemanticDecoded(t *testing.T) {
	src, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	direct, directAssets, directStats, directUnknown, err := compileSemanticDecoded(&src, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	directJSON, err := direct.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// The direct path owns and mutates its decoded document, so the stage path
	// decodes the same bytes again instead of replaying a normalized value.
	stageSrc, err := decodeSemanticPlan(stagePipelineFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateSemanticPlan(&stageSrc)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePlan(validated, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lowered, stats, err := lowerPlan(resolved)
	if err != nil {
		t.Fatal(err)
	}
	sealed, assets, sealedStats, unknown, err := sealPlan(lowered, stats, resolved, CompileOptions{})
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
