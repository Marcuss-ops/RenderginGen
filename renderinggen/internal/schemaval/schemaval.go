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
// position). The historical behavior ignored unknown keywords, so a contract
// written with `pattern`/`maxLength`/`uniqueItems` validated LESS than it
// claimed and the boundary tests passed anyway — a false guarantee. A schema
// author now gets a loud, explicit failure instead, and the fix is either to
// implement the keyword or to remove it from the contract.
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
)

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

// checkSupported walks the schema and returns an error naming every keyword
// this package does not implement, so an unsupported constraint is never
// silently ignored.
func checkSupported(node map[string]any, path string) error {
	var unsupported []string
	var walk func(map[string]any, string)
	walk = func(m map[string]any, at string) {
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
	return validate(d, schema, schema, "#")
}

func validate(doc any, schema, root map[string]any, path string) error {
	if ref, ok := schema["$ref"].(string); ok {
		target, err := resolveRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return validate(doc, target, root, path)
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
	if t, ok := schema["type"].(string); ok {
		if !hasType(doc, t) {
			return fmt.Errorf("%s: want type %s, got %T", path, t, doc)
		}
	}
	for _, key := range []string{"allOf"} {
		if list, ok := schema[key].([]any); ok {
			for i, sub := range list {
				m, ok := sub.(map[string]any)
				if !ok {
					continue
				}
				if err := validate(doc, m, root, fmt.Sprintf("%s/%s[%d]", path, key, i)); err != nil {
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
			if err := validate(doc, m, root, path); err == nil {
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
			if err := validate(doc, m, root, path); err == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matches %d oneOf branches, want exactly 1", path, matches)
		}
	}
	if cond, ok := schema["if"].(map[string]any); ok {
		if err := validate(doc, cond, root, path); err == nil {
			if thenS, ok := schema["then"].(map[string]any); ok {
				if err := validate(doc, thenS, root, path); err != nil {
					return err
				}
			}
		} else if elseS, ok := schema["else"].(map[string]any); ok {
			if err := validate(doc, elseS, root, path); err != nil {
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
				if err := validate(val, sub, root, path+"."+key); err != nil {
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
					if err := validate(val, apv, root, path+"."+key); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range d {
				if err := validate(item, items, root, fmt.Sprintf("%s[%d]", path, i)); err != nil {
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
			re, err := regexp.Compile(pattern)
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
	}
	return true
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

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
