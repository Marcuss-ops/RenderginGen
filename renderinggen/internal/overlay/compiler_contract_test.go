package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompileIfSemanticRejectsUntypedConcretePlan pins the fail-closed
// execution-worker boundary: a document without schema_version is rejected.
// The historical byte-for-byte pass-through let concrete plans bypass the
// compiler entirely — that bypass is the bug this test guards against.
func TestCompileIfSemanticRejectsUntypedConcretePlan(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"bg","type":"image","asset":"assets/background.jpg","start_frame":0,"duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err == nil {
		t.Fatalf("untyped concrete plan must be rejected, got compiled=%+v semantic=%v", compiled, semantic)
	}
	if len(assets) != 0 {
		t.Fatalf("rejected plan must synthesize no assets, got %+v", assets)
	}
	if !strings.Contains(err.Error(), "only accepted contract") {
		t.Fatalf("error must name the accepted contract, got: %v", err)
	}
}

func TestCompileIfSemanticOptionalBackground(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "background":{"kind":"color","color":[0,0,0,1]},
      "items":[{"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"clean_slide_up","text":"hi","start_ms":0,"end_ms":1000}]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("compile semantic background: semantic=%v err=%v", semantic, err)
	}
	if len(compiled.Layers) < 2 || compiled.Layers[0].ID != "background" || compiled.Layers[0].Type != "color" {
		t.Fatalf("background was not emitted first: %+v", compiled.Layers)
	}
	if compiled.Layers[0].DurationFrames != compiled.Canvas.DurationFrames {
		t.Fatalf("background duration=%d, canvas duration=%d", compiled.Layers[0].DurationFrames, compiled.Canvas.DurationFrames)
	}
}

func TestCompileIfSemanticTextMotionProducesAnimatorContract(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
      "items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_fade_in","motion_id":"character_cascade",
        "text":"Powerfully simple.","start_ms":0,"end_ms":2000}]
    }`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("compile text motion: %v", err)
	}
	if len(compiled.Layers) != 1 || len(compiled.Layers[0].TextAnimators) != 1 {
		t.Fatalf("expected one text animator, got %+v", compiled.Layers)
	}
	a := compiled.Layers[0].TextAnimators[0]
	if a.Selectors[0].Unit != "glyph" || len(a.Properties) != 2 {
		t.Fatalf("unexpected character cascade contract: %+v", a)
	}
}

func TestCompileSemanticTextUsesExplicitCanvasLocalBox(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"placement-fixture","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
      "items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_focus_v1",
        "motion_id":"character_cascade","text":"ABC","start_ms":0,"end_ms":5000}]
    }`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Layers) != 1 {
		t.Fatalf("expected one text layer, got %d", len(compiled.Layers))
	}
	layer := compiled.Layers[0]
	if len(layer.Size) != 2 || layer.Size[0] != 1920 || layer.Size[1] != 120 {
		t.Fatalf("text local box = %#v, want [1920 120]", layer.Size)
	}
	// Chronon places the text-frame centre in canvas coordinates: a
	// full-canvas centred safe-area text frame is centred at the canvas
	// centre (960, 540) — not an offset from it (see resolveTextLayout).
	if len(layer.Position) != 2 || layer.Position[0] != 960 || layer.Position[1] != 540 {
		t.Fatalf("centered text position = %#v, want [960 540]", layer.Position)
	}
	if len(layer.TextAnimators) != 1 || layer.TextAnimators[0].Selectors[0].Unit != "glyph" {
		t.Fatalf("ABC selector fixture was not transported: %#v", layer.TextAnimators)
	}
}

