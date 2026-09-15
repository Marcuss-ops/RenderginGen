package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// normalizeMaterializedImagePaths is the workaround for a producer that declares
// one image format and publishes another, and it runs on the critical path to
// Chronon: it rewrites the plan's asset paths in place and returns the rename map
// the prepared-package manifest is projected through. A bug here is a black
// render (wrong extension kept) or a missing-asset failure (plan rewritten
// without the file being moved), so the contract gets tests of its own.

// imageFixtures are real magic-number prefixes, matching what the media
// vocabulary sniffs. They are duplicated as literals on purpose: this test must
// fail if the shared vocabulary and the pass stop agreeing.
var imageFixtures = map[string][]byte{
	".png":  append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...),
	".jpg":  append([]byte("\xff\xd8\xff\xe0"), make([]byte, 64)...),
	".webp": append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 64)...),
	".gif":  append([]byte("GIF89a"), make([]byte, 64)...),
}

// writeAsset materializes one workspace asset.
func writeAsset(t *testing.T, root, logical string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(logical))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// imagePlan builds a plan whose layers reference the given asset paths.
func imagePlan(assets ...string) *overlay.Plan {
	plan := &overlay.Plan{}
	for i, asset := range assets {
		plan.Layers = append(plan.Layers, overlay.Layer{
			ID:    fmt.Sprintf("layer_%d", i+1),
			Type:  "image",
			Asset: asset,
		})
	}
	return plan
}

// assetPaths lists the workspace files under root, as slash paths, sorted.
func assetPaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

// TestNormalizeMaterializedImagePathsRenamesMismatchedBytes is the case the
// workaround exists for: JPEG bytes behind a .png path must be renamed, and the
// plan must be rewritten to the new path (otherwise Chronon is handed a path that
// no longer exists).
func TestNormalizeMaterializedImagePathsRenamesMismatchedBytes(t *testing.T) {
	root := t.TempDir()
	writeAsset(t, root, "assets/photo.png", imageFixtures[".jpg"])
	plan := imagePlan("assets/photo.png")

	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := renamed["assets/photo.jpg"]; got != "assets/photo.png" {
		t.Fatalf("rename map = %v, want assets/photo.jpg → assets/photo.png", renamed)
	}
	if got := plan.Layers[0].Asset; got != "assets/photo.jpg" {
		t.Fatalf("plan asset = %q, want the renamed path assets/photo.jpg", got)
	}
	if paths := assetPaths(t, root); len(paths) != 1 || paths[0] != "assets/photo.jpg" {
		t.Fatalf("workspace files = %v, want only assets/photo.jpg", paths)
	}
}

// TestNormalizeMaterializedImagePathsLeavesCorrectAssetsAlone pins that a
// producer who declared the right extension pays nothing: no rename, no plan
// mutation, no entry in the map (which is what the caller uses to decide whether
// the prepared manifest needs projecting at all).
func TestNormalizeMaterializedImagePathsLeavesCorrectAssetsAlone(t *testing.T) {
	root := t.TempDir()
	for ext, data := range imageFixtures {
		name := "assets/photo" + ext
		writeAsset(t, root, name, data)
		// The .jpg fixture is JPEG; every other fixture matches its own name.
		plan := imagePlan(name)
		renamed, err := normalizeMaterializedImagePaths(root, plan)
		if err != nil {
			t.Fatalf("%s: normalize: %v", name, err)
		}
		if len(renamed) != 0 {
			t.Fatalf("%s: renamed = %v, want no renames", name, renamed)
		}
		if got := plan.Layers[0].Asset; got != name {
			t.Fatalf("%s: plan asset = %q, want it untouched", name, got)
		}
	}
	// Uppercase extensions are the same extension as far as the loader is
	// concerned, so no rename is issued.
	upper := "assets/PHOTO.PNG"
	writeAsset(t, root, upper, imageFixtures[".png"])
	plan := imagePlan(upper)
	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err != nil {
		t.Fatalf("uppercase: %v", err)
	}
	if len(renamed) != 0 || plan.Layers[0].Asset != upper {
		t.Fatalf("uppercase: renamed=%v asset=%q, want the path untouched", renamed, plan.Layers[0].Asset)
	}
}

