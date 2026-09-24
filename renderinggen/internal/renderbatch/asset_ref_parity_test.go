package renderbatch

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

const assetRefContractSchema = "overlay-plan.v1.schema.json"

// TestSemanticAssetRefAssetIDParity pins the writer side of the asset_refs
// contract against the decoder. The document transports
// overlay.SemanticAssetRef itself — one declaration, so there is no second
// field set to drift — and its asset id is spelled asset_id on the wire, the
// same key overlay's decoder and Chronon3d's render_plan_decoder read. A tag
// drift to "assetId" would still compile, but the worker would publish a plan
// whose asset_id the decoder silently drops and the render would die with a
// missing asset.
func TestSemanticAssetRefAssetIDParity(t *testing.T) {
	typ := reflect.TypeOf(overlay.SemanticAssetRef{})
	documentField, ok := reflect.TypeOf(semanticItemDocument{}).FieldByName("AssetRefs")
	if !ok || documentField.Type != reflect.SliceOf(typ) {
		t.Fatalf("the plan document does not transport overlay.SemanticAssetRef: %v", documentField.Type)
	}
	tags := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tags[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for _, want := range []string{"asset_id", "sha256", "url", "media_type"} {
		if !tags[want] {
			t.Errorf("overlay.SemanticAssetRef has no %q field: %v", want, reflect.VisibleFields(typ))
		}
	}
	if len(tags) != 4 {
		t.Errorf("overlay.SemanticAssetRef declares %d json fields, want exactly the contract's four", len(tags))
	}

	planTyp := reflect.TypeOf(PlanAssetRef{})
	planField, ok := planTyp.FieldByName("AssetID")
	if !ok {
		t.Fatalf("renderbatch.PlanAssetRef has no field AssetID")
	}
	if got := planField.Tag.Get("json"); got != "asset_id" {
		t.Fatalf("renderbatch.PlanAssetRef.AssetID json tag = %q, want \"asset_id\"", got)
	}

	// The schema is the third authority: overlay's decoder and this writer
	// must both describe the same property set. A field that exists in Go but
	// not in the schema would be dropped by a schema-validating producer.
	schema := contractschema.Load(t, assetRefContractSchema)
	schemaFields := contractschema.PropertyNames(t, schema, "#/properties/items/items/properties/asset_refs/items")
	if !contractschema.Contains(schemaFields, "asset_id") {
		t.Fatal("schema asset_refs items must declare asset_id")
	}
	decodedFields := contractschema.JSONFieldNames(overlay.SemanticAssetRef{})
	if extra := contractschema.Difference(decodedFields, schemaFields); len(extra) > 0 {
		t.Errorf("overlay.SemanticAssetRef declares %v, which the contract schema forbids", extra)
	}
	if missing := contractschema.Difference(schemaFields, decodedFields); len(missing) > 0 {
		t.Errorf("the contract schema declares %v, which overlay.SemanticAssetRef does not transport", missing)
	}

	// Writer/reader round-trip: a document built through the typed writer
	// must carry asset_id on the wire and must be accepted by the same
	// strict decoder the worker uses. This is the parity that a tag-only
	// check cannot provide.
	spec := PlanSpec{
		PlanID: "parity-asset-id", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 5000,
		Items: []PlanItem{{
			ID: "item", Kind: "entity_image", TemplateID: "IMAGE_OVERLAY", PresetID: overlay.ImageMotionCorpusPresetID,
			EntityID: "parity-asset", Text: "parity",
			AssetRefs: []PlanAssetRef{{AssetID: "parity-asset", SHA256: strings.Repeat("a", 64), URL: "https://example.test/parity.jpg", MediaType: "image/jpeg"}},
			StartMS:   0, EndMS: 5000,
		}},
	}
	built, err := BuildPlan(spec)
	if err != nil {
		t.Fatalf("BuildPlan with asset_id: %v", err)
	}
	if !strings.Contains(string(built), `"asset_id":"parity-asset"`) {
		t.Fatalf("built plan does not carry asset_id on the wire: %s", built)
	}
	result, err := overlay.CompileSemantic(built)
	if err != nil {
		t.Fatalf("overlay decoder rejected the writer's asset_id: %v", err)
	}
	// The decoder must read the value back, not merely accept the document:
	// a rejected-then-ignored asset id is the silent-drop failure this test
	// exists for.
	resolved := false
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" && strings.Contains(layer.Asset, "parity-asset") {
			resolved = true
		}
	}
	if !resolved {
		t.Fatalf("the decoder dropped the asset id: %+v", result.Plan.Layers)
	}
	concrete, err := CompileRenderPlan(built)
	if err != nil {
		t.Fatalf("CompileRenderPlan: %v", err)
	}
	// The concrete plan must not invent a different key.
	if strings.Contains(string(concrete), `"AssetID"`) || strings.Contains(string(concrete), `"assetId"`) {
		t.Fatalf("concrete plan carries a non-canonical asset key: %s", concrete)
	}
}
