package overlay

import (
	"strings"
	"testing"
)

// TestSegmentIdentityDeclaredMapping pins the compatibility shim that resolves a
// chain segment's kind/template when the producer omitted them. The mapping is
// content-based, so it is only safe for content that names exactly one family;
// the cases below are the whole allowed surface.
func TestSegmentIdentityDeclaredMapping(t *testing.T) {
	cases := []struct {
		name     string
		seg      chainSegment
		wantKind string
		wantTmpl string
	}{
		{
			name:     "image bytes infer the image family",
			seg:      chainSegment{Assets: []SemanticAssetRef{{ID: "img-1"}}},
			wantKind: string(KindImageSynonym),
			wantTmpl: "IMAGE_OVERLAY",
		},
		{
			name:     "asset id alone infers the image family",
			seg:      chainSegment{AssetID: "img-2"},
			wantKind: string(KindImageSynonym),
			wantTmpl: "IMAGE_OVERLAY",
		},
		{
			name:     "composite layers infer the image family",
			seg:      chainSegment{ImageLayers: []SemanticImageLayer{{}}},
			wantKind: string(KindImageSynonym),
			wantTmpl: "IMAGE_OVERLAY",
		},
		{
			name:     "text alone infers the phrase family",
			seg:      chainSegment{Text: "una frase"},
			wantKind: string(KindTextPhraseSynonym),
			wantTmpl: "IMPORTANT_PHRASE",
		},
		{
			name:     "kind without template derives the template",
			seg:      chainSegment{Kind: string(KindEntityCard), Text: "Ada"},
			wantKind: string(KindEntityCard),
			wantTmpl: "person_default",
		},
		{
			name:     "explicit identity wins",
			seg:      chainSegment{Kind: "image", TemplateID: "IMAGE_OVERLAY", Assets: []SemanticAssetRef{{ID: "a"}}},
			wantKind: "image",
			wantTmpl: "IMAGE_OVERLAY",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, template, err := segmentIdentity(tc.seg, "chain-1", 0)
			if err != nil {
				t.Fatalf("segmentIdentity: %v", err)
			}
			if kind != tc.wantKind || template != tc.wantTmpl {
				t.Fatalf("identity = (%q, %q), want (%q, %q)", kind, template, tc.wantKind, tc.wantTmpl)
			}
		})
	}
}

// TestSegmentIdentityFailsClosed pins the two shapes the shim refuses to guess.
func TestSegmentIdentityFailsClosed(t *testing.T) {
	both := chainSegment{
		Assets: []SemanticAssetRef{{ID: "img-1"}},
		Map:    &SemanticMap{},
	}
	if _, _, err := segmentIdentity(both, "chain-1", 1); err == nil || !strings.Contains(err.Error(), "cannot be inferred") {
		t.Fatalf("map+image segment error = %v, want an explicit ambiguity failure", err)
	}

	if _, _, err := segmentIdentity(chainSegment{}, "chain-1", 0); err == nil || !strings.Contains(err.Error(), "carries no content") {
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
			{"start_us":2000000,"duration_us":2000000}
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

// TestCompileChainInfersMissingKind keeps the compatibility shim honest: a
// legacy segment without `kind` still lowers through its inferred family.
func TestCompileChainInfersMissingKind(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-legacy","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_01_blur_soft_reveal","text":"FIRST",
		"start_ms":0,"end_ms":4000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_01_blur_soft_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2000000},
			{"text":"SECOND","motion_id":"phrase_apple_clean_01_blur_soft_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","start_us":2000000,"duration_us":2000000}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic legacy chain segment: %v", err)
	}
	found := false
	for _, layer := range result.Plan.Layers {
		if strings.Contains(layer.ID, "seg1") && layer.Text == "SECOND" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy segment did not lower through the inferred family: %+v", result.Plan.Layers)
	}
}
