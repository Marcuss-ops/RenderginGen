package processor

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

func prepareSchemaPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source to resolve the contract schema")
	}
	// <repo>/renderinggen/internal/processor -> <repo>/contracts/...
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "overlay-prepare.v1.schema.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canonical prepare schema %s: %v", path, err)
	}
	return filepath.Clean(path)
}

func schemaProperties(t *testing.T, schema map[string]any, pointer string) []string {
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

func structJSONFields(v any) []string {
	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func missingFrom(schemaSide, goSide []string) []string {
	have := make(map[string]bool, len(goSide))
	for _, v := range goSide {
		have[v] = true
	}
	var out []string
	for _, v := range schemaSide {
		if !have[v] {
			out = append(out, v)
		}
	}
	return out
}

// TestOverlayPrepareSchemaMatchesEnvelope pins the overlay.prepare contract
// (PipelineGen's pre-timing warm-up document) to the struct the worker decodes,
// in both directions. It is the same class of check the semantic plan contract
// has: without it, a field declared in the published schema could be dropped by
// the worker with no error, and a field the worker reads could be impossible
// for a schema-validating producer to send.
func TestOverlayPrepareSchemaMatchesEnvelope(t *testing.T) {
	raw, err := os.ReadFile(prepareSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode prepare schema: %v", err)
	}

	cases := []struct {
		name    string
		pointer string
		goValue any
	}{
		{"envelope", "#/", overlayPrepareEnvelope{}},
		{"intents[]", "#/properties/intents/items", overlayPrepareIntent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := schemaProperties(t, schema, tc.pointer)
			got := structJSONFields(tc.goValue)
			if missing := missingFrom(want, got); len(missing) > 0 {
				t.Errorf("%s: schema declares %v, but the envelope does not decode them (a schema-valid document would lose them silently)", tc.name, missing)
			}
			if extra := missingFrom(got, want); len(extra) > 0 {
				t.Errorf("%s: the envelope decodes %v, which the published schema forbids", tc.name, extra)
			}
		})
	}
}

// TestOverlayPrepareRejectsUnknownFields pins the strict decode that makes the
// pin above meaningful for the prepare job type too: the schema declares
// additionalProperties:false, so an unknown key is a producer bug.
func TestOverlayPrepareRejectsUnknownFields(t *testing.T) {
	valid := `{"schema_version":"` + overlayPrepareSchema + `","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,` +
		`"intents":[{"template_id":"PERSON","timing_state":"PENDING"}]}`
	if err := validateOverlayPrepare([]byte(valid)); err != nil {
		t.Fatalf("the canonical document must validate: %v", err)
	}
	unknownTop := strings.Replace(valid, `"plan_id":"p",`, `"plan_id":"p","not_in_contract":1,`, 1)
	if err := validateOverlayPrepare([]byte(unknownTop)); err == nil {
		t.Fatal("an unknown top-level field must fail the prepare validation")
	}
	unknownIntent := strings.Replace(valid, `"timing_state":"PENDING"`, `"timing_state":"PENDING","extra":1`, 1)
	if err := validateOverlayPrepare([]byte(unknownIntent)); err == nil {
		t.Fatal("an unknown intent field must fail the prepare validation")
	}
}