// TestNormalizeMaterializedImagePathsLeavesUnknownFormatsAlone pins the
// fail-safe direction: bytes the vocabulary cannot name keep the producer's path,
// because renaming them to a raster extension would hand Chronon a decoder that
// cannot read the file — turning a visible mismatch into a silent black frame.
func TestNormalizeMaterializedImagePathsLeavesUnknownFormatsAlone(t *testing.T) {
	root := t.TempDir()
	writeAsset(t, root, "assets/logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`))
	writeAsset(t, root, "assets/photo.bmp", append([]byte("BM"), make([]byte, 64)...))
	plan := imagePlan("assets/logo.svg", "assets/photo.bmp")

	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(renamed) != 0 {
		t.Fatalf("renamed = %v, want nothing renamed for unrecognized formats", renamed)
	}
	if plan.Layers[0].Asset != "assets/logo.svg" || plan.Layers[1].Asset != "assets/photo.bmp" {
		t.Fatalf("plan assets = %q, %q, want both paths untouched", plan.Layers[0].Asset, plan.Layers[1].Asset)
	}
}

// TestNormalizeMaterializedImagePathsSkipsNonImageLayers pins that only image
// layers are sniffed: a text layer's asset field and a video layer's path are not
// pictures, and renaming them would corrupt a render that was already correct.
func TestNormalizeMaterializedImagePathsSkipsNonImageLayers(t *testing.T) {
	root := t.TempDir()
	writeAsset(t, root, "assets/clip.png", imageFixtures[".png"])
	plan := &overlay.Plan{Layers: []overlay.Layer{
		{ID: "text", Type: "text", Asset: "assets/clip.png"},
		{ID: "video", Type: "video", Asset: "assets/clip.png"},
		{ID: "background", Type: "background"},
		{ID: "image", Type: "image", Asset: ""},
	}}

	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(renamed) != 0 {
		t.Fatalf("renamed = %v, want no renames for non-image layers", renamed)
	}
	for _, layer := range plan.Layers {
		want := "assets/clip.png"
		if layer.Type == "background" || layer.Asset == "" {
			want = ""
		}
		if layer.Asset != want {
			t.Fatalf("layer %s asset = %q, want it untouched (%q)", layer.ID, layer.Asset, want)
		}
	}
}

// TestNormalizeMaterializedImagePathsMultipleImages pins that a plan with several
// mismatched images is normalized in one pass, each mapped to its own rename.
func TestNormalizeMaterializedImagePathsMultipleImages(t *testing.T) {
	root := t.TempDir()
	writeAsset(t, root, "assets/one.png", imageFixtures[".jpg"])
	writeAsset(t, root, "assets/two.png", imageFixtures[".webp"])
	writeAsset(t, root, "assets/three.png", imageFixtures[".png"])
	plan := imagePlan("assets/one.png", "assets/two.png", "assets/three.png")

	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := map[string]string{
		"assets/one.jpg":  "assets/one.png",
		"assets/two.webp": "assets/two.png",
	}
	if len(renamed) != len(want) {
		t.Fatalf("renamed = %v, want %v", renamed, want)
	}
	for newPath, oldPath := range want {
		if renamed[newPath] != oldPath {
			t.Fatalf("renamed[%q] = %q, want %q", newPath, renamed[newPath], oldPath)
		}
	}
	if plan.Layers[0].Asset != "assets/one.jpg" || plan.Layers[1].Asset != "assets/two.webp" {
		t.Fatalf("plan assets = %q, %q, want the renamed paths", plan.Layers[0].Asset, plan.Layers[1].Asset)
	}
	if plan.Layers[2].Asset != "assets/three.png" {
		t.Fatalf("the already-correct layer was rewritten to %q", plan.Layers[2].Asset)
	}
	paths := assetPaths(t, root)
	sort.Strings(paths)
	wantFiles := []string{"assets/one.jpg", "assets/three.png", "assets/two.webp"}
	if len(paths) != len(wantFiles) {
		t.Fatalf("workspace files = %v, want %v", paths, wantFiles)
	}
	for i := range wantFiles {
		if paths[i] != wantFiles[i] {
			t.Fatalf("workspace files = %v, want %v", paths, wantFiles)
		}
	}
}

// TestNormalizeMaterializedImagePathsErrorsOnMissingAsset pins the fail-closed
// half: a plan that references a file the workspace does not have is reported
// here, naming the asset, rather than left for Chronon to discover.
func TestNormalizeMaterializedImagePathsErrorsOnMissingAsset(t *testing.T) {
	root := t.TempDir()
	plan := imagePlan("assets/missing.png")
	renamed, err := normalizeMaterializedImagePaths(root, plan)
	if err == nil {
		t.Fatalf("expected an error for a missing asset, got renamed=%v", renamed)
	}
	if !strings.Contains(err.Error(), "assets/missing.png") {
		t.Fatalf("error = %q, want it to name the missing asset", err)
	}
}

// TestNormalizeMaterializedImagePathsErrorsOnEmptyFile pins that a zero-byte
// asset is an error, not a silently skipped file: an empty image is a
// materialization bug, and reporting it is how a retry is triggered.
func TestNormalizeMaterializedImagePathsErrorsOnEmptyFile(t *testing.T) {
	root := t.TempDir()
	writeAsset(t, root, "assets/empty.png", nil)
	if _, err := normalizeMaterializedImagePaths(root, imagePlan("assets/empty.png")); err == nil {
		t.Fatal("expected an error for a zero-byte image asset")
	}
}

// TestNormalizeMaterializedImagePathsNilPlan pins the documented no-op: a job
// without a plan has nothing to normalize.
func TestNormalizeMaterializedImagePathsNilPlan(t *testing.T) {
	renamed, err := normalizeMaterializedImagePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("normalize(nil plan): %v", err)
	}
	if renamed != nil {
		t.Fatalf("renamed = %v, want nil", renamed)
	}
}