func TestCompileSemanticTextMotionsDoNotCollapseToSameContract(t *testing.T) {
	motions := []string{"word_reveal", "character_cascade", "opacity_wave", "scale_wave", "char_wave"}
	contracts := make(map[string]string, len(motions))
	for _, motionID := range motions {
		raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"` + motionID + `","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_focus_v1","motion_id":"` + motionID + `","text":"ABC","start_ms":0,"end_ms":5000}]}`)
		compiled, _, _, err := CompileIfSemantic(raw)
		if err != nil {
			t.Fatalf("%s: %v", motionID, err)
		}
		if len(compiled.Layers) != 1 || len(compiled.Layers[0].TextAnimators) != 1 {
			t.Fatalf("%s: missing text animator", motionID)
		}
		data, err := json.Marshal(compiled.Layers[0].TextAnimators[0])
		if err != nil {
			t.Fatal(err)
		}
		contract := string(data)
		if previous, exists := contracts[contract]; exists {
			t.Fatalf("%s collapsed to the same text animator contract as %s", motionID, previous)
		}
		contracts[contract] = motionID
	}
}

// TestCompileIfSemanticRejectsMissingPresetID pins ADR-029 forward-point (d):
// RenderingGen no longer re-maps a template_id to a preset (it must not know
// that IMPORTANT_PHRASE means caption_card). A preset-driven template without
// a preset_id is rejected — the semantic_role → preset decision lives only in
// PipelineGen's SemanticOverlayResolver.
func TestCompileIfSemanticRejectsMissingPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","text":"hi","start_ms":0,"end_ms":1000},
        {"id":"word","template_id":"IMPORTANT_WORD","text":"APPLE","start_ms":1000,"end_ms":2000}
      ]
    }`)
	if _, _, _, err := CompileIfSemantic(raw); err == nil {
		t.Fatal("preset-driven template without preset_id must be rejected (no template→preset mirror)")
	}
}

// TestCompileIfSemanticPresetLessPrimitiveCompilesBare pins that preset-less
// primitives (PRODUCT / LOGO) do NOT require a preset_id: they compile to a
// bare layer whose appearance is the renderer's default. This is the other
// half of ADR-029 (d) — RenderingGen only enforces the preset for preset-driven
// templates, never invents one for a primitive.
func TestCompileIfSemanticPresetLessPrimitiveCompilesBare(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"product","template_id":"PRODUCT","start_ms":0,"end_ms":1000,
         "asset_refs":[{"asset_id":"prod","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/objects/prod.png","media_type":"image/png"}]}
      ]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("preset-less primitive must compile: semantic=%v err=%v", semantic, err)
	}
	if len(compiled.Layers) != 1 {
		t.Fatalf("preset-less primitive must compile to a bare layer: %+v", compiled.Layers)
	}
}

// TestCompileIfSemanticRejectsUnknownSchema pins the fail-closed path for a
// plan that is neither concrete nor the known semantic contract.
func TestCompileIfSemanticRejectsUnknownSchema(t *testing.T) {
	raw := []byte(`{"schema_version":"something.else.v9","plan_id":"p"}`)
	_, _, _, err := CompileIfSemantic(raw)
	if err == nil {
		t.Fatal("unknown plan schema must be rejected")
	}
}

// TestCompileIfSemanticMalformedJSON pins graceful decoding failure.
func TestCompileIfSemanticMalformedJSON(t *testing.T) {
	if _, _, _, err := CompileIfSemantic([]byte(`{not-json`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

// TestCompileIfSemanticKindAndExplicitText pins the kind-SSOT contract: the
// plan's preset_id slot is compiled through the SAME single compileSemantic
// path (no new renderer), and the display text comes ONLY from the item's
// explicit `text` — RenderingGen has no entity_ref fallback.
func TestCompileIfSemanticKindAndExplicitText(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"QUESTO CAMBIA TUTTO","start_ms":0,"end_ms":1000},
        {"id":"person","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","text":"Cook","start_ms":1000,"end_ms":2000}
      ]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("semantic plan must compile: semantic=%v err=%v", semantic, err)
	}
	if len(compiled.Layers) != 2 {
		t.Fatalf("compiled layers = %+v", compiled.Layers)
	}
	// The item's explicit text is used verbatim; RenderingGen never invents a
	// display text.
	if compiled.Layers[1].Text != "Cook" {
		t.Fatalf("entity text = %q, want Cook", compiled.Layers[1].Text)
	}
	if compiled.Layers[1].Animation == nil || len(compiled.Layers[1].Animation.Tracks) == 0 {
		t.Fatalf("expected generic animation tracks, got %+v", compiled.Layers[1].Animation)
	}
}

