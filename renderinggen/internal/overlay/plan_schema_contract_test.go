package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// The compiled plan is validated by the ENGINE, not by this repository: the CLI
// decodes plan.json with additionalProperties:false at every level and refuses
// the render when a property is not declared by its schema. That makes the
// engine's published schema a wire contract with no version on it, and the
// failure mode is misleading rather than silent: the render fails with a schema
// violation that names a property RenderingGen emits on purpose, so the report
// reads as a RenderingGen regression while the cause is an engine build whose
// plan contract is older than what this repository emits.
//
// This test closes that gap from the cheap side. It reads the engine's own
// schema when the sibling checkout is present (the same discovery the progress
// contract test uses for the engine's log source) and requires every property
// the compiled official presets emit to be declared at its path, so an
// emitted property the published contract does not declare is named from the
// contract itself, instead of only by a failed render.
//
// The schema is deliberately NOT copied into this repository. A checked-in
// duplicate of the engine's contract would be a second source of truth for the
// same fact — it would still be the copy that goes stale, and it would pass
// while the engine moved. The sibling document is the contract, and the
// checkout-independent half of the emitted shape is already pinned by
// text_glow_contract_test.go.

// renderPlanSchemaRelPath is the engine's published render-plan schema, the
// artifact the CLI validates against. The internal decoder is not the contract;
// the published schema is.
const renderPlanSchemaRelPath = "Chronon3d/schemas/json/chronon.render-plan.v2.schema.json"

// renderPlanSchemaPathEnv overrides the discovery of that file, for a build
// machine that has the engine's schemas somewhere other than beside this
// checkout.
const renderPlanSchemaPathEnv = "CHRONON_RENDER_PLAN_SCHEMA"

// schemaPropertyPaths maps a JSON path to the property names the schema
// declares AT that path, e.g. "layers[]" -> {id, type, text, ...} and
// "layers[].style" -> {font, fill, glow, ...}. A path with no entry is
// unconstrained (the schema says nothing about it).
type schemaPropertyPaths map[string]map[string]bool

// collectSchemaPropertyPaths walks a JSON Schema and records the declared
// property set of every object it constrains. `properties` declares an object;
// `items` maps an array path onto its element path; a subschema list
// (allOf/anyOf/oneOf) contributes the union of its alternatives, so a property
// declared by any accepted alternative is not reported as unknown.
func collectSchemaPropertyPaths(node any, path string, out schemaPropertyPaths) {
	switch typed := node.(type) {
	case map[string]any:
		if props, ok := typed["properties"].(map[string]any); ok {
			allowed, exists := out[path]
			if !exists {
				allowed = make(map[string]bool, len(props))
				out[path] = allowed
			}
			for name, sub := range props {
				allowed[name] = true
				collectSchemaPropertyPaths(sub, joinPath(path, name), out)
			}
		}
		if items, ok := typed["items"].(map[string]any); ok {
			collectSchemaPropertyPaths(items, path+"[]", out)
		}
	case []any:
		for _, alternative := range typed {
			collectSchemaPropertyPaths(alternative, path, out)
		}
	}
}

// joinPath appends a property name to a path, keeping array elements under the
// `[]` form the two walkers agree on.
func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// unknownPlanProperties walks a decoded plan and records every object property
// the schema does not declare at that path, keyed by its full JSON path so a
// failure names the exact property the engine would have rejected.
func unknownPlanProperties(node any, path string, declared schemaPropertyPaths, out map[string]bool) {
	switch typed := node.(type) {
	case map[string]any:
		allowed, constrained := declared[path]
		for name, value := range typed {
			if constrained && !allowed[name] {
				out[joinPath(path, name)] = true
			}
			unknownPlanProperties(value, joinPath(path, name), declared, out)
		}
	case []any:
		for _, element := range typed {
			unknownPlanProperties(element, path+"[]", declared, out)
		}
	}
}

// engineRenderPlanSchema loads the engine's published schema, or skips when no
// sibling checkout (and no override) is available. The schema is an external
// artifact: a standalone RenderingGen checkout cannot carry it, which is why
// this gate skips instead of inventing a local copy to compare against.
func engineRenderPlanSchema(t *testing.T) (schemaPropertyPaths, string) {
	t.Helper()
	raw, source := engineRenderPlanSchemaBytes(t)
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode render-plan schema %s: %v", source, err)
	}
	declared := schemaPropertyPaths{}
	collectSchemaPropertyPaths(document, "", declared)
	if len(declared) == 0 {
		t.Fatalf("render-plan schema %s declares no object properties; the check would pass vacuously", source)
	}
	return declared, source
}

