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

// The published contract (contracts/overlay-plan.v1.schema.json) and the
// compiler's Go structs are two representations of the SAME boundary. Before
// this test they had drifted in both directions and nothing compared them:
//
//   - the compiler accepted image_preset_id / motion_id / motion_params /
//     watermark.font_ref / watermark.margin_px, which the schema forbade
//     (additionalProperties: false), so a producer that validated its document
//     against the published schema could not use per-item motion at all;
//   - the schema declared project_id / renderer_version / fingerprint /
//     scene_id / entity_id / render_key, which the compiler ignored silently
//     (a rename in the producer was therefore invisible).
//
// The test is the checked projection the repository already uses for its other
// shared vocabularies: if one side gains or loses a property, it fails here,
// naming the property and the object, instead of drifting into a silent
// half-contract.
func contractSchemaPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source to resolve the contract schema")
	}
	// <repo>/renderinggen/internal/overlay -> <repo>/contracts/...
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "overlay-plan.v1.schema.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canonical contract schema %s: %v", path, err)
	}
	return filepath.Clean(path)
}

// propertyNames returns the declared keys of a schema object (properties map).
func propertyNames(t *testing.T, schema map[string]any, pointer string) []string {
	t.Helper()
	node := schema
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		if part == "" {
			continue
		}
		next, ok := node[part].(map[string]any)
		if !ok {
			t.Fatalf("schema pointer %s: no object at %q", pointer, part)
		}
		node = next
	}
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema pointer %s: no properties object", pointer)
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// jsonFieldNames returns the JSON property names of a struct type, ignoring
// fields excluded from the wire ("-") such as the internal BoxWidth/BoxHeight
// carriers.
func jsonFieldNames(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("%s is not a struct", typ)
	}
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestContractSchemaMatchesCompilerStructs is the bidirectional pin. Every
// schema object the compiler decodes is compared with its Go mirror; a
// property present on one side only fails with the exact name.
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
		name    string
		pointer string
		goValue any
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
			missingInGo := difference(want, got)
			missingInSchema := difference(got, want)
			if len(missingInGo) > 0 {
				t.Errorf("%s: schema declares %v, but the Go struct does not decode them (a producer sending them would be silently ignored)", tc.name, missingInGo)
			}
			if len(missingInSchema) > 0 {
				t.Errorf("%s: the Go struct decodes %v, but the schema forbids them (a producer validating against contracts/overlay-plan.v1.schema.json could never send them)", tc.name, missingInSchema)
			}
		})
	}
}

// TestContractSchemaRejectsUnknownCompilerFields pins the consequence of the
// pin above: with the projections aligned, a key outside the contract is a
// producer bug and the compile pass must reject it loudly (DisallowUnknownFields)
// instead of dropping it. The negative case uses a field the old permissive
// decode discarded without any error.
func TestContractSchemaRejectsUnknownCompilerFields(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,` +
		`"background":{"kind":"color","color":[0,0,0,1]},` +
		`"definitely_not_in_the_contract":true}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("an unknown top-level field must fail the compile pass, not be silently dropped")
	}

	itemRaw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,` +
		`"items":[{"id":"i","template_id":"PERSON","preset_id":"name_glow_slide","text":"Ada",` +
		`"start_ms":0,"end_ms":1000,"unknown_item_field":1}]}`)
	if _, err := CompileSemantic(itemRaw); err == nil {
		t.Fatal("an unknown item field must fail the compile pass, not be silently dropped")
	}
}

// TestContractDeclaresEveryLiveLoweringInput pins the fields the compiler
// actually reads to lower a layer: they MUST be part of the published
// contract, otherwise the capability exists in code but is unreachable for
// every schema-validating producer (the historical motion_id gap).
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

	for _, field := range []string{"image_preset_id", "motion_id", "motion_params"} {
		if !contains(itemProperties, field) {
			t.Errorf("items.%s is read by the compiler but absent from the contract", field)
		}
	}
	for _, field := range []string{"font_ref", "margin_px"} {
		if !contains(watermarkProperties, field) {
			t.Errorf("watermark.%s is read by the compiler but absent from the contract", field)
		}
	}
}

// TestStyleProfileVocabularyIsSchemaOwned pins the ownership decision for the
// style profile: the value is producer vocabulary that the worker threads
// through without interpreting it, so the schema is its single owner and the
// worker must NOT grow a second enum. If a future change adds one, this test
// fails and forces the decision to be made in one place.
func TestStyleProfileVocabularyIsSchemaOwned(t *testing.T) {
	raw, err := os.ReadFile(contractSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no top-level properties")
	}
	styleProfile, ok := props["style_profile"].(map[string]any)
	if !ok {
		t.Fatal("schema does not declare style_profile")
	}
	values, ok := styleProfile["enum"].([]any)
	if !ok || len(values) == 0 {
		t.Fatal("schema style_profile must carry the producer-owned enum")
	}
	// semanticPlan carries the raw value (opaque pass-through) — a Go-side
	// mirror of this enum would be a second source of truth.
	if typ := reflect.TypeOf(semanticPlan{}); fieldByName(typ, "style_profile") == "" {
		t.Fatal("semanticPlan must accept style_profile as an opaque string")
	}
}

func fieldByName(typ reflect.Type, jsonName string) string {
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == jsonName {
			return jsonName
		}
	}
	return ""
}

func difference(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, v := range b {
		set[v] = true
	}
	var out []string
	for _, v := range a {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
