package batch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func batchSchemaPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source to resolve the contract schema")
	}
	// <repo>/renderinggen/internal/batch -> <repo>/contracts/...
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "renderinggen.batch-multilingual.v1.schema.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canonical batch schema %s: %v", path, err)
	}
	return filepath.Clean(path)
}

// propertyNames returns the declared keys of a schema object at a JSON pointer.
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

func jsonFieldNames(v any) []string {
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

// TestMultilingualManifestSchemaMatchesGoStructs pins the published batch
// contract to the structs that decode it, in both directions. Before this test
// the two had drifted exactly once and silently: the schema declared
// `audio_source_asset` (the localized voiceover) while LanguageJob had no such
// field, so a manifest that VALIDATED against the published schema was accepted
// and its voiceover was dropped — the language job rendered with the base audio
// and nothing reported the loss.
func TestMultilingualManifestSchemaMatchesGoStructs(t *testing.T) {
	raw, err := os.ReadFile(batchSchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode batch schema: %v", err)
	}

	cases := []struct {
		name    string
		pointer string
		goValue any
	}{
		{"manifest", "#/", MultilingualManifest{}},
		{"base", "#/properties/base", FlatJob{}},
		{"languages[]", "#/properties/languages/items", LanguageJob{}},
		{"asset", "#/$defs/asset", queue.AssetRef{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := propertyNames(t, schema, tc.pointer)
			got := jsonFieldNames(tc.goValue)
			if missing := difference(want, got); len(missing) > 0 {
				t.Errorf("%s: schema declares %v, but the Go struct does not decode them (a schema-valid manifest would lose them silently)", tc.name, missing)
			}
			if extra := difference(got, want); len(extra) > 0 {
				t.Errorf("%s: the Go struct decodes %v, which the published schema forbids", tc.name, extra)
			}
		})
	}
}

// TestMultilingualManifestPreservesAudioSourceAsset is the behavioural half of
// the pin above: a declared localized voiceover must reach the submitted job's
// assets (so the worker materializes it and the plan can bind to it by hash),
// and it must take part in the idempotency key.
func TestMultilingualManifestPreservesAudioSourceAsset(t *testing.T) {
	voice := queue.AssetRef{Hash: "voicehash", LogicalPath: "audio/it.mp3"}
	m := MultilingualManifest{
		Schema:  SchemaMultilingualBatchV1,
		BatchID: "batch-1",
		Base: FlatJob{
			ID:         "base",
			RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1"}`),
			Assets:     []queue.AssetRef{{Hash: "bh", LogicalPath: "videos/base.mp4"}},
		},
		Langs: []LanguageJob{{
			Language:         "it",
			RenderPlan:       json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1"}`),
			Assets:           []queue.AssetRef{{Hash: "sh", LogicalPath: "subtitles/it.ass"}},
			AudioSourceAsset: &voice,
		}},
	}
	jobs, err := ExpandMultilingual(m)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 (base + one language)", len(jobs))
	}
	got := jobs[1].Assets
	var found bool
	for _, a := range got {
		if a == voice {
			found = true
		}
	}
	if !found {
		t.Fatalf("localized voiceover is missing from the submitted job assets: %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("language assets = %+v, want the declared ASS track plus the voiceover", got)
	}

	// The key must cover the voiceover: a manifest whose voiceover changed is
	// different work, not a replay.
	withoutVoice := m
	withoutVoice.Langs = []LanguageJob{{Language: "it", RenderPlan: m.Langs[0].RenderPlan, Assets: m.Langs[0].Assets}}
	other, err := ExpandMultilingual(withoutVoice)
	if err != nil {
		t.Fatalf("expand without voice: %v", err)
	}
	if jobs[1].IdempotencyKey == other[1].IdempotencyKey {
		t.Fatal("idempotency key must change when the localized voiceover changes")
	}
}

// TestMultilingualManifestRejectsMalformedVoiceover pins fail-closed handling:
// an unusable declaration must not be accepted (and then ignored).
func TestMultilingualManifestRejectsMalformedVoiceover(t *testing.T) {
	base := MultilingualManifest{
		Schema:  SchemaMultilingualBatchV1,
		BatchID: "batch-1",
		Base: FlatJob{
			ID:         "base",
			RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1"}`),
		},
	}
	cases := []struct {
		name  string
		voice *queue.AssetRef
		asset []queue.AssetRef
	}{
		{name: "missing hash", voice: &queue.AssetRef{LogicalPath: "audio/it.mp3"}},
		{name: "missing logical path", voice: &queue.AssetRef{Hash: "h"}},
		{name: "conflicting duplicate path", voice: &queue.AssetRef{Hash: "other", LogicalPath: "audio/it.mp3"},
			asset: []queue.AssetRef{{Hash: "h", LogicalPath: "audio/it.mp3"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Langs = []LanguageJob{{
				Language:         "it",
				RenderPlan:       json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1"}`),
				Assets:           tc.asset,
				AudioSourceAsset: tc.voice,
			}}
			if _, err := ExpandMultilingual(m); err == nil {
				t.Fatal("a malformed voiceover declaration must fail closed")
			}
		})
	}
}