// TestReadFileHeadShortReads pins the head reader's contract, which the rename
// decision depends on: a file shorter than the sniff window yields its bytes
// rather than an error, and only a truly empty file fails.
func TestReadFileHeadShortReads(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.bin")
	if err := os.WriteFile(small, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, err := readFileHead(small, imageSniffBytes)
	if err != nil {
		t.Fatalf("readFileHead(short file): %v", err)
	}
	if string(head) != "abc" {
		t.Fatalf("head = %q, want %q", head, "abc")
	}

	empty := filepath.Join(root, "empty.bin")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileHead(empty, imageSniffBytes); err == nil {
		t.Fatal("readFileHead(empty file) must fail")
	}

	if _, err := readFileHead(filepath.Join(root, "absent.bin"), imageSniffBytes); err == nil {
		t.Fatal("readFileHead(missing file) must fail")
	}
}

// TestFinalPreparedAssetsProjectsRenames pins the prepared-package projection:
// both the old and the new logical path are declared so the package can resolve
// the asset whichever path the consumer holds, and the content hash is
// unchanged because a rename does not touch bytes.
func TestFinalPreparedAssetsProjectsRenames(t *testing.T) {
	compiled := []overlay.Asset{
		{Hash: "h1", LogicalPath: "assets/photo.png"},
		{Hash: "h2", LogicalPath: "assets/other.png"},
	}
	projected := finalPreparedAssets(compiled, map[string]string{"assets/photo.jpg": "assets/photo.png"})
	var paths []string
	for _, asset := range projected {
		paths = append(paths, asset.LogicalPath)
		if asset.LogicalPath == "assets/photo.jpg" && asset.Hash != "h1" {
			t.Fatalf("projected asset hash = %q, want the original h1 (a rename does not change bytes)", asset.Hash)
		}
	}
	sort.Strings(paths)
	want := []string{"assets/other.png", "assets/photo.jpg", "assets/photo.png"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

// TestFinalPreparedAssetsWithoutRenamesIsTheIdentity pins the common case: the
// compiled manifest is returned as-is (not copied) when nothing was renamed.
func TestFinalPreparedAssetsWithoutRenamesIsTheIdentity(t *testing.T) {
	compiled := []overlay.Asset{{Hash: "h1", LogicalPath: "assets/photo.png"}}
	projected := finalPreparedAssets(compiled, nil)
	if len(projected) != 1 || projected[0] != compiled[0] {
		t.Fatalf("projected = %v, want the input unchanged", projected)
	}
}
