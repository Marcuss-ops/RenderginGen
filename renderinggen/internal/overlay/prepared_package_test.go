package overlay

import (
	"strings"
	"testing"
)

func TestCompileSemanticBuildsDeduplicatedPreparedPackage(t *testing.T) {
	hash := strings.Repeat("a", 64)
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"prepared-1","video_id":"video-1","language":"it",
		"width":1920,"height":1080,"fps_num":30,"fps_den":1,
		"items":[
			{"id":"phrase-a","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"static_text_smoke","text":"Frase completa tradotta","start_ms":0,"end_ms":1000},
			{"id":"phrase-b","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"static_text_smoke","text":"Frase completa tradotta","start_ms":1000,"end_ms":2000},
			{"id":"image-a","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":0,"end_ms":1000,"asset_refs":[{"asset_id":"photo","sha256":"` + hash + `","url":"https://cdn.test/photo.png","media_type":"image/png"}]},
			{"id":"image-b","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":1000,"end_ms":2000,"asset_refs":[{"asset_id":"photo","sha256":"` + hash + `","url":"https://cdn.test/photo.png","media_type":"image/png"}]}
		]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Prepared.Language != "it" {
		t.Fatalf("language = %q, want it", result.Prepared.Language)
	}
	if len(result.Prepared.TextRuns) != 1 {
		t.Fatalf("text runs = %d, want one deduplicated complete phrase", len(result.Prepared.TextRuns))
	}
	if len(result.Prepared.Assets) != 1 {
		t.Fatalf("prepared assets = %d, want one content-addressed image", len(result.Prepared.Assets))
	}
	if len(result.Prepared.Overlays) != 4 {
		t.Fatalf("prepared overlays = %d, want four frame references", len(result.Prepared.Overlays))
	}
	if result.Prepared.Overlays[0].TextKey == "" || result.Prepared.Overlays[1].TextKey != result.Prepared.Overlays[0].TextKey {
		t.Fatalf("identical complete phrases did not share a text key: %+v", result.Prepared.Overlays[:2])
	}
	if result.Prepared.Overlays[2].AssetKey == "" || result.Prepared.Overlays[3].AssetKey != result.Prepared.Overlays[2].AssetKey {
		t.Fatalf("identical images did not share an asset key: %+v", result.Prepared.Overlays[2:])
	}
	if result.Prepared.ContentHash == "" {
		t.Fatal("prepared package has no content hash")
	}
	if result.Prepared.Overlays[0].Dynamic.DurationFrames != 30 {
		t.Fatalf("first dynamic duration = %d, want 30", result.Prepared.Overlays[0].Dynamic.DurationFrames)
	}
}

func TestPreparedPackageIdentityIsDeterministic(t *testing.T) {
	const raw = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","language":"en","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"t","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"static_text_smoke","text":"READY","start_ms":0,"end_ms":1000}]}`
	a, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if a.Prepared.ContentHash != b.Prepared.ContentHash {
		t.Fatalf("prepared hash changed across identical compiles: %s != %s", a.Prepared.ContentHash, b.Prepared.ContentHash)
	}
}
