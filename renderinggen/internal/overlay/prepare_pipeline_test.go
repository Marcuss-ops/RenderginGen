package overlay

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestPendingImageAssetsIncludesChainSegments pins the probe contract for a
// fused chain: the container owns no image of its own, so only the segments can
// tell the processor which images the lowering will fit. When this list misses
// a chain image the prepared lower fails closed ("awaits source dimensions but
// none were probed"), which is the failure the r13 replay surfaced on a
// camera-pan mixed chain.
func TestPendingImageAssetsIncludesChainSegments(t *testing.T) {
	asset := func(id, path string) string {
		return `{"asset_id":"` + id + `","sha256":"` + strings.Repeat(id, 64) + `","url":"` + path + `","media_type":"image/jpeg"}`
	}
	segment := func(id string, startUS int64) string {
		return `{"kind":"entity_image","template_id":"image_popup","preset_id":"image_scale_in","image_preset_id":"image_scale_in",` +
			`"asset_id":"` + id + `","assets":[` + asset(id, "assets/semantic/"+id+".jpg") + `],` +
			`"start_us":` + strconv.FormatInt(startUS, 10) + `,"duration_us":3000000,"params":{"width":520,"height":520}}`
	}
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"pending-chain","video_id":"pending-chain",
		"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":6000,
		"items":[{
			"id":"pending-chain-1","kind":"image_chain","template_id":"IMAGE_CHAIN",
			"start_ms":0,"end_ms":6000,
			"asset_refs":[` + asset("a", "assets/semantic/a.jpg") + `,` + asset("b", "assets/semantic/b.jpg") + `],
			"params":{"chain_segments":[` + segment("a", 0) + `,` + segment("b", 3000000) + `]}
		}]
	}`)

	resolved, err := ResolveSemanticForPrepare(raw, CompileOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := resolved.PendingImageAssets()
	want := []string{"assets/semantic/a.jpg", "assets/semantic/b.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pending image assets = %v, want the two chain segment images %v", got, want)
	}
}