// engineRenderPlanSchemaBytes resolves the schema document:
// CHRONON_RENDER_PLAN_SCHEMA first, then a sibling Chronon checkout discovered
// by walking up from this test's source (never a hardcoded home directory).
func engineRenderPlanSchemaBytes(t *testing.T) ([]byte, string) {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv(renderPlanSchemaPathEnv)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("%s=%s not readable: %v", renderPlanSchemaPathEnv, path, err)
		}
		return raw, path
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate this test's source to discover a sibling Chronon checkout")
	}
	dir := filepath.Dir(source)
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, filepath.FromSlash(renderPlanSchemaRelPath))
		if raw, err := os.ReadFile(candidate); err == nil {
			return raw, candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skipf("no sibling Chronon checkout with %s; set %s to validate the emitted plan against the engine's contract", renderPlanSchemaRelPath, renderPlanSchemaPathEnv)
	return nil, ""
}

// TestCompiledOfficialPresetsMatchTheEnginePlanSchema is the drift gate: every
// property the compiler emits for every official preset must be declared by the
// engine's render-plan schema, at the same path. It catches the plan/contract
// half of the divergence — a property the renderer would reject — from the
// contract itself, before any render is attempted.
func TestCompiledOfficialPresetsMatchTheEnginePlanSchema(t *testing.T) {
	declared, source := engineRenderPlanSchema(t)

	presetIDs := OfficialPresetIDs()
	if len(presetIDs) == 0 {
		t.Fatal("the official preset registry is empty; the schema check would pass vacuously")
	}
	for _, id := range presetIDs {
		t.Run(id, func(t *testing.T) {
			plan := certificationPlan(t, id)
			var decoded any
			if err := json.Unmarshal(mustMarshalPlan(t, plan), &decoded); err != nil {
				t.Fatalf("decode compiled plan: %v", err)
			}
			unknown := map[string]bool{}
			unknownPlanProperties(decoded, "", declared, unknown)
			if len(unknown) == 0 {
				return
			}
			t.Errorf("preset %s emits plan properties the engine schema (%s) does not declare:\n  %s\n"+
				"The engine refuses an undeclared property (additionalProperties:false), so a render of this plan fails before it starts. Either the compiler emits a property the published contract does not declare (fix the plan), or this schema is older than the engine build that will render it (refresh it, and rebuild the binary from the revision that carries the property).",
				id, source, strings.Join(keysOf(unknown), "\n  "))
		})
	}
}

// TestRenderPlanSchemaCheckDetectsAnUndeclaredProperty proves the walker fires.
// A conformance check that silently stops matching — an engine schema reshaped
// behind a $ref chain, a renamed path form — would pass every preset and
// certify nothing. The synthetic document below carries the exact shape that
// failed in production (a glow block the schema does not declare), and it must
// be reported at every nesting level.
func TestRenderPlanSchemaCheckDetectsAnUndeclaredProperty(t *testing.T) {
	declared := schemaPropertyPaths{}
	collectSchemaPropertyPaths(map[string]any{
		"properties": map[string]any{
			"layers": map[string]any{
				"items": map[string]any{
					"properties": map[string]any{
						"id":   map[string]any{"type": "string"},
						"type": map[string]any{"type": "string"},
						"style": map[string]any{
							"properties": map[string]any{
								"font": map[string]any{"type": "string"},
								"shadow": map[string]any{
									"properties": map[string]any{
										"color": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}, "", declared)

	plan := map[string]any{
		"layers": []any{
			map[string]any{
				"id":   "phrase",
				"type": "text",
				"style": map[string]any{
					"font":   "Poppins",
					"shadow": map[string]any{"color": "#000", "glow": true},
					"glow":   map[string]any{"radius": 12.0},
					"bogus":  "x",
				},
			},
		},
	}
	unknown := map[string]bool{}
	unknownPlanProperties(plan, "", declared, unknown)

	for _, want := range []string{"layers[].style.glow", "layers[].style.bogus", "layers[].style.shadow.glow"} {
		if !unknown[want] {
			t.Errorf("walker missed undeclared property %s; unknown=%v", want, keysOf(unknown))
		}
	}
	for _, mustBeKnown := range []string{"layers[].id", "layers[].type", "layers[].style.font", "layers[].style.shadow.color"} {
		if unknown[mustBeKnown] {
			t.Errorf("walker reported declared property %s as unknown; unknown=%v", mustBeKnown, keysOf(unknown))
		}
	}
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func mustMarshalPlan(t *testing.T, plan *Plan) []byte {
	t.Helper()
	raw, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return raw
}
