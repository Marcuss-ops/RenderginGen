// Package schemaval is a small, dependency-free JSON-Schema (draft 2020-12
// subset) validator. It exists so the boundary contract tests can validate an
// emitted document against the CANONICAL on-disk schema instead of a
// hand-maintained copy of it.
//
// Implemented validation keywords: $ref (local JSON pointers), type, const,
// enum, required, properties, additionalProperties, items, minItems, maxItems,
// minimum, maximum, exclusiveMinimum, exclusiveMaximum, minLength, maxLength,
// pattern, allOf, anyOf, oneOf, if/then/else. Annotation-only keywords ($schema,
// $id, title, description, $comment, default, examples, deprecated, readOnly,
// writeOnly, format) are accepted and, per draft 2020-12, do not constrain the
// instance.
//
// FAIL-CLOSED on everything else: ValidateFile rejects a schema that uses any
// keyword this package does not implement (recursively, in every subschema
// position), AND rejects an implemented keyword whose VALUE has a shape this
// package cannot enforce (a `type` array holding an unknown name, a non-array
// `enum`, a non-string `pattern`, a `$ref` that is not a string, …). The
// historical behavior ignored unknown keywords, so a contract written with
// `pattern`/`maxLength`/`uniqueItems` validated LESS than it claimed and the
// boundary tests passed anyway — a false guarantee. A schema author now gets a
// loud, explicit failure instead, and the fix is either to implement the keyword
// or to remove it from the contract.
//
// Two traps this package must never fall into, both of which make a supported
// keyword validate NOTHING:
//
//   - siblings of $ref. Draft 2020-12 applies `$ref` AND the keywords next to
//     it, so `{"$ref": "#/$defs/x", "required": [...]}` constrains with both.
//     Returning as soon as the reference resolves would drop the sibling
//     constraint silently.
//   - a `type` in array form (`["string", "null"]`). Type-asserting
//     `schema["type"].(string)` and ignoring the failure means the constraint
//     disappears; both forms are enforced below.
package schemaval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// knownTypeNames is the complete set of JSON Schema primitive type names, plus
// "null" (which the data model of `any` can represent). A name outside this set
// is a schema authoring bug, not a constraint to ignore.
var knownTypeNames = map[string]bool{
	"object": true, "array": true, "string": true, "number": true,
	"integer": true, "boolean": true, "null": true,
}

// maxRefDepth bounds $ref indirection. A self-referential schema
// ({"$ref": "#"}) would otherwise recurse until the stack dies; the error is
// explicit instead.
const maxRefDepth = 64

// patternCache memoizes compiled `pattern` regexps. The keyword is evaluated
// once per matching string INSTANCE, so compiling per value put a regexp
// compilation (and its allocation) inside the per-item boundary check of every
// contract test. Keyed by pattern text, which comes from the schemas, so the
// cache is bounded by the contract surface.
var patternCache sync.Map // string -> *regexp.Regexp

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if cached, ok := patternCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patternCache.Store(pattern, re)
	return re, nil
}

// valueKeywords are leaf validation keywords: they constrain the instance and
// contain no subschemas to recurse into.
var valueKeywords = map[string]bool{
	"$ref": true, "type": true, "const": true, "enum": true,
	"required": true, "minItems": true, "maxItems": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"minLength": true, "maxLength": true, "pattern": true,
}

// annotationKeywords cannot change validation (draft 2020-12 says so for
// `format`, which is annotation-only by default).
var annotationKeywords = map[string]bool{
	"$schema": true, "$id": true, "$anchor": true, "$comment": true,
	"title": true, "description": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true, "format": true,
}

// singleSubschema keywords hold one subschema (or a boolean schema).
var singleSubschema = map[string]bool{
	"additionalProperties": true, "items": true, "if": true, "then": true, "else": true,
}

// subschemaArray keywords hold an array of subschemas.
var subschemaArray = map[string]bool{"allOf": true, "anyOf": true, "oneOf": true}