// TestCompileIfSemanticTextIsMandatory pins that a text kind without explicit
// text is rejected: RenderingGen never reconstructs a display name from
// entity_ref or any other fallback — PipelineGen owns the displayed text.
func TestCompileIfSemanticTextIsMandatory(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"person","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","start_ms":0,"end_ms":1000}
      ]
    }`)
	if _, _, _, err := CompileIfSemantic(raw); err == nil {
		t.Fatal("text kind without text must be rejected (no entity_ref fallback)")
	}
}

// TestCompileIfSemanticImportantPhraseAndNamedImage pins the two overlay
// classes used by the first real Chronon canary together. IMPORTANT_PHRASE is
// a readable emphasis card; PERSON + lower_third_safe is an image/name
// composition where the name is the item's explicit text and the image remains
// a content-addressed asset.
func TestCompileIfSemanticImportantPhraseAndNamedImage(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"phrase-and-name","video_id":"phrase-and-name",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase-important","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card",
         "text":"THIS CHANGES EVERYTHING","start_ms":500,"end_ms":1800},
        {"id":"person-image-name","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","image_preset_id":"image_slide_left",
         "text":"Matt Damon",
         "start_ms":2200,"end_ms":4200,
         "asset_refs":[{"asset_id":"matt-damon","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                         "url":"https://store.example/objects/matt-damon.png","media_type":"image/png"}]}
      ]
    }`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("semantic phrase/name plan: semantic=%v err=%v", semantic, err)
	}
	if len(compiled.Layers) != 3 {
		t.Fatalf("compiled layers = %+v", compiled.Layers)
	}
	if compiled.Layers[0].Text != "THIS CHANGES EVERYTHING" {
		t.Fatalf("important phrase layer = %+v", compiled.Layers[0])
	}
	if compiled.Layers[1].Type != "image" || compiled.Layers[1].Asset != "assets/semantic/matt-damon.png" {
		t.Fatalf("named image asset layer = %+v", compiled.Layers[1])
	}
	if compiled.Layers[1].Animation == nil || len(compiled.Layers[1].Animation.Tracks) == 0 {
		t.Fatalf("entity image must carry image preset animation = %+v", compiled.Layers[1].Animation)
	}
	// Chronon places image frames by centre offset from the canvas centre
	// (see resolveImageLayout): the 260px image_left card is flush with the
	// left edge, so its centre sits at -(canvasW-boxW)/2 = -510 on a 1280
	// canvas. A zero offset would mean the image is centred.
	if len(compiled.Layers[1].Position) != 2 || compiled.Layers[1].Position[0] != -510 {
		t.Fatalf("entity image must use image preset layout = %+v", compiled.Layers[1].Position)
	}
	if compiled.Layers[2].Text != "Matt Damon" {
		t.Fatalf("named image label layer = %+v", compiled.Layers[2])
	}
	if len(assets) != 1 || assets[0].LogicalPath != "assets/semantic/matt-damon.png" {
		t.Fatalf("materialized assets = %+v", assets)
	}
}

// TestCompileIfSemanticTransportsChrononPresetID ensures RenderingGen does
// not mirror Chronon's preset registry. Chronon remains responsible for the
// authoritative lookup during plan compilation.
func TestCompileIfSemanticTransportsChrononPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_focus_v1","text":"x","start_ms":0,"end_ms":1000}
      ]
    }`)
	if _, _, _, err := CompileIfSemantic(raw); err != nil {
		t.Fatalf("non-empty Chronon preset id must be transported: %v", err)
	}
}
