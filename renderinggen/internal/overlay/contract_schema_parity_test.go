package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func contractSchemaPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Join(filepath.Dir(source), "../../../contracts/overlay-plan.v1.schema.json")
}

func schemaAt(t *testing.T, schema map[string]any, pointer string) map[string]any {
	t.Helper()
	value := any(schema)
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		if part == "" {
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("schema pointer %q traverses non-object at %q", pointer, part)
		}
		value, ok = object[part]
		if !ok {
			t.Fatalf("schema pointer %q missing %q", pointer, part)
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema pointer %q is not an object", pointer)
	}
	return object
}

func propertyNames(t *testing.T, schema map[string]any, pointer string) []string {
	t.Helper()
	properties, ok := schemaAt(t, schema, pointer)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema object %q has no properties", pointer)
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func jsonFieldNames(t *testing.T, value any) []string {
	t.Helper()
	typ := reflect.TypeOf(value)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" {
			name = typ.Field(i).Name
		}
		if name != "-" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func difference(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, value := range b {
		set[value] = true
	}
	var out []string
	for _, value := range a {
		if !set[value] {
			out = append(out, value)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
}

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
	raw, err := os.ReadFile(contractSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode contract schema: %v", err)
	}
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
		{"plan.items[].asset_refs[]", "#/properties/items/items/properties/asset_refs/items", semanticAssetRef{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := propertyNames(t, schema, tc.pointer)
			got := jsonFieldNames(t, tc.goValue)
			if missing := difference(want, got); len(missing) > 0 {
				t.Errorf("schema declares %v, but Go does not decode them", missing)
			}
			if missing := difference(got, want); len(missing) > 0 {
				t.Errorf("Go decodes %v, but schema forbids them", missing)
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
	raw, err := os.ReadFile(contractSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	itemProperties := propertyNames(t, schema, "#/properties/items/items")
	watermarkProperties := propertyNames(t, schema, "#/properties/watermark")
	for _, field := range []string{"image_preset_id", "motion_id", "motion_params", "params", "style"} {
		if !contains(itemProperties, field) {
			t.Errorf("items.%s is read by the compiler but absent from the schema", field)
		}
	}
	for _, field := range []string{"font_ref", "margin_px"} {
		if !contains(watermarkProperties, field) {
			t.Errorf("watermark.%s is read by the compiler but absent from the schema", field)
		}
	}
}

func TestStyleProfileVocabularyIsSchemaOwned(t *testing.T) {
	raw, err := os.ReadFile(contractSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	styleProfile, ok := schemaAt(t, schema, "#/properties/style_profile")["enum"].([]any)
	if !ok || len(styleProfile) == 0 {
		t.Fatal("schema style_profile must carry the producer-owned enum")
	}
	if fieldByName(reflect.TypeOf(semanticPlan{}), "style_profile") == "" {
		t.Fatal("semanticPlan must accept style_profile as an opaque string")
	}
}
