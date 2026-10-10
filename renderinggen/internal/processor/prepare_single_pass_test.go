package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// testImageJPEG encodes real JPEG bytes: the prepare stage sniffs the file head
// AND fits the entity image box from the decoded bytes, so a fake signature
// would not exercise the path this test is about.
func testImageJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPrepareJobRebuildsThePreparedPackageFromTheFinalPlan pins the single-pass
// prepare contract on the case that used to rebuild the sidecar twice: a job
// whose image bytes force an extension rename (JPEG bytes declared as .png) and
// whose entity image box is fitted from those bytes.
//
// The sidecar that reaches Chronon must describe the FINAL plan — the renamed
// logical path, and a content hash that validates against its own body — rather
// than the compiler's mid-prepare view. Chronon validates this relationship
// 1:1 before GPU compilation, so a stale sidecar is a rejected or wrong render.
func TestPrepareJobRebuildsThePreparedPackageFromTheFinalPlan(t *testing.T) {
	proc, store, _ := newProcessorWith(t, Options{})
	assetBytes := testImageJPEG(t, 1600, 900)
	assetHash := storage.Hash(assetBytes)
	if err := store.Put(context.Background(), assetHash, assetBytes); err != nil {
		t.Fatalf("put asset: %v", err)
	}

	job := &queue.Job{
		ID: "single-pass-1", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"single-pass-1","video_id":"single-pass-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"img-1","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":0,"end_ms":1000,
             "asset_refs":[{"asset_id":"apple","sha256":"` + assetHash + `","url":"https://store.example/objects/apple.png","media_type":"image/png"}]}
          ]
        }`),
	}

	prepared, err := proc.PrepareJob(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = prepared.Workspace.Cleanup() }()

	// The plan (and therefore the sidecar) must point at the bytes that are
	// actually on disk: the producer declared .png while publishing JPEG.
	var planAssets []string
	for _, layer := range prepared.Plan.Layers {
		if layer.Asset != "" {
			planAssets = append(planAssets, layer.Asset)
		}
	}
	if len(planAssets) == 0 || !strings.HasSuffix(planAssets[0], ".jpg") {
		t.Fatalf("plan assets = %v, want the normalized .jpg path", planAssets)
	}

	raw, err := os.ReadFile(prepared.Workspace.PreparedPackagePath())
	if err != nil {
		t.Fatalf("read prepared sidecar: %v", err)
	}
	var pkg overlay.PreparedPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("decode prepared sidecar: %v", err)
	}
	// The content hash covers the whole body, so Validate() fails on a sidecar
	// assembled from mixed plan states.
	if err := pkg.Validate(); err != nil {
		t.Fatalf("prepared sidecar does not validate against its own content: %v", err)
	}
	if len(pkg.Assets) == 0 {
		t.Fatalf("prepared sidecar carries no asset binding: %+v", pkg)
	}
	for _, asset := range pkg.Assets {
		if strings.HasSuffix(asset.LogicalPath, ".png") {
			t.Errorf("prepared sidecar still describes the pre-rename path %q; assets=%+v", asset.LogicalPath, pkg.Assets)
		}
		if asset.LogicalPath != planAssets[0] {
			t.Errorf("prepared asset path %q disagrees with plan layer %q", asset.LogicalPath, planAssets[0])
		}
	}
	// The sidecar is what Chronon reads, and the renamed file must exist.
	assetPath, err := prepared.Workspace.AssetPath(pkg.Assets[0].LogicalPath)
	if err != nil {
		t.Fatalf("resolve sidecar asset %q: %v", pkg.Assets[0].LogicalPath, err)
	}
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("sidecar asset %q is not in the workspace: %v", pkg.Assets[0].LogicalPath, err)
	}
}
