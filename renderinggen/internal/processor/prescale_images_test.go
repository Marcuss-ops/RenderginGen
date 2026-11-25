package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

func writePrescaleTestPNG(t *testing.T, path string, width, height int) []byte {
	t.Helper()
	data := testImagePNG(t, width, height)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func imageDimensions(t *testing.T, path string) (int, int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height
}

func TestPrescalePlanImagesCreatesDisplayBoundDerivative(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "assets", "semantic", "photo.png")
	sourceBytes := writePrescaleTestPNG(t, sourcePath, 2400, 1600)
	sourceHash := storage.Hash(sourceBytes)
	plan := &overlay.Plan{
		Canvas: overlay.Canvas{Width: 1920, Height: 1080},
		Layers: []overlay.Layer{{ID: "photo", Type: "image", Asset: "assets/semantic/photo.png"}},
	}

	images, err := prescalePlanImages(root, plan, "", nil)
	if err != nil {
		t.Fatalf("prescalePlanImages: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("prescaled images = %d, want 1", len(images))
	}
	got := images[0]
	if got.OriginalPath != "assets/semantic/photo.png" {
		t.Fatalf("original path = %q", got.OriginalPath)
	}
	if !strings.HasPrefix(got.Path, "assets/prescaled/") || !strings.HasSuffix(got.Path, "/photo.png") {
		t.Fatalf("derived path = %q, want assets/prescaled/<hash>/photo.png", got.Path)
	}
	if got.Width != 2048 || got.Height != 1365 {
		t.Fatalf("derived size = %dx%d, want 2048x1365", got.Width, got.Height)
	}
	if plan.Layers[0].Asset != got.Path {
		t.Fatalf("plan asset = %q, want %q", plan.Layers[0].Asset, got.Path)
	}
	if width, height := imageDimensions(t, sourcePath); width != 2400 || height != 1600 {
		t.Fatalf("source mutated to %dx%d, want 2400x1600", width, height)
	}
	if width, height := imageDimensions(t, filepath.Join(root, filepath.FromSlash(got.Path))); width != 2048 || height != 1365 {
		t.Fatalf("derived file = %dx%d, want 2048x1365", width, height)
	}
	if got.Hash != storage.Hash(got.Data) {
		t.Fatalf("derived hash = %q, want %q", got.Hash, storage.Hash(got.Data))
	}
	if storage.Hash(sourceBytes) != sourceHash {
		t.Fatal("source bytes changed")
	}
}

func TestPrescalePlanImagesSkipsSmallImagesAndMapRasters(t *testing.T) {
	root := t.TempDir()
	writePrescaleTestPNG(t, filepath.Join(root, "assets", "small.png"), 1200, 800)
	writePrescaleTestPNG(t, filepath.Join(root, "assets", "map.png"), 2400, 1600)
	plan := &overlay.Plan{
		Canvas: overlay.Canvas{Width: 1920, Height: 1080},
		Layers: []overlay.Layer{
			{ID: "small", Type: "image", Asset: "assets/small.png"},
			{ID: "map", Type: "image", Asset: "assets/map.png", MapRasterWidth: 2400, MapRasterHeight: 1600},
		},
	}

	images, err := prescalePlanImages(root, plan, "", nil)
	if err != nil {
		t.Fatalf("prescalePlanImages: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("prescaled images = %d, want 0", len(images))
	}
	for i, layer := range plan.Layers {
		if strings.Contains(layer.Asset, "prescaled") {
			t.Fatalf("layer %d was rewritten: %q", i, layer.Asset)
		}
	}
}

func TestPrepareJobPrescalesOversizedImageAsset(t *testing.T) {
	proc, store, _ := newProcessorWith(t, Options{})
	assetBytes := testImagePNG(t, 2400, 1600)
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	job := &queue.Job{
		ID: "prescale-job-1", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"prescale-job-1","video_id":"prescale-job-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"img-1","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":0,"end_ms":1000,
             "asset_refs":[{"asset_id":"photo","sha256":"` + assetHash + `","url":"https://store.example/objects/photo.png","media_type":"image/png"}]}
          ]
        }`),
	}

	prepared, err := proc.PrepareJob(context.Background(), job)
	if err != nil {
		t.Fatalf("PrepareJob: %v", err)
	}
	t.Cleanup(func() { _ = prepared.Workspace.Cleanup() })

	var imageAsset string
	for _, layer := range prepared.Plan.Layers {
		if layer.Type == "image" {
			imageAsset = layer.Asset
			break
		}
	}
	if !strings.HasPrefix(imageAsset, "assets/prescaled/") {
		t.Fatalf("plan image asset = %q, want a prescaled derivative", imageAsset)
	}
	derivedPath, err := prepared.Workspace.AssetPath(imageAsset)
	if err != nil {
		t.Fatalf("asset path: %v", err)
	}
	if width, height := imageDimensions(t, derivedPath); width != 960 || height != 640 {
		t.Fatalf("derived file = %dx%d, want 960x640 (2x the fitted card box)", width, height)
	}
	sourcePath := filepath.Join(prepared.Workspace.Root(), "assets", "semantic", "photo.png")
	if width, height := imageDimensions(t, sourcePath); width != 2400 || height != 1600 {
		t.Fatalf("source file = %dx%d, want 2400x1600", width, height)
	}

	derivedBytes, err := os.ReadFile(derivedPath)
	if err != nil {
		t.Fatal(err)
	}
	derivedHash := storage.Hash(derivedBytes)
	found := false
	for _, asset := range prepared.PreparedOverlay.Assets {
		if asset.LogicalPath == imageAsset && asset.ContentHash == derivedHash {
			found = true
		}
		if asset.LogicalPath == "assets/semantic/photo.png" {
			t.Fatalf("prepared package still binds the oversized source: %+v", asset)
		}
	}
	if !found {
		t.Fatalf("prepared package does not bind the derived asset %q (%s): %+v", imageAsset, derivedHash, prepared.PreparedOverlay.Assets)
	}
	if !bytes.Equal(assetBytes, mustReadFile(t, filepath.Join(prepared.Workspace.Root(), "assets", "semantic", "photo.png"))) {
		t.Fatal("source bytes changed during prescale")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImageDisplayBoundUsesDisplayPixelsForImageFocusReveal(t *testing.T) {
	layer := overlay.Layer{ID: "image-focus-reveal__scene-01-image-0__image__01", Type: "image", Size: []float64{1340, 760}}
	if got := imageDisplayBound(layer); got != 1340 {
		t.Fatalf("image-focus bound = %d, want 1340 (1x display box)", got)
	}
	underscored := overlay.Layer{ID: "image_focus_reveal__scene-01-image-0__image__01", Type: "image", Size: []float64{1340, 760}}
	if got := imageDisplayBound(underscored); got != 1340 {
		t.Fatalf("image_focus_reveal bound = %d, want 1340 (1x display box)", got)
	}
	regular := overlay.Layer{ID: "regular-image", Type: "image", Size: []float64{1340, 760}}
	if got := imageDisplayBound(regular); got != 2048 {
		t.Fatalf("regular bound = %d, want 2048 (2x cap)", got)
	}
}

func TestDisplayBoundSize(t *testing.T) {
	tests := []struct {
		width, height, max int
		wantW, wantH       int
	}{
		{1600, 900, 2048, 1600, 900},
		{2400, 1600, 2048, 2048, 1365},
		{1600, 2400, 2048, 1365, 2048},
		{3000, 3000, 2048, 2048, 2048},
	}
	for _, test := range tests {
		gotW, gotH := displayBoundSize(test.width, test.height, test.max)
		if gotW != test.wantW || gotH != test.wantH {
			t.Errorf("displayBoundSize(%d,%d,%d) = %dx%d, want %dx%d",
				test.width, test.height, test.max, gotW, gotH, test.wantW, test.wantH)
		}
	}
}
