package overlay

import "testing"

// The producer (PipelineGen OverlayItem) declares start_us/duration_us with
// omitempty: a zero StartUS is dropped from the wire and a zero DurationUS
// marks the legacy millisecond path. The decoder must accept both shapes —
// rejecting them failed live job v17 on its timeline-start item (start_us=0
// omitted, duration_us present).
func TestSemanticItemDecodesOmittedZeroStartUS(t *testing.T) {
	// Wire shape of a timeline-start item: no start_us, duration_us present.
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"timing-omitempty","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{` +
		`"id":"image-slide-right-reveal__chiara-poggi__entity_image__01","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in",` +
		`"motion_id":"image_slide_right_reveal","text":"Chiara Poggi","start_ms":0,"end_ms":3000,"duration_us":3000000,` +
		`"asset_refs":[{"asset_id":"img-1","url":"https://example/img.jpg","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","media_type":"image/jpeg"}]` +
		`}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("timeline-start item without start_us must decode: %v", err)
	}
	if len(result.Plan.Layers) == 0 {
		t.Fatal("timeline-start item produced no layers")
	}
}

func TestSemanticItemDecodesLegacyMillisecondPath(t *testing.T) {
	// Wire shape of a legacy-ms item: neither start_us nor duration_us.
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"timing-legacy","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{` +
		`"id":"phrase-legacy","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default",` +
		`"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"30 SEP 2026","start_ms":1000,"end_ms":4000` +
		`}]}`)
	if _, err := CompileSemantic(raw); err != nil {
		t.Fatalf("legacy millisecond item must decode: %v", err)
	}
}
