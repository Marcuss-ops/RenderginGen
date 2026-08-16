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
        {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","text":"This changes everything","start_ms":100,"end_ms":900},
        {"id":"image-1","template_id":"IMAGE_OVERLAY","start_ms":900,"end_ms":1900,
         "asset_refs":[{"asset_id":"img-1","url":"https://drive/image.png","sha256":"abc"}]}
      ]
    }`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !semantic || len(assets) != 1 || assets[0].LogicalPath != "assets/image.png" {
		t.Fatalf("semantic=%v assets=%+v", semantic, assets)
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
