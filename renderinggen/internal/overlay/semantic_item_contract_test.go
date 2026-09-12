package overlay

import (
	"strings"
	"testing"
)

func entityContractPlan(item string) []byte {
	return []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"entity-contract",
		"video_id":"entity-contract",
		"width":1280,"height":720,"fps_num":30,"fps_den":1,
		"items":[` + item + `]
	}`)
}

func TestEntityContractRequiresProducerIdentityAndTiming(t *testing.T) {
	_, err := CompileSemantic(entityContractPlan(`{
		"id":"person-1","kind":"entity_card","template_id":"PERSON",
		"preset_id":"name_fade_in","text":"Ada Lovelace",
		"start_ms":1000,"end_ms":3000,"duration_ms":2000
	}`))
	if err == nil || !strings.Contains(err.Error(), "requires entity_id") {
		t.Fatalf("expected missing entity_id rejection, got %v", err)
	}
}

func TestEntityContractRejectsTimingDrift(t *testing.T) {
	_, err := CompileSemantic(entityContractPlan(`{
		"id":"person-1","entity_id":"person:ada","kind":"entity_card","template_id":"PERSON",
		"preset_id":"name_fade_in","text":"Ada Lovelace",
		"start_ms":1000,"end_ms":3000,"duration_ms":1999
	}`))
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected duration mismatch rejection, got %v", err)
	}
}

func TestEntityContractAcceptsCompleteProducerItem(t *testing.T) {
	result, err := CompileSemantic(entityContractPlan(`{
		"id":"person-1","entity_id":"person:ada","kind":"entity_card","template_id":"PERSON",
		"preset_id":"name_fade_in","text":"Ada Lovelace",
		"start_ms":1000,"end_ms":3000,"duration_ms":2000
	}`))
	if err != nil {
		t.Fatalf("complete entity item rejected: %v", err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Text != "Ada Lovelace" {
		t.Fatalf("unexpected entity lowering: %+v", result.Plan.Layers)
	}
}
