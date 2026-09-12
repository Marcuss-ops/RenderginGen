package schemaval

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeSchema(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestUnsupportedKeywordsFailClosed pins the fix for the silent false
// guarantee: a schema using a keyword this validator does not implement must be
// rejected, not partially enforced. The historical behavior ignored `pattern`
// and `maxLength` (both used by the real Chronon v2 schema), so a document that
// violated them validated cleanly.
func TestUnsupportedKeywordsFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		wantSub string
	}{
		{"top-level unsupported keyword", `{"type":"object","uniqueItems":true}`, "uniqueItems"},
		{"nested in properties", `{"type":"object","properties":{"a":{"multipleOf":2}}}`, "multipleOf"},
		{"nested in items", `{"type":"array","items":{"contains":{"type":"string"}}}`, "contains"},
		{"nested in $defs", `{"$defs":{"x":{"patternProperties":{}}},"type":"object"}`, "patternProperties"},
		{"nested in oneOf", `{"oneOf":[{"type":"string"}],"unevaluatedProperties":false}`, "unevaluatedProperties"},
		{"nested in if/then", `{"if":{"type":"object"},"then":{"dependentRequired":{"a":["b"]}}}`, "dependentRequired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFile([]byte(`{}`), writeSchema(t, tc.schema))
			if err == nil {
				t.Fatalf("schema %s must fail closed", tc.schema)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not name the unsupported keyword %q", err, tc.wantSub)
			}
		})
	}
}

// TestAnnotationKeywordsAreAllowed pins that the fail-closed check does not fire
// on keywords that cannot change validation, per the 2020-12 specification.
func TestAnnotationKeywordsAreAllowed(t *testing.T) {
	schema := `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"$id":"https://example.test/x",
		"title":"t","description":"d","$comment":"c",
		"default":{},"examples":[{}],"format":"date-time",
		"deprecated":false,"readOnly":false,"writeOnly":false,
		"type":"object","properties":{"a":{"type":"string"}}
	}`
	if err := ValidateFile([]byte(`{"a":"x"}`), writeSchema(t, schema)); err != nil {
		t.Fatalf("annotation-only schema must validate: %v", err)
	}
}

// TestImplementedKeywordsAreEnforced pins that the keywords added for the real
// contracts actually constrain the instance.
func TestImplementedKeywordsAreEnforced(t *testing.T) {
	schema := writeSchema(t, `{
		"type":"object",
		"properties":{
			"code":{"type":"string","pattern":"^[A-Z]{2}$","maxLength":2},
			"n":{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":10}
		}
	}`)
	if err := ValidateFile([]byte(`{"code":"AB","n":5}`), schema); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	cases := map[string]string{
		"pattern":          `{"code":"ab","n":5}`,
		"maxLength":        `{"code":"ABC","n":5}`,
		"exclusiveMinimum": `{"code":"AB","n":0}`,
		"exclusiveMaximum": `{"code":"AB","n":10}`,
	}
	for keyword, doc := range cases {
		if err := ValidateFile([]byte(doc), schema); err == nil {
			t.Errorf("%s was not enforced for %s", keyword, doc)
		}
	}
}

// TestContractSchemasUseOnlySupportedKeywords walks every published contract in
// this repository and fails if one declares a constraint the validator would
// ignore. This is the data-driven half of the fix: the boundary tests validate
// against these files, so an unimplemented keyword in a contract is a guarantee
// that was never checked.
func TestContractSchemasUseOnlySupportedKeywords(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	// <repo>/renderinggen/internal/schemaval -> <repo>/contracts
	dir := filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts")
	files, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no contract schemas found under %s", dir)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckSupported(raw); err != nil {
			t.Errorf("%s: %v", filepath.Base(file), err)
		}
	}
}
