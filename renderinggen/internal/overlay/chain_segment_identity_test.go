package overlay

import (
	"strings"
	"testing"
)

// TestSegmentIdentityRequiresProducerIdentity pins the producer contract: a
// chain segment must declare both `kind` and `template_id`. The content-based
// inference that used to fill them in was retired on 2026-11-21
// (CHAIN-SEGMENT-SSOT Open 4); every segment compiled from a real plan carries
// both keys, so guessing a family is now a silent producer bug.
func TestSegmentIdentityRequiresProducerIdentity(t *testing.T) {
	explicit := chainSegment{Kind: "image", TemplateID: "IMAGE_OVERLAY", Assets: []SemanticAssetRef{{ID: "a"}}}
	kind, template, err := segmentIdentity(explicit, "chain-1", 0)
	if err != nil {
		t.Fatalf("segmentIdentity explicit: %v", err)
	}
	if kind != "image" || template != "IMAGE_OVERLAY" {
		t.Fatalf("identity = (%q, %q), want the declared identity", kind, template)
	}

	cases := []struct {
		name string
		seg  chainSegment
	}{
		{name: "missing kind", seg: chainSegment{TemplateID: "IMPORTANT_PHRASE", Text: "una frase"}},
		{name: "missing template", seg: chainSegment{Kind: string(KindTextPhraseSynonym), Text: "una frase"}},
		{name: "the old image inference is gone", seg: chainSegment{Assets: []SemanticAssetRef{{ID: "img-1"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := segmentIdentity(tc.seg, "chain-1", 0); err == nil || !strings.Contains(err.Error(), "must declare kind and template_id") {
				t.Fatalf("error = %v, want the producer-contract failure", err)
			}
		})
	}
}

// TestSegmentIdentityFailsClosed pins the one check that survives the shim: a
// segment that declares its identity but has nothing to draw still fails.
func TestSegmentIdentityFailsClosed(t *testing.T) {
	empty := chainSegment{Kind: string(KindMap), TemplateID: "MAP"}
	if _, _, err := segmentIdentity(empty, "chain-1", 1); err == nil || !strings.Contains(err.Error(), "carries no content") {
		t.Fatalf("empty segment error = %v, want an explicit empty-segment failure", err)
	}
}

// TestCompileChainRejectsContentlessSegment proves the compile path fails closed
// instead of lowering an empty layer that silently renders nothing.
func TestCompileChainRejectsContentlessSegment(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-empty","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_01_blur_soft_reveal","text":"FIRST",
		"start_ms":0,"end_ms":4000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_01_blur_soft_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2000000},
			{"kind":"text_phrase","template_id":"IMPORTANT_PHRASE","start_us":2000000,"duration_us":2000000}
		]}
	}]}`)
	_, err := CompileSemantic(raw)
	if err == nil {
		t.Fatal("a segment with no content must fail the compile")
	}
	if !strings.Contains(err.Error(), "carries no content") {
		t.Fatalf("error = %v, want the empty-segment message", err)
	}
}

// TestCompileChainRejectsMissingKind pins the retired shim end to end: a
// segment without `kind`/`template_id` no longer lowers through an inferred
// family, it fails the compile with the producer-contract error.
func TestCompileChainRejectsMissingKind(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-legacy","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_01_blur_soft_reveal","text":"FIRST",
		"start_ms":0,"end_ms":4000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_01_blur_soft_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2000000},
			{"text":"SECOND","motion_id":"phrase_apple_clean_01_blur_soft_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","start_us":2000000,"duration_us":2000000}
		]}
	}]}`)
	_, err := CompileSemantic(raw)
	if err == nil {
		t.Fatal("a segment without kind/template_id must fail the compile")
	}
	if !strings.Contains(err.Error(), "must declare kind and template_id") {
		t.Fatalf("error = %v, want the producer-contract failure", err)
	}
}
