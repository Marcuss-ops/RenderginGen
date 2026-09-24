// Package contractschema is the single reader of the checked-in contract JSON
// Schemas for the RenderingGen test suites.
//
// Three suites assert that a Go struct and a published schema declare the same
// fields — internal/overlay (renderinggen.overlay-plan.v1),
// internal/renderbatch (the same document's asset_refs entry) and
// internal/batch (renderinggen.batch-multilingual.v1). Each one had grown its
// own JSON-pointer walker, its own struct-tag reader and its own set
// difference, so "compare the schema with the struct" was implemented three
// times with three subtly different behaviours (one skipped fields tagged "-",
// one silently continued on an empty path segment). Sharing the comparison is
// what makes the three suites comparable: a parity failure now means the field
// sets differ, never that one copy of the walker does.
//
// The package is test-only in practice — nothing in a production binary imports
// it — but it is a normal package (not a _test.go file) so every suite can
// import the same implementation.
package contractschema

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

// Dir returns the repository's contracts directory.
func Dir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the contractschema source to resolve the contracts directory")
	}
	// <repo>/renderinggen/internal/contractschema -> <repo>/contracts
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts"))
}

// Load reads and decodes one checked-in schema by file name (for example
// "overlay-plan.v1.schema.json"). A missing or malformed schema fails the test:
// a parity check that silently reads nothing would pass vacuously.
func Load(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(Dir(t), name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract schema %s: %v", path, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode contract schema %s: %v", path, err)
	}
	if len(schema) == 0 {
		t.Fatalf("contract schema %s decoded to an empty document", path)
	}
	return schema
}

// At walks a JSON pointer such as "#/properties/items/items/properties/params"
// and returns the object it names. A pointer that traverses a non-object or
// misses a segment fails the test rather than returning nil, because a nil
// object would make the caller's field-set comparison vacuously empty.
func At(t *testing.T, schema map[string]any, pointer string) map[string]any {
	t.Helper()
	value := any(schema)
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		if part == "" {
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("schema pointer %q traverses a non-object at %q", pointer, part)
		}
		value, ok = object[part]
		if !ok {
			t.Fatalf("schema pointer %q is missing %q", pointer, part)
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema pointer %q is not an object", pointer)
	}
	return object
}

// PropertyNames returns the sorted keys of the schema object's "properties"
// block.
func PropertyNames(t *testing.T, schema map[string]any, pointer string) []string {
	t.Helper()
	properties, ok := At(t, schema, pointer)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema object %q declares no properties", pointer)
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// JSONFieldNames returns the sorted JSON names of a struct's exported fields, the
// wire spelling every tag in this repository uses. Fields tagged "-" are absent
// from the document and are therefore not part of the comparison.
func JSONFieldNames(value any) []string {
	typ := reflect.TypeOf(value)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	names := make([]string, 0, typ.NumField())
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

// Difference returns the entries of a that are missing from b, in a's order.
func Difference(a, b []string) []string {
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

// Contains reports whether list holds want.
func Contains(list []string, want string) bool {
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
}
