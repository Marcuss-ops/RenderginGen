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

func TestFitEntityImageResizesLinkedPremiumFrameAndMorphMask(t *testing.T) {
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

	imageLayer := overlay.Layer{
		ID: "portrait:image", Type: "image", Asset: "assets/semantic/photo.png",
		BoxWidth: 480, BoxHeight: 480, Size: []float64{480, 480}, Fit: overlay.FitContain,
		EntityImage: true, Enable3D: true, Position: []float64{0, 0, 12},
		Animation:       &overlay.LayerAnimation{Tracks: []overlay.AnimationTrack{{Property: "position_z", Keyframes: []overlay.AnimationKeyframe{{Frame: 0, Value: 4.0}, {Frame: 20, Value: 0.0}}}}},
		PremiumWipeMask: true,
		Masks:           []overlay.LayerMask{{Type: "path", Path: []overlay.LayerPathCommand{{Type: "move_to", Point: []float64{0, 0}}}, TargetPath: []overlay.LayerPathCommand{{Type: "move_to", Point: []float64{1, 1}}}}},
	}
	frame := overlay.Layer{
		ID: "portrait:image:premium:frame", Type: "shape", Size: []float64{480, 480},
		PremiumParentImageID: "portrait:image", PremiumParentSize: []float64{480, 480},
		PremiumSizeScale: []float64{1.06, 1.06}, PremiumZOffset: -20,
		PremiumPathKind: "rounded_rect", PremiumShapeKind: "path", PremiumSyncTransform: true,
		Shape: &overlay.LayerShape{Type: "path", Path: []overlay.LayerPathCommand{{Type: "move_to", Point: []float64{0, 0}}}},
	}
	plan := &overlay.Plan{Canvas: overlay.Canvas{Width: 1280, Height: 720}, Layers: []overlay.Layer{imageLayer, frame}}
	if err := fitEntityImageLayersToAssets(root, plan); err != nil {
		t.Fatal(err)
	}
	gotImage, gotFrame := plan.Layers[0], plan.Layers[1]
	if gotFrame.Size[0] != 480*1.06 || gotFrame.Size[1] != 270*1.06 {
		t.Fatalf("linked frame size = %v, want [508.8 286.2]", gotFrame.Size)
	}
	if gotFrame.Position[2] != -8 || !gotFrame.Enable3D || gotFrame.Animation == nil {
		t.Fatalf("linked frame transform = pos %v enable3d %v animation %+v", gotFrame.Position, gotFrame.Enable3D, gotFrame.Animation)
	}
	if gotFrame.Animation.Tracks[0].Keyframes[0].Value != -16.0 {
		t.Fatalf("frame Z animation did not include its authored offset: %+v", gotFrame.Animation.Tracks[0])
	}
	if len(gotImage.Masks) != 1 || len(gotImage.Masks[0].Path) != 5 || len(gotImage.Masks[0].TargetPath) != 5 {
		t.Fatalf("fitted image mask was not rebuilt at final source geometry: %+v", gotImage.Masks)
	}
}
