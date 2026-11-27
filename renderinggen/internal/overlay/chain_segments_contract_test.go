package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// canonicalChainSegmentKeys is the consumer-side statement of the
// chain_segments wire contract. The Rust producer
// (`editorial_plan_v2::planner::coalesce::segment_contract_tests::CANONICAL_KEYS`)
// and PipelineGen (`capmodel.ChainSegment`) pin the same list in their own
// repositories, so a key added on one side fails in that side's own test
// instead of passing because a sibling checkout happened to be absent.
var canonicalChainSegmentKeys = map[string]bool{
	"text": true, "group_id": true, "subgroup_id": true, "motion_id": true,
	"preset_id": true, "image_preset_id": true, "template_id": true, "kind": true,
	"entity_id": true, "entity_caption": true, "caption_motion_id": true,
	"caption_motion_params": true, "caption_font_family": true, "caption_layout": true,
	"caption_color": true, "entity_style_id": true,
	"start_us": true, "duration_us": true, "motion_params": true, "asset_id": true,
	"assets": true, "image_layers": true, "frame": true, "params": true,
	"style": true, "map": true,
}

// TestChainSegmentMirrorMatchesCanonicalContract pins the consumer mirror to
// the documented key set. This is the gate that does not need the PipelineGen
// checkout: the mirror either models exactly the contract or this fails.
func TestChainSegmentMirrorMatchesCanonicalContract(t *testing.T) {
	typ := reflect.TypeOf(chainSegment{})
	if got, want := typ.NumField(), len(canonicalChainSegmentKeys); got != want {
		t.Fatalf("chainSegment has %d fields, canonical contract has %d keys", got, want)
	}
	seen := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			t.Fatalf("field %s carries no json tag", field.Name)
		}
		if !canonicalChainSegmentKeys[tag] {
			t.Fatalf("field %s tag %q is not in the canonical chain_segments contract", field.Name, tag)
		}
		seen[tag] = true
	}
	for key := range canonicalChainSegmentKeys {
		if !seen[key] {
			t.Fatalf("chainSegment does not model contract key %q", key)
		}
	}
}

// TestConformanceFixtureSegmentsAreContracted keeps the shared fixture honest:
// every key the consumer receives must be one it models, otherwise the extra
// key is silently dropped and the lowering diverges from the producer.
func TestConformanceFixtureSegmentsAreContracted(t *testing.T) {
	path := filepath.Join("..", "..", "..", "contracts", "chain-segments-v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared conformance fixture: %v", err)
	}
	var document struct {
		Plan struct {
			Items []struct {
				Params map[string]json.RawMessage `json:"params"`
			} `json:"items"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode conformance document: %v", err)
	}
	if len(document.Plan.Items) != 1 {
		t.Fatalf("conformance plan items = %d, want 1", len(document.Plan.Items))
	}
	var segments []map[string]json.RawMessage
	if err := json.Unmarshal(document.Plan.Items[0].Params["chain_segments"], &segments); err != nil {
		t.Fatalf("decode conformance chain_segments: %v", err)
	}
	if len(segments) != 5 {
		t.Fatalf("conformance chain segments = %d, want 5", len(segments))
	}
	for index, segment := range segments {
		for key := range segment {
			if !canonicalChainSegmentKeys[key] {
				t.Fatalf("fixture segment %d carries key %q that chainSegment does not model", index, key)
			}
		}
	}
}

// TestChainSegmentsAreDecodedOnceIntoTheItem pins the typed contract: the
// resolve stage stores the decoded segment list on the item, so the lowering
// reads a typed value instead of re-decoding params["chain_segments"].
func TestChainSegmentsAreDecodedOnceIntoTheItem(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"typed-chain","video_id":"typed-chain",
		"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":6000,
		"items":[{
			"id":"typed-chain-1","kind":"image_chain","template_id":"IMAGE_CHAIN",
			"start_ms":0,"end_ms":6000,
			"asset_refs":[
				{"asset_id":"a","sha256":"` + strings.Repeat("a", 64) + `","url":"https://example.test/a.jpg","media_type":"image/jpeg"},
				{"asset_id":"b","sha256":"` + strings.Repeat("b", 64) + `","url":"https://example.test/b.jpg","media_type":"image/jpeg"}
			],
			"params":{"chain_segments":[
				{"kind":"entity_image","template_id":"image_popup","preset_id":"image_scale_in","image_preset_id":"image_scale_in","asset_id":"a","assets":[{"asset_id":"a","sha256":"` + strings.Repeat("a", 64) + `","url":"https://example.test/a.jpg","media_type":"image/jpeg"}],"start_us":0,"duration_us":3000000,"params":{"width":520,"height":520}},
				{"kind":"entity_image","template_id":"image_popup","preset_id":"image_scale_in","image_preset_id":"image_scale_in","asset_id":"b","assets":[{"asset_id":"b","sha256":"` + strings.Repeat("b", 64) + `","url":"https://example.test/b.jpg","media_type":"image/jpeg"}],"start_us":3000000,"duration_us":3000000,"params":{"width":520,"height":520}}
			]}
		}]
	}`)
	src, err := decodeSemanticPlan(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	validated, err := validateSemanticPlan(&src)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	resolved, err := resolvePlan(validated, CompileOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved.Items) != 1 {
		t.Fatalf("resolved %d items, want 1", len(resolved.Items))
	}
	item := resolved.Items[0].Item
	if len(item.chainSegments) != 2 {
		t.Fatalf("typed chainSegments = %d, want the two producer segments", len(item.chainSegments))
	}
	if item.chainSegments[1].AssetID != "b" || item.chainSegments[1].StartUS != 3_000_000 {
		t.Fatalf("typed segment 1 = %+v, want asset b at 3s", item.chainSegments[1])
	}
}

// TestChainSegmentsForPrefersTheTypedList proves the lowering does not touch
// the params blob once the resolve stage has decoded it: even a params value
// that could never decode as a segment list is ignored.
func TestChainSegmentsForPrefersTheTypedList(t *testing.T) {
	item := semanticItem{
		ID: "typed",
		chainSegments: []chainSegment{
			{Kind: "entity_image", TemplateID: "image_popup", AssetID: "a", StartUS: 0, DurationUS: 1_000_000},
			{Kind: "entity_image", TemplateID: "image_popup", AssetID: "b", StartUS: 1_000_000, DurationUS: 1_000_000},
		},
	}
	segments, err := chainSegmentsFor(item, map[string]any{"chain_segments": "not-segments"})
	if err != nil {
		t.Fatalf("chainSegmentsFor decoded the blob instead of using the typed list: %v", err)
	}
	if len(segments) != 2 || segments[1].AssetID != "b" {
		t.Fatalf("segments = %+v, want the typed list", segments)
	}
}
