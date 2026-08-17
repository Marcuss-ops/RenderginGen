package overlay

import (
	"encoding/json"
	"testing"
)

func TestCompileIfSemanticCompilesPipelinePlan(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"plan-1","video_id":"video-1",
      "width":1280,"height":720,"fps":30,
      "items":[
        {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","text":"This changes it","start_ms":100,"end_ms":900},
        {"id":"image-1","template_id":"IMAGE_OVERLAY","start_ms":900,"end_ms":1900,
         "asset_refs":[{"asset_id":"img-1","url":"https://drive/image.png","sha256":"abc"}]}
      ]
    }`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The text layer forces the canonical font into the queue assets, so the
	// compiled job carries image + font (content-addressed).
	if !semantic || len(assets) != 2 || assets[0].LogicalPath != "assets/image.png" {
		t.Fatalf("semantic=%v assets=%+v", semantic, assets)
	}
	if assets[1].Hash != CanonicalFontHash || assets[1].LogicalPath != "assets/fonts/DejaVuSans.ttf" {
		t.Fatalf("font asset = %+v", assets[1])
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Schema != "chronon.render-plan" || plan.Canvas.DurationFrames != 57 {
		t.Fatalf("compiled plan = %+v", plan)
	}
	if len(plan.Layers) != 2 || plan.Layers[1].Asset != "assets/image.png" {
		t.Fatalf("compiled layers = %+v", plan.Layers)
	}
	// The text layer carries the canonical font and no auto-fit (short text).
	if plan.Layers[0].Type != "text" || plan.Layers[0].Font != "assets/fonts/DejaVuSans.ttf" || plan.Layers[0].FontSize != 0 {
		t.Fatalf("text layer = %+v", plan.Layers[0])
	}
}

// TestCompileIfSemanticFullVocabulary locks the complete canonical semantic
// vocabulary (9 entities + video background): every template compiles to its
// concrete primitive, image slots are resolved, and Params (box/animation/
// priority/font_size) are honored.
func TestCompileIfSemanticFullVocabulary(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"plan-vocab","video_id":"video-1",
      "width":1280,"height":720,"fps":30,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","text":"UN TITOLO MOLTO LUNGO CHE SUPERA AMPIAMENTE IL BUDGET","start_ms":0,"end_ms":1000,"params":{"animation":{"preset":"fade_in"}}},
        {"id":"word","template_id":"IMPORTANT_WORD","text":"VELOCE","start_ms":1000,"end_ms":2000},
        {"id":"number","template_id":"NUMBER","text":"42%","start_ms":2000,"end_ms":3000},
        {"id":"quote","template_id":"QUOTE","text":"\u201cIl futuro è adesso\u201d","start_ms":3000,"end_ms":4000},
        {"id":"person","template_id":"PERSON","text":"Ada Lovelace","start_ms":4000,"end_ms":5000},
        {"id":"org","template_id":"ORGANIZATION","text":"ACME","start_ms":5000,"end_ms":6000},
        {"id":"location","template_id":"LOCATION","text":"Milano","start_ms":6000,"end_ms":7000},
        {"id":"img1","template_id":"IMAGE_OVERLAY","start_ms":0,"end_ms":3000,
         "params":{"position":"right","priority":0.9,"box_width":260,"box_height":260},
         "asset_refs":[{"asset_id":"a","url":"https://drive/a.png","sha256":"hash-a"}]},
        {"id":"img2","template_id":"IMAGE_OVERLAY","start_ms":500,"end_ms":3500,
         "params":{"position":"right","priority":0.5,"box_width":260,"box_height":260},
         "asset_refs":[{"asset_id":"b","url":"https://drive/b.png","sha256":"hash-b"}]},
        {"id":"product","template_id":"PRODUCT","start_ms":3000,"end_ms":5000,
         "params":{"position":[100,200]},
         "asset_refs":[{"asset_id":"p","url":"https://drive/p.png","sha256":"hash-p"}]},
        {"id":"logo","template_id":"LOGO","start_ms":0,"end_ms":8000,
         "params":{"position":"corner"},
         "asset_refs":[{"asset_id":"l","url":"https://drive/l.png","sha256":"hash-l"}]},
        {"id":"video_bg","template_id":"VIDEO_BACKGROUND","start_ms":0,"end_ms":8000,
         "asset_refs":[{"asset_id":"bg","url":"https://drive/bg.mp4","sha256":"hash-bg"}]}
      ]
    }`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !semantic {
		t.Fatal("expected semantic compile")
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 13 {
		t.Fatalf("layers = %d, want 13 (including contrast veil)", len(plan.Layers))
	}
	byID := map[string]Layer{}
	for _, layer := range plan.Layers {
		byID[layer.ID] = layer
	}
	// Template → concrete primitive mapping.
	checks := []struct {
		id, typ, preset string
	}{
		{"phrase", "text", "caption_safe_area"},
		{"word", "text", "kinetic_word"},
		{"number", "text", "kinetic_word"},
		{"quote", "text", "caption_safe_area"},
		{"person", "text", "title_centered"},
		{"org", "text", "title_centered"},
		{"location", "text", "title_centered"},
		{"product", "image", ""},
		{"logo", "image", ""},
	}
	for _, c := range checks {
		layer, ok := byID[c.id]
		if !ok {
			t.Fatalf("missing layer %s", c.id)
		}
		if layer.Type != c.typ || (c.preset != "" && layer.Preset != c.preset) {
			t.Fatalf("%s: type=%s preset=%s want %s/%s", c.id, layer.Type, layer.Preset, c.typ, c.preset)
		}
	}
	// Video background uses `source` and spans the full canvas.
	bg := byID["video_bg"]
	if bg.Type != "video" || bg.Source != "assets/bg.mp4" || bg.BoxWidth != 1280 || bg.BoxHeight != 720 || bg.DurationFrames != 240 {
		t.Fatalf("video bg = %+v", bg)
	}
	// Auto-fit: the long phrase carries a font_size override.
	if byID["phrase"].FontSize == 0 {
		t.Fatalf("long phrase must carry font_size, got %+v", byID["phrase"])
	}
	// Animation param → layer animation block.
	if byID["phrase"].Animation == nil || byID["phrase"].Animation.Preset != "fade_in" {
		t.Fatalf("phrase animation = %+v", byID["phrase"].Animation)
	}
	// Collision avoidance: img1 (priority 0.9) keeps the right slot, img2
	// (0.5) must move to a different position.
	img1, img2 := byID["img1"], byID["img2"]
	if img1.Position == nil || img2.Position == nil {
		t.Fatalf("image positions missing: img1=%v img2=%v", img1.Position, img2.Position)
	}
	if img1.Position[0] == img2.Position[0] && img1.Position[1] == img2.Position[1] {
		t.Fatalf("collision not resolved: img1=%v img2=%v", img1.Position, img2.Position)
	}
	// Explicit numeric position wins for product.
	if byID["product"].Position[0] != 100 || byID["product"].Position[1] != 200 {
		t.Fatalf("product position = %v", byID["product"].Position)
	}
	// The font asset is projected exactly once.
	fontCount := 0
	for _, asset := range assets {
		if asset.Hash == CanonicalFontHash {
			fontCount++
		}
	}
	if fontCount != 1 {
		t.Fatalf("font assets = %d, want 1", fontCount)
	}
}

func TestCompileIfSemanticLeavesChrononPlanUnchanged(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1}`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil || semantic || len(assets) != 0 || string(compiled) != string(raw) {
		t.Fatalf("compiled=%s assets=%v semantic=%v err=%v", compiled, assets, semantic, err)
	}
}

// TestCompileIfSemanticRejectsInvalidPlans verifies the validator rejects
// broken specs before rendering instead of silently correcting them:
// negative start_ms, zero duration, an unknown template (type), and an image
// with an empty source (or an asset without a content hash).
func TestCompileIfSemanticRejectsInvalidPlans(t *testing.T) {
	cases := []struct {
		name string
		plan string
	}{
		{
			name: "negative start_ms",
			plan: `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps":1,"items":[{"id":"x","template_id":"IMPORTANT_PHRASE","text":"t","start_ms":-1,"end_ms":1000}]}`,
		},
		{
			name: "zero duration",
			plan: `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps":1,"items":[{"id":"x","template_id":"IMPORTANT_PHRASE","text":"t","start_ms":500,"end_ms":500}]}`,
		},
		{
			name: "unknown template type",
			plan: `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps":1,"items":[{"id":"x","template_id":"DOES_NOT_EXIST","start_ms":0,"end_ms":1000}]}`,
		},
		{
			name: "image without source",
			plan: `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps":1,"items":[{"id":"x","template_id":"IMAGE_OVERLAY","start_ms":0,"end_ms":1000}]}`,
		},
		{
			name: "asset without sha256",
			plan: `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps":1,"items":[{"id":"x","template_id":"IMAGE_OVERLAY","start_ms":0,"end_ms":1000,"asset_refs":[{"asset_id":"img","url":"https://drive/i.png"}]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := CompileIfSemantic([]byte(tc.plan)); err == nil {
				t.Fatalf("expected rejection for %s, got nil error", tc.name)
			}
		})
	}
}
