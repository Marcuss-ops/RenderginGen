package overlay

import (
	"strings"
	"testing"
)

func TestSimpleTextElementDoesNotRequireTemplate(t *testing.T) {
	result, err := CompileSemantic([]byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"simple-text","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
		"items":[{"id":"title","text":"HELLO WORLD","style_id":"clean_white","motion_id":"fade_in","start_ms":0,"end_ms":1000}]
	}`))
	if err != nil {
		t.Fatalf("simple text without template rejected: %v", err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Text != "HELLO WORLD" {
		t.Fatalf("simple text was not preserved: %+v", result.Plan.Layers)
	}
	if result.Plan.Layers[0].Type != "text" {
		t.Fatalf("simple text lowered as %q, want text", result.Plan.Layers[0].Type)
	}
}

func TestTextElementKeepsStyleAndMotionIndependent(t *testing.T) {
	item := semanticItem{
		ID: "phrase", Text: "This Changes Everything", StyleID: "glow_red", MotionID: "character_cascade",
		StartMS: 0, EndMS: 2000,
	}
	if err := validateTextElementContract(item); err != nil {
		t.Fatal(err)
	}
	if item.StyleID == item.MotionID {
		t.Fatal("style_id and motion_id must remain separate contract identities")
	}
}

func TestTemplateInstanceSlotsRequireTemplateButRemainOpaque(t *testing.T) {
	_, err := CompileSemantic([]byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"slots","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
		"items":[{"id":"quote","template_id":"QUOTE","template_slots":{"quote":"Success is built one day at a time.","author":"Marcus"},"text":"Success is built one day at a time.","start_ms":0,"end_ms":2000}]
	}`))
	if err == nil {
		// QUOTE is preset-driven, so the missing preset must still be rejected;
		// importantly this proves slots do not become an implicit preset source.
		t.Fatal("template instance without producer-selected preset was accepted")
	}
	if !strings.Contains(err.Error(), "requires preset_id") {
		t.Fatalf("template slots changed the failure mode: %v", err)
	}
}

func TestSlotsWithoutTemplateAreRejected(t *testing.T) {
	item := semanticItem{ID: "bad", Text: "A", TemplateSlots: map[string]any{"quote": "A"}, StartMS: 0, EndMS: 1000}
	if err := validateTextElementContract(item); err == nil || !strings.Contains(err.Error(), "template_slots") {
		t.Fatalf("expected slots-without-template rejection, got %v", err)
	}
}

func TestImageRequiresReusableTemplateInstance(t *testing.T) {
	item := semanticItem{ID: "image", Kind: string(KindEntityImage), Text: "", Assets: []SemanticAssetRef{{ID: "asset"}}, StartMS: 0, EndMS: 1000}
	if err := validateTextElementContract(item); err == nil || !strings.Contains(err.Error(), "without template_id") {
		t.Fatalf("expected image primitive rejection, got %v", err)
	}
}
