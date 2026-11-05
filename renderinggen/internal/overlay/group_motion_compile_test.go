package overlay

import "testing"

// A new composite carries one group_motion_id and motion-free children. The
// compiler must lower the whole group through the group recipe: if it fell back
// to compileLegacyCompositeImageLayers, the children would have no animation
// and this test would catch the silent regression.
func TestCompositeGroupMotionCompilesWithMotionFreeChildren(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"group-owner","video_id":"group-owner",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":2000,
		"items":[{
			"id":"pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"group_motion_id":"duo_split_reveal",
			"start_ms":0,"end_ms":2000,
			"asset_refs":[
				{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"assets/semantic/a.png","media_type":"image/png"},
				{"asset_id":"b","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"assets/semantic/b.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"a","asset_id":"a","start_ms":0,"end_ms":1000,"preset_id":"image_scale_in","motion_id":""},
				{"id":"b","asset_id":"b","start_ms":1000,"end_ms":2000,"preset_id":"image_focus_in","motion_id":""}
			]
		}]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile group-owned composite: %v", err)
	}
	imageLayers := 0
	for _, layer := range result.Plan.Layers {
		if layer.Type != "image" {
			continue
		}
		imageLayers++
		if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Fatalf("image layer %q has no group choreography: %+v", layer.ID, layer)
		}
	}
	if imageLayers != 2 {
		t.Fatalf("compiled image layers = %d, want 2", imageLayers)
	}
}

// The legacy plan shape (no group_motion_id, per-child motion_id) must remain
// replayable. New producer plans never emit this shape, but old sealed jobs do.
func TestLegacyCompositeStillCompilesPerChildMotion(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"legacy-owner","video_id":"legacy-owner",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":2000,
		"items":[{
			"id":"pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":2000,
			"asset_refs":[
				{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"assets/semantic/a.png","media_type":"image/png"},
				{"asset_id":"b","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"assets/semantic/b.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"a","asset_id":"a","start_ms":0,"end_ms":1000,"preset_id":"image_scale_in","motion_id":"image_fade_reveal"},
				{"id":"b","asset_id":"b","start_ms":1000,"end_ms":2000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal"}
			]
		}]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile legacy per-child composite: %v", err)
	}
	imageLayers := 0
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" {
			imageLayers++
		}
	}
	if imageLayers != 2 {
		t.Fatalf("legacy image layers = %d, want 2", imageLayers)
	}
}
