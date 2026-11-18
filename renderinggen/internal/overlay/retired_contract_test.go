package overlay

import (
	"strings"
	"testing"
)

func TestRetiredAnimationPoliciesFailWithMigrationError(t *testing.T) {
	for _, policies := range []string{`[]`, `[{"target":"image","group_id":"g"}]`} {
		raw := []byte(`{
			"schema_version":"renderinggen.overlay-plan.v1",
			"plan_id":"retired-policy","video_id":"retired-policy",
			"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":2000,
			"animation_policies":` + policies + `,
			"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","text":"Retired","start_ms":0,"end_ms":2000}]
		}`)
		_, err := CompileSemantic(raw)
		if err == nil || !strings.Contains(err.Error(), "animation_policies is retired; migrate to animation groups or explicit motion_id") {
			t.Fatalf("animation_policies=%s error = %v, want dedicated retirement error", policies, err)
		}
	}
}

func TestPlanWithoutRetiredAnimationPoliciesStillCompiles(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"no-policy","video_id":"no-policy",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":2000,
		"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"Modern","start_ms":0,"end_ms":2000}]
	}`)
	if _, err := CompileSemantic(raw); err != nil {
		t.Fatalf("modern plan without animation_policies must compile: %v", err)
	}
}
