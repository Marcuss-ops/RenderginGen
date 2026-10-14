package overlay

import (
	"strings"
	"testing"
)

// SSOT: the canonical params.font_family reaches entity captions.
func TestTypographySSOTEntityCaptionFont(t *testing.T) {
	// Explicit payload choice reaches the caption layer.
	raw := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"ssot-font","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"ent","kind":"entity_card","entity_id":"person:ada","template_id":"PERSON","preset_id":"phrase_default","image_preset_id":"image_fade_in","entity_style_id":"random",` +
		`"text":"Ada Lovelace","entity_caption":"Ada Lovelace","params":{"font_family":"inter","glow_size":10,"stroke_size":4},"start_ms":0,"end_ms":2000,"duration_ms":2000,` +
		`"asset_refs":[{"asset_id":"img","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/i.png","media_type":"image/png"}]}]}`
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var caption *Layer
	for i := range result.Plan.Layers {
		if strings.HasSuffix(result.Plan.Layers[i].ID, ":caption") {
			caption = &result.Plan.Layers[i]
		}
	}
	if caption == nil || caption.Style == nil {
		t.Fatalf("no caption layer compiled: %+v", result.Plan.Layers)
	}
	if caption.Style.Font != "assets/fonts/Inter.ttf" {
		t.Errorf("caption font = %q, want payload Inter", caption.Style.Font)
	}
	if caption.Style.Glow == nil || caption.Style.Glow.Radius != 10 {
		t.Errorf("caption glow = %+v, want payload radius 10", caption.Style.Glow)
	}
	if caption.Style.Stroke == nil || caption.Style.Stroke.Width != 4 {
		t.Errorf("caption stroke = %+v, want payload width 4", caption.Style.Stroke)
	}
}

// SSOT: an explicit caption_color survives entity style resolution.
func TestTypographySSOTCaptionColorRespectsPayload(t *testing.T) {
	raw := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"ssot-color","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"ent","kind":"entity_card","entity_id":"person:ada","template_id":"PERSON","preset_id":"phrase_default","image_preset_id":"image_fade_in","entity_style_id":"random",` +
		`"text":"Ada Lovelace","entity_caption":"Ada Lovelace","caption_color":"#AABBCC","start_ms":0,"end_ms":2000,"duration_ms":2000,` +
		`"asset_refs":[{"asset_id":"img","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/i.png","media_type":"image/png"}]}]}`
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range result.Plan.Layers {
		if strings.HasSuffix(l.ID, ":caption") && l.Style != nil {
			if l.Style.Fill != "#AABBCC" {
				t.Fatalf("caption fill = %q, want payload #AABBCC", l.Style.Fill)
			}
			return
		}
	}
	t.Fatal("no caption layer found")
}

// SSOT: phrase motions never force Bricolage; payload/preset default wins.
func TestTypographySSOTPhraseKeepsPayloadFont(t *testing.T) {
	raw := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"ssot-tw","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"apple_focus_rise",` +
		`"text":"Hello world","start_ms":0,"end_ms":3000}]}`
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	layer := result.Plan.Layers[0]
	if layer.Style == nil {
		t.Fatal("no style")
	}
	if strings.Contains(layer.Style.Font, "Bricolage") {
		t.Fatalf("phrase motion forced Bricolage without payload choice: %q", layer.Style.Font)
	}
}
