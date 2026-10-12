package overlay

import (
	"strings"
	"testing"
)

func TestCompileAssetlessClipStartLightLeakToTransparentGlowLayers(t *testing.T) {
	result := compileJSON(t, `{"id":"clip-leak","kind":"light_leak","template_id":"LIGHT_LEAK","motion_id":"lightleak_horizontal_sweep","start_ms":1000,"end_ms":2200}`)
	if result.Plan == nil || len(result.Plan.Layers) == 0 {
		t.Fatal("clip-start light leak compiled without visible layers")
	}
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" {
			t.Fatalf("transparent support surface leaked into render plan: %+v", layer)
		}
	}
}

func TestAssetlessLightLeakRejectsNonLightLeakMotion(t *testing.T) {
	_, err := CompileSemantic([]byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{"id":"clip-leak","kind":"light_leak","template_id":"LIGHT_LEAK","motion_id":"image_focus_reveal","start_ms":0,"end_ms":1200}]}`))
	if err == nil || !strings.Contains(err.Error(), "light_leak_v1") {
		t.Fatalf("compile error = %v, want light_leak_v1 admission error", err)
	}
}
