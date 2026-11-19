package overlay

import (
	"os"
	"testing"
)

func TestDebugR4PlanCaptionPositions(t *testing.T) {
	path := os.Getenv("DEBUG_PLAN")
	if path == "" {
		t.Skip("DEBUG_PLAN not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range res.Plan.Layers {
		if layer.Type == "image" || layer.CaptionForImageID != "" {
			t.Logf("layer=%s type=%s pos=%v size=%v caption_for=%s entity_image=%v enable3d=%v screen_space=%v", layer.ID, layer.Type, layer.Position, layer.Size, layer.CaptionForImageID, layer.EntityImage, layer.Enable3D, layer.ScreenSpace)
		}
	}
}