// subschemaMap keywords hold a map of subschemas.
var subschemaMap = map[string]bool{
	"properties": true, "$defs": true, "definitions": true,
}

// checkValueKeywordShapes rejects an implemented keyword whose value cannot be
// enforced as declared. Without it, a shape the validator does not understand
// degrades to "no constraint": `type: ["string","null"]` and `enum: "a"` both
// used to pass every document, so the gate reported a boundary it never checked.
func checkValueKeywordShapes(m map[string]any, at string, unsupported *[]string) {
	if raw, ok := m["$ref"]; ok {
		if _, isString := raw.(string); !isString {
			*unsupported = append(*unsupported, at+".$ref (not a string)")
		}
	}
	if raw, ok := m["type"]; ok {
		switch value := raw.(type) {
		case string:
			if !knownTypeNames[value] {
				*unsupported = append(*unsupported, at+".type (unknown type name "+value+")")
			}
		case []any:
			if len(value) == 0 {
				*unsupported = append(*unsupported, at+".type (empty array)")
			}
			for _, name := range value {
				s, isString := name.(string)
				if !isString || !knownTypeNames[s] {
					*unsupported = append(*unsupported, at+".type (array element is not a known type name)")
					break
				}
			}
		default:
			*unsupported = append(*unsupported, at+".type (not a string or array of strings)")
		}
	}
	if raw, ok := m["enum"]; ok {
		list, isArray := raw.([]any)
		if !isArray || len(list) == 0 {
			*unsupported = append(*unsupported, at+".enum (not a non-empty array)")
		}
	}
	if raw, ok := m["required"]; ok {
		list, isArray := raw.([]any)
		if !isArray {
			*unsupported = append(*unsupported, at+".required (not an array)")
		} else {
			for _, name := range list {
				if _, isString := name.(string); !isString {
					*unsupported = append(*unsupported, at+".required (element is not a string)")
					break
				}
			}
		}
	}
	if raw, ok := m["pattern"]; ok {
		pattern, isString := raw.(string)
		if !isString {
			*unsupported = append(*unsupported, at+".pattern (not a string)")
		} else if _, err := compilePattern(pattern); err != nil {
			*unsupported = append(*unsupported, at+".pattern (does not compile: "+err.Error()+")")
		}
	}
	for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minItems", "maxItems", "minLength", "maxLength"} {
		if raw, ok := m[key]; ok {
			if _, isNumber := raw.(float64); !isNumber {
				*unsupported = append(*unsupported, at+"."+key+" (not a number)")
			}
		}
	}
}

