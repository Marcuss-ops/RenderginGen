package overlay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

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

func TestReusableGroupAnimationWorksForDifferentImageCounts(t *testing.T) {
	for _, count := range []int{2, 3, 5} {
		var assets, layers []string
		for index := 0; index < count; index++ {
			id := fmt.Sprintf("image_%d", index)
			assets = append(assets, fmt.Sprintf(`{"asset_id":%q,"sha256":%q,"url":%q,"media_type":"image/png"}`, id, strings.Repeat("a", 64), "assets/semantic/"+id+".png"))
			layers = append(layers, fmt.Sprintf(`{"id":%q,"asset_id":%q,"start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","motion_id":""}`, id, id))
		}
		raw := fmt.Sprintf(`{
			"schema_version":"renderinggen.overlay-plan.v1","plan_id":"reusable-%d","video_id":"reusable-%d",
			"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":5000,
			"items":[{"id":"group","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"composition_id":"multiple_images","group_motion_id":"image_group_slide_right","start_ms":0,"end_ms":5000,
			"asset_refs":[%s],"image_layers":[%s]}]}`, count, count, strings.Join(assets, ","), strings.Join(layers, ","))
		result, err := CompileSemantic([]byte(raw))
		if err != nil {
			t.Fatalf("compile reusable transition with %d images: %v", count, err)
		}
		images := 0
		var reference []AnimationTrack
		for _, layer := range result.Plan.Layers {
			if layer.Type != "image" {
				continue
			}
			images++
			if layer.Animation == nil || len(layer.Animation.Tracks) != 4 {
				t.Fatalf("group size %d layer %q has no reusable transition tracks: %+v", count, layer.ID, layer.Animation)
			}
			if reference == nil {
				reference = layer.Animation.Tracks
			} else if fmt.Sprint(reference) != fmt.Sprint(layer.Animation.Tracks) {
				t.Errorf("group size %d changed the selected motion across members", count)
			}
		}
		if images != count {
			t.Errorf("group size %d rendered %d image layers", count, images)
		}
	}
}

func TestAllReusableImageAnimationsCompileForOneImage(t *testing.T) {
	ids := motion.ReusableImageGroupMotionIDs()
	if len(ids) != 14 {
		t.Fatalf("reusable image-group inventory has %d motions, want 14: %v", len(ids), ids)
	}
	for _, id := range ids {
		raw := fmt.Sprintf(`{
			"schema_version":"renderinggen.overlay-plan.v1","plan_id":"single-%s","video_id":"single-%s",
			"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":2000,
			"items":[{"id":"image","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"composition_id":"single_image","motion_id":%q,"start_ms":0,"end_ms":2000,
			"asset_refs":[{"asset_id":"image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url":"assets/semantic/image.png","media_type":"image/png"}]}]}`, id, id, id)
		result, err := CompileSemantic([]byte(raw))
		if err != nil {
			t.Fatalf("compile %s for one image: %v", id, err)
		}
		var animated bool
		for _, layer := range result.Plan.Layers {
			if layer.Type == "image" && layer.Animation != nil && len(layer.Animation.Tracks) == 4 {
				animated = true
			}
		}
		if !animated {
			t.Errorf("%s did not compile its four image tracks for a single image", id)
		}
	}
}
