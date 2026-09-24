package overlay

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
)

const overlayContractSchema = "overlay-plan.v1.schema.json"

func fieldByName(typ reflect.Type, jsonName string) string {
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == jsonName {
			return jsonName
		}
	}
	return ""
}

// TestContractSchemaMatchesCompilerStructs is the bidirectional pin. Every
// schema object the compiler decodes is compared with its Go mirror.
func TestContractSchemaMatchesCompilerStructs(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	cases := []struct {
		name, pointer string
		goValue       any
	}{
		{"plan", "#/", semanticPlan{}},
		{"plan.source", "#/properties/source", semanticSource{}},
		{"plan.background", "#/properties/background", semanticBackground{}},
		{"plan.subtitles", "#/properties/subtitles", semanticSubtitles{}},
		{"plan.watermark", "#/properties/watermark", semanticWatermark{}},
		{"plan.audio", "#/properties/audio", semanticAudio{}},
		{"plan.items[]", "#/properties/items/items", semanticItem{}},
		{"plan.items[].asset_refs[]", "#/properties/items/items/properties/asset_refs/items", SemanticAssetRef{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := contractschema.PropertyNames(t, schema, tc.pointer)
			got := contractschema.JSONFieldNames(tc.goValue)
			if missing := contractschema.Difference(want, got); len(missing) > 0 {
				t.Errorf("schema declares %v, but Go does not decode them", missing)
			}
			if extra := contractschema.Difference(got, want); len(extra) > 0 {
				t.Errorf("Go decodes %v, but schema forbids them", extra)
			}
		})
	}
}

func TestContractSchemaRejectsUnknownCompilerFields(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"background":{"kind":"color","color":[0,0,0,1]},"definitely_not_in_the_contract":true}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("unknown top-level field must be rejected")
	}
	itemRaw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{"id":"i","template_id":"PERSON","preset_id":"phrase_default","text":"Ada","start_ms":0,"end_ms":1000,"unknown_item_field":1}]}`)
	if _, err := CompileSemantic(itemRaw); err == nil {
		t.Fatal("unknown item field must be rejected")
	}
}

func TestContractDeclaresEveryLiveLoweringInput(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	itemProperties := contractschema.PropertyNames(t, schema, "#/properties/items/items")
	watermarkProperties := contractschema.PropertyNames(t, schema, "#/properties/watermark")
	for _, field := range []string{"image_preset_id", "motion_id", "motion_params", "params", "style"} {
		if !contractschema.Contains(itemProperties, field) {
			t.Errorf("items.%s is read by the compiler but absent from the schema", field)
		}
	}
	for _, field := range []string{"font_ref", "margin_px"} {
		if !contractschema.Contains(watermarkProperties, field) {
			t.Errorf("watermark.%s is read by the compiler but absent from the schema", field)
		}
	}
}

func TestStyleProfileVocabularyIsSchemaOwned(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	styleProfile, ok := contractschema.At(t, schema, "#/properties/style_profile")["enum"].([]any)
	if !ok || len(styleProfile) == 0 {
		t.Fatal("schema style_profile must carry the producer-owned enum")
	}
	if fieldByName(reflect.TypeOf(semanticPlan{}), "style_profile") == "" {
		t.Fatal("semanticPlan must accept style_profile as an opaque string")
	}
}
