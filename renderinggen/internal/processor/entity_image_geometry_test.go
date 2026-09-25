package processor

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func TestFitEntityImageLayersToMaterializedAssetRemovesMatteGeometry(t *testing.T) {
	root := t.TempDir()
	assetPath := filepath.Join(root, "assets", "semantic", "photo.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1600, 900))); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	plan := &overlay.Plan{Layers: []overlay.Layer{{
		ID: "portrait:image", Type: "image", Asset: "assets/semantic/photo.png",
		BoxWidth: 480, BoxHeight: 480, Size: []float64{480, 480}, Fit: overlay.FitContain,
		EntityImage: true,
	}}}
	if err := fitEntityImageLayersToAssets(root, plan); err != nil {
		t.Fatal(err)
	}
	layer := plan.Layers[0]
	if layer.BoxWidth != 480 || layer.BoxHeight != 270 || layer.Size[0] != 480 || layer.Size[1] != 270 {
		t.Fatalf("layer geometry = box %dx%d size %v, want 480x270", layer.BoxWidth, layer.BoxHeight, layer.Size)
	}
	if layer.EntityImage {
		t.Fatal("transient entity-image marker survived after materialization")
	}
	if layer.Fit != overlay.FitCover {
		t.Fatalf("entity image fit = %q, want cover to remove contain matte", layer.Fit)
	}
}