// checkSupported walks the schema and returns an error naming every keyword
// this package does not implement (or cannot enforce), so an unsupported
// constraint is never silently ignored.
func checkSupported(node map[string]any, path string) error {
	var unsupported []string
	var walk func(map[string]any, string)
	walk = func(m map[string]any, at string) {
		checkValueKeywordShapes(m, at, &unsupported)
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := m[key]
			switch {
			case key == "$ref" || valueKeywords[key] || annotationKeywords[key]:
			case singleSubschema[key]:
				if sub, ok := value.(map[string]any); ok {
					walk(sub, at+"."+key)
				} else if _, isBool := value.(bool); !isBool {
					unsupported = append(unsupported, at+"."+key+" (not an object or boolean schema)")
				}
			case subschemaArray[key]:
				arr, ok := value.([]any)
				if !ok {
					unsupported = append(unsupported, at+"."+key+" (not an array of schemas)")
					continue
				}
				for i, item := range arr {
					if sub, ok := item.(map[string]any); ok {
						walk(sub, fmt.Sprintf("%s.%s[%d]", at, key, i))
					}
				}
			case subschemaMap[key]:
				m, ok := value.(map[string]any)
				if !ok {
					unsupported = append(unsupported, at+"."+key+" (not a map of schemas)")
					continue
				}
				names := make([]string, 0, len(m))
				for name := range m {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					if sub, ok := m[name].(map[string]any); ok {
						walk(sub, at+"."+key+"."+name)
					}
				}
			default:
				unsupported = append(unsupported, at+"."+key)
			}
		}
	}
	walk(node, path)
	if len(unsupported) > 0 {
		return fmt.Errorf("unsupported JSON-Schema keyword(s) — the validator would silently ignore them: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

// CheckSupported reports whether a schema uses only keywords this validator
// implements. It lets a contract-owning test fail on an unimplemented
// constraint instead of discovering it as a boundary that validated nothing.
func CheckSupported(raw []byte) error {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	return checkSupported(schema, "#")
}

// ValidateFile validates the JSON document in doc against the schema at
// schemaPath.
func ValidateFile(doc []byte, schemaPath string) error {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", schemaPath, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("decode schema %s: %w", schemaPath, err)
	}
	if err := checkSupported(schema, "#"); err != nil {
		return fmt.Errorf("schema %s: %w", schemaPath, err)
	}
	var d any
	if err := json.Unmarshal(doc, &d); err != nil {
		return fmt.Errorf("decode document: %w", err)
	}
	return validate(d, schema, schema, "#", 0)
}

func validate(doc any, schema, root map[string]any, path string, refDepth int) error {
	if ref, ok := schema["$ref"].(string); ok {
		if refDepth >= maxRefDepth {
			return fmt.Errorf("%s: $ref chain longer than %d (self-referential schema?)", path, maxRefDepth)
		}
		target, err := resolveRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		// Siblings of $ref are applied too (draft 2020-12), so the reference is
		// checked first and then the rest of THIS schema's keywords are applied
		// by falling through — never by returning here.
		if err := validate(doc, target, root, path, refDepth+1); err != nil {
			return err
		}
	}
	if c, ok := schema["const"]; ok {
		if !deepEqual(doc, c) {
			return fmt.Errorf("%s: value %v does not equal const %v", path, doc, c)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if deepEqual(doc, e) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: value %v is not in enum", path, doc)
		}
	}
	if err := validateType(doc, schema, path); err != nil {
		return err
	}
	for _, key := range []string{"allOf"} {
		if list, ok := schema[key].([]any); ok {
			for i, sub := range list {
				m, ok := sub.(map[string]any)
				if !ok {
					continue
				}
				if err := validate(doc, m, root, fmt.Sprintf("%s/%s[%d]", path, key, i), refDepth); err != nil {
					return err
				}
			}
		}
	}
	if list, ok := schema["anyOf"].([]any); ok {
		var lastErr error
		okAny := false
		for _, sub := range list {
			m, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			if err := validate(doc, m, root, path, refDepth); err == nil {
				okAny = true
				break
			} else {
				lastErr = err
			}
		}
		if !okAny {
			return fmt.Errorf("%s: does not match anyOf: %v", path, lastErr)
		}
	}
	if list, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, sub := range list {
			m, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			if err := validate(doc, m, root, path, refDepth); err == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matches %d oneOf branches, want exactly 1", path, matches)
		}
	}
	if cond, ok := schema["if"].(map[string]any); ok {
		if err := validate(doc, cond, root, path, refDepth); err == nil {
			if thenS, ok := schema["then"].(map[string]any); ok {
				if err := validate(doc, thenS, root, path, refDepth); err != nil {
					return err
				}
			}
		} else if elseS, ok := schema["else"].(map[string]any); ok {
			if err := validate(doc, elseS, root, path, refDepth); err != nil {
				return err
			}
		}
	}

	switch d := doc.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := d[name]; !present {
					return fmt.Errorf("%s: missing required property %q", path, name)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for key, val := range d {
			if sub, ok := props[key].(map[string]any); ok {
				if err := validate(val, sub, root, path+"."+key, refDepth); err != nil {
					return err
				}
				continue
			}
			if ap, ok := schema["additionalProperties"]; ok {
				switch apv := ap.(type) {
				case bool:
					if !apv {
						return fmt.Errorf("%s: additional property %q is not allowed", path, key)
					}
				case map[string]any:
					if err := validate(val, apv, root, path+"."+key, refDepth); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range d {
				if err := validate(item, items, root, fmt.Sprintf("%s[%d]", path, i), refDepth); err != nil {
					return err
				}
			}
		}
		if min, ok := num(schema["minItems"]); ok && float64(len(d)) < min {
			return fmt.Errorf("%s: %d items, want >= %v", path, len(d), min)
		}
		if max, ok := num(schema["maxItems"]); ok && float64(len(d)) > max {
			return fmt.Errorf("%s: %d items, want <= %v", path, len(d), max)
		}
	case string:
		if min, ok := num(schema["minLength"]); ok && float64(len(d)) < min {
			return fmt.Errorf("%s: string length %d, want >= %v", path, len(d), min)
		}
		if max, ok := num(schema["maxLength"]); ok && float64(len(d)) > max {
			return fmt.Errorf("%s: string length %d, want <= %v", path, len(d), max)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := compilePattern(pattern)
			if err != nil {
				return fmt.Errorf("%s: schema pattern %q does not compile: %w", path, pattern, err)
			}
			if !re.MatchString(d) {
				return fmt.Errorf("%s: value %q does not match pattern %q", path, d, pattern)
			}
		}
	case float64:
		if min, ok := num(schema["minimum"]); ok && d < min {
			return fmt.Errorf("%s: %v < minimum %v", path, d, min)
		}
		if max, ok := num(schema["maximum"]); ok && d > max {
			return fmt.Errorf("%s: %v > maximum %v", path, d, max)
		}
		if min, ok := num(schema["exclusiveMinimum"]); ok && d <= min {
			return fmt.Errorf("%s: %v <= exclusiveMinimum %v", path, d, min)
		}
		if max, ok := num(schema["exclusiveMaximum"]); ok && d >= max {
			return fmt.Errorf("%s: %v >= exclusiveMaximum %v", path, d, max)
		}
	}
	return nil
}

// validateType enforces `type`, in both of its spellings. The array form is a
// union (the instance must match at least one name), and an unknown name can no
// longer reach here: checkValueKeywordShapes rejects it at load time.
func validateType(doc any, schema map[string]any, path string) error {
	switch declared := schema["type"].(type) {
	case string:
		if !hasType(doc, declared) {
			return fmt.Errorf("%s: want type %s, got %T", path, declared, doc)
		}
	case []any:
		for _, name := range declared {
			if want, ok := name.(string); ok && hasType(doc, want) {
				return nil
			}
		}
		return fmt.Errorf("%s: want one of types %v, got %T", path, declared, doc)
	}
	return nil
}

// hasType reports whether doc matches one JSON Schema primitive type name. The
// default is FALSE: a name this function does not implement must not be treated
// as "everything matches", which is how an unknown type silently disabled the
// whole constraint.
func hasType(doc any, want string) bool {
	switch want {
	case "object":
		_, ok := doc.(map[string]any)
		return ok
	case "array":
		_, ok := doc.([]any)
		return ok
	case "string":
		_, ok := doc.(string)
		return ok
	case "boolean":
		_, ok := doc.(bool)
		return ok
	case "integer":
		f, ok := doc.(float64)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := doc.(float64)
		return ok
	case "null":
		return doc == nil
	}
	return false
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// deepEqual compares two decoded JSON values for JSON equality. It compares the
// CANONICAL encodings (json.Marshal sorts object keys, and every JSON number is
// an int64/float64 here) rather than using reflect.DeepEqual, whose numeric and
// nil-vs-empty distinctions are not JSON's: `[]` must not equal `{}`, and a
// decoded 1.0 must equal a decoded 1.
func deepEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// resolveRef resolves a local JSON pointer like "#/$defs/easing".
func resolveRef(root map[string]any, ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported external $ref %q", ref)
	}
	var current any = root
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
		current, ok = m[seg]
		if !ok {
			return nil, fmt.Errorf("$ref %q segment %q not found", ref, seg)
		}
	}
	m, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q does not resolve to an object", ref)
	}
	return m, nil
}
