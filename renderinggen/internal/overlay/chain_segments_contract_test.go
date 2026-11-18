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
	"caption_color": true, "entity_style_id": true, "camera_motion_id": true,
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
