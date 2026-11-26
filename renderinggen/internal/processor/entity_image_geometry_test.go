package processor

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// resolveImagePlan resolves a two-image document (one entity portrait, one
// fixed-size overlay) WITHOUT lowering it, which is the state the prepare path
// is in when it probes the materialized bytes.
func resolveImagePlan(t *testing.T, width, height int) *overlay.PreparedResolve {
	t.Helper()
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	raw := json.RawMessage(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"geometry-probe","video_id":"geometry-probe",
      	      "width":` + strconv.Itoa(width) + `,"height":` + strconv.Itoa(height) + `,"fps_num":30,"fps_den":1,"duration_ms":2000,
      "items":[
        {"id":"photo","kind":"entity_image","template_id":"image_popup",
         "preset_id":"image_focus_in","image_preset_id":"image_focus_in",
         "entity_caption":"Ada Lovelace","start_ms":0,"end_ms":2000,"params":{"width":480,"height":480},
         "asset_refs":[{"asset_id":"photo","sha256":"` + hashA + `","url":"https://store.example/objects/photo.png","media_type":"image/png"}]},
        {"id":"wide","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in",
         "start_ms":0,"end_ms":2000,"params":{"width":96,"height":96},
         "asset_refs":[{"asset_id":"wide","sha256":"` + hashB + `","url":"https://store.example/objects/wide.png","media_type":"image/png"}]}
      ]
    }`)
	resolved, err := overlay.ResolveSemanticForPrepare(raw, overlay.CompileOptions{})
	if err != nil {
		t.Fatalf("resolve image plan: %v", err)
	}
	return resolved
}

// TestProbePendingImageSizesReadsMaterializedAssets certifies the processor half
// of the sized lower: the resolve names the images whose geometry derives from
// their source, and the probe returns the decoded pixel size for each of them,
// keyed by the same logical path the plan will carry.
//
// The probe runs before any layer exists, so it reads the whole image set of the
// item resolution rather than the (not yet built) layer table. A fixed-size
// image appearing here is extra header parsing, never a wrong box: lowering only
// consumes the sizes its layers ask for.
func TestProbePendingImageSizesReadsMaterializedAssets(t *testing.T) {
	root := t.TempDir()
	resolved := resolveImagePlan(t, 1920, 1080)
	paths := resolved.PendingImageAssets()
	if len(paths) != 2 {
		t.Fatalf("pending image assets = %v, want the two referenced images", paths)
	}
	for _, path := range paths {
		if !strings.HasSuffix(path, ".png") {
			t.Fatalf("pending image %q does not end in .png", path)
		}
	}
	writeTestPNG(t, filepath.Join(root, filepath.FromSlash(paths[0])), 1280, 1519)
	writeTestPNG(t, filepath.Join(root, filepath.FromSlash(paths[1])), 1600, 900)

	proc := &Processor{}
	sizes, err := proc.probePendingImageSizes(root, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if got := sizes[paths[0]]; got.Width != 1280 || got.Height != 1519 {
		t.Fatalf("probed %s = %+v, want 1280x1519", paths[0], got)
	}
	if got := sizes[paths[1]]; got.Width != 1600 || got.Height != 900 {
		t.Fatalf("probed %s = %+v, want 1600x900", paths[1], got)
	}
	if len(sizes) != len(paths) {
		t.Fatalf("probed %d assets, want %d", len(sizes), len(paths))
	}
}

// TestProbePendingImageSizesFailsClosed proves a missing or unreadable source
// can never silently lower at the blind geometry: the probe returns the error
// before the one lower runs.
func TestProbePendingImageSizesFailsClosed(t *testing.T) {
	resolved := resolveImagePlan(t, 1920, 1080)
	proc := &Processor{}
	if _, err := proc.probePendingImageSizes(t.TempDir(), resolved); err == nil {
		t.Fatal("probing a missing materialized asset was accepted")
	}
}

// TestProbePendingImageSizesAlwaysReturnsATable pins the non-nil contract: an
// empty table still selects the sized lower instead of a blind fallback.
func TestProbePendingImageSizesAlwaysReturnsATable(t *testing.T) {
	raw := json.RawMessage(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"probe-empty","video_id":"probe-empty",
      "width":640,"height":360,"fps_num":30,"fps_den":1,"duration_ms":1000,
      "source":{"asset_id":"clip","sha256":"` + strings.Repeat("c", 64) + `","path":"videos/clip.mp4"}
    }`)
	resolved, err := overlay.ResolveSemanticForPrepare(raw, overlay.CompileOptions{})
	if err != nil {
		t.Fatalf("resolve source-only plan: %v", err)
	}
	proc := &Processor{}
	sizes, err := proc.probePendingImageSizes(t.TempDir(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if sizes == nil {
		t.Fatal("nil size table would let the caller fall back to a blind lower")
	}
	if len(sizes) != 0 {
		t.Fatalf("source-only plan probed %d assets, want 0", len(sizes))
	}
}
