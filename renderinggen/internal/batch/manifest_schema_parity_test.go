package batch

import (
	"encoding/json"
	"testing"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
)

const batchContractSchema = "renderinggen.batch-multilingual.v1.schema.json"

// TestMultilingualManifestSchemaMatchesGoStructs pins the published batch
// contract to the structs that decode it, in both directions. Before this test
// the two had drifted exactly once and silently: the schema declared
// `audio_source_asset` (the localized voiceover) while LanguageJob had no such
// field, so a manifest that VALIDATED against the published schema was accepted
// and its voiceover was dropped — the language job rendered with the base audio
// and nothing reported the loss.
func TestMultilingualManifestSchemaMatchesGoStructs(t *testing.T) {
	schema := contractschema.Load(t, batchContractSchema)

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
			want := contractschema.PropertyNames(t, schema, tc.pointer)
			got := contractschema.JSONFieldNames(tc.goValue)
			if missing := contractschema.Difference(want, got); len(missing) > 0 {
				t.Errorf("%s: schema declares %v, but the Go struct does not decode them (a schema-valid manifest would lose them silently)", tc.name, missing)
			}
			if extra := contractschema.Difference(got, want); len(extra) > 0 {
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
