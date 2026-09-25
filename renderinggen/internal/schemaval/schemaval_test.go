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

// TestRefSiblingsAreEnforced pins the first draft-2020-12 trap: `$ref` does not
// suspend the keywords declared NEXT TO it. Returning as soon as the reference
// resolved made `{"$ref": …, "additionalProperties": false}` accept every
// document — a schema that claimed a constraint it never applied, which is the
// same false guarantee the fail-closed check exists to prevent.
func TestRefSiblingsAreEnforced(t *testing.T) {
	// The referenced schema owns only `required`; the object's shape belongs to
	// the schema that carries the reference. Draft 2020-12 applies both schemas
	// independently — a $ref does NOT merge its target's `properties` into its
	// siblings — so each half has to be enforced on its own evidence.
	schema := writeSchema(t, `{
		"$defs": {"requiresA": {"required": ["a"]}},
		"$ref": "#/$defs/requiresA",
		"type": "object",
		"properties": {"a": {"type": "string"}},
		"additionalProperties": false
	}`)

	if err := ValidateFile([]byte(`{"a":"x"}`), schema); err != nil {
		t.Fatalf("the document satisfies the reference and its siblings: %v", err)
	}
	// The reference itself must still be applied…
	if err := ValidateFile([]byte(`{}`), schema); err == nil {
		t.Error("the referenced `required` was not applied: the document has no `a`")
	}
	// …and so must the sibling `additionalProperties`, which is why the schema
	// carrying the reference also has to carry `properties`.
	if err := ValidateFile([]byte(`{"a":"x","extra":1}`), schema); err == nil {
		t.Error("the sibling `additionalProperties` was not applied: `extra` is not allowed")
	}
	// The sibling `properties` must constrain the value, not just match the name.
	if err := ValidateFile([]byte(`{"a":7}`), schema); err == nil {
		t.Error("the sibling `properties` type constraint was not applied: 7 is not a string")
	}
}

// TestTypeArrayAndNullFormsAreEnforced pins the second trap: `type` is legal as
// an array of names (a union) and "null" is a legal name in it. Type-asserting
// the string form dropped both, so the constraint vanished from a document that
// declared it.
func TestTypeArrayAndNullFormsAreEnforced(t *testing.T) {
	schema := writeSchema(t, `{
		"type": "object",
		"properties": {
			"maybe": {"type": ["string", "null"]},
			"nothing": {"type": "null"}
		},
		"required": ["maybe", "nothing"]
	}`)

	for _, doc := range []string{`{"maybe":null,"nothing":null}`, `{"maybe":"text","nothing":null}`} {
		if err := ValidateFile([]byte(doc), schema); err != nil {
			t.Fatalf("declared type union rejected %s: %v", doc, err)
		}
	}
	if err := ValidateFile([]byte(`{"maybe":7,"nothing":null}`), schema); err == nil {
		t.Error("the array-form type union was not enforced: 7 is neither string nor null")
	}
	if err := ValidateFile([]byte(`{"maybe":"text","nothing":"text"}`), schema); err == nil {
		t.Error("type \"null\" was not enforced: a string is not null")
	}
}

// TestKeywordValueShapesFailClosed pins that an implemented keyword whose VALUE
// the validator cannot enforce is rejected at load time. Each of these shapes
// used to pass silently and therefore validated nothing.
func TestKeywordValueShapesFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{"unknown type name", `{"type":"strings"}`, "unknown type name"},
		{"empty type union", `{"type":[]}`, "empty array"},
		{"type union with a non-string", `{"type":["string",2]}`, "not a known type name"},
		{"numeric type", `{"type":7}`, "not a string or array of strings"},
		{"non-string $ref", `{"$ref":7}`, "$ref (not a string)"},
		{"non-array enum", `{"enum":"a"}`, "enum (not a non-empty array)"},
		{"non-array required", `{"required":"a"}`, "required (not an array)"},
		{"required element not a string", `{"required":[1]}`, "element is not a string"},
		{"non-string pattern", `{"pattern":7}`, "pattern (not a string)"},
		{"uncompilable pattern", `{"pattern":"["}`, "does not compile"},
		{"non-numeric minimum", `{"minimum":"0"}`, "minimum (not a number)"},
		{"non-numeric maxLength", `{"maxLength":"2"}`, "maxLength (not a number)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFile([]byte(`{}`), writeSchema(t, tc.schema))
			if err == nil {
				t.Fatalf("schema %s must fail closed", tc.schema)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

// TestNotIsEnforced pins the `not` keyword, the one the published v3 extension
// contract uses to forbid stroke.color+stroke.gradient together and to close the
// path-command variants. Before it was implemented, CheckSupported rejected the
// contract outright; ignoring it instead would have silently accepted a document
// that declared two mutually exclusive stroke paints.
func TestNotIsEnforced(t *testing.T) {
	schema := writeSchema(t, `{
		"type":"object",
		"properties": {
			"stroke": {
				"type":"object",
				"properties": {"color":{"type":"string"},"gradient":{"type":"object"}},
				"not": {"required": ["color", "gradient"]}
			},
			"command": {
				"type":"object",
				"required":["type"],
				"properties": {"type":{"enum":["move_to","close"]},"point":{"type":"array"}}
			}
		},
		"allOf": [
			{"if": {"properties": {"command": {"type": "object"}}, "required": ["command"]},
			 "then": {"properties": {"command": {"not": {"required": ["point"]}}}}}
		]
	}`)

	// A stroke with only one paint is valid.
	if err := ValidateFile([]byte(`{"stroke":{"color":"#FFFFFF"}}`), schema); err != nil {
		t.Fatalf("single stroke paint rejected: %v", err)
	}
	// Both paints at once must be rejected by `not`.
	if err := ValidateFile([]byte(`{"stroke":{"color":"#FFFFFF","gradient":{}}}`), schema); err == nil {
		t.Fatal("stroke with color AND gradient must be rejected by not")
	}
	// The conditional `not` closes the move_to variant: a point is forbidden.
	if err := ValidateFile([]byte(`{"command":{"type":"move_to","point":[0,0]}}`), schema); err == nil {
		t.Fatal("move_to with a point must be rejected by the conditional not")
	}
	// A command with no point satisfies the same `not`.
	if err := ValidateFile([]byte(`{"command":{"type":"close"}}`), schema); err != nil {
		t.Fatalf("close without a point rejected: %v", err)
	}
}

// TestSelfReferentialRefFailsExplicitly pins the $ref depth guard. A schema that
// references itself is now an error instead of unbounded recursion.
func TestSelfReferentialRefFailsExplicitly(t *testing.T) {
	err := ValidateFile([]byte(`{}`), writeSchema(t, `{
		"$defs": {"x": {"$ref": "#/$defs/x"}},
		"$ref": "#/$defs/x"
	}`))
	if err == nil {
		t.Fatal("a self-referential schema must fail explicitly")
	}
	if !strings.Contains(err.Error(), "$ref chain") {
		t.Fatalf("unexpected error: %v", err)
	}
}
