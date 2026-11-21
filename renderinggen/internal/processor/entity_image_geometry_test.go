package processor

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestProbePlanImageSizesReadsMaterializedAssets certifies the processor half
// of the sized compile: the plan carries logical asset paths, and the probe
// returns the decoded pixel size for every image layer that needs a source
// aspect — exactly the table the overlay compiler expects, keyed the same way.
func TestProbePlanImageSizesReadsMaterializedAssets(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, filepath.Join(root, "assets", "semantic", "photo.png"), 1280, 1519)
	writeTestPNG(t, filepath.Join(root, "assets", "semantic", "wide.png"), 1600, 900)
	plan := &overlay.Plan{
		Canvas: overlay.Canvas{Width: 1920, Height: 1080},
		Layers: []overlay.Layer{
			{ID: "pin:image", Type: "image", Asset: "assets/semantic/photo.png", DeriveWidthFromAsset: true},
			{ID: "card:image", Type: "image", Asset: "assets/semantic/wide.png", EntityImage: true},
			// A fixed-size image needs no probe: its box is already final.
			{ID: "logo", Type: "image", Asset: "assets/semantic/photo.png", Size: []float64{96, 96}},
		},
	}
	sizes, err := probePlanImageSizes(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := sizes["assets/semantic/photo.png"]; got.Width != 1280 || got.Height != 1519 {
		t.Fatalf("probed photo = %+v, want 1280x1519", got)
	}
	if got := sizes["assets/semantic/wide.png"]; got.Width != 1600 || got.Height != 900 {
		t.Fatalf("probed wide = %+v, want 1600x900", got)
	}
	if len(sizes) != 2 {
		t.Fatalf("probed %d assets, want 2 (fixed-size layers must not be read)", len(sizes))
	}
}

// TestProbePlanImageSizesFailsClosed proves a missing or unreadable source can
// never silently compile at the blind geometry: the probe returns the error
// before any second compile runs.
func TestProbePlanImageSizesFailsClosed(t *testing.T) {
	root := t.TempDir()
	plan := &overlay.Plan{Layers: []overlay.Layer{{
		ID: "card:image", Type: "image", Asset: "assets/semantic/missing.png", EntityImage: true,
	}}}
	if _, err := probePlanImageSizes(root, plan); err == nil {
		t.Fatal("probing a missing materialized asset was accepted")
	}
}

func TestProbePlanImageSizesAlwaysReturnsATable(t *testing.T) {
	sizes, err := probePlanImageSizes(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sizes == nil {
		t.Fatal("nil size table would let the caller fall back to the blind compile")
	}
	if len(sizes) != 0 {
		t.Fatalf("empty plan probed %d assets, want 0", len(sizes))
	}
}
