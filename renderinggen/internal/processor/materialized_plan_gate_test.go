package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func TestValidateMaterializedPlanAssetsFailsBeforeChronon(t *testing.T) {
	root := t.TempDir()
	plan := &overlay.Plan{Layers: []overlay.Layer{{ID: "entity-image", Type: "image", Asset: "assets/semantic/missing.jpg"}}}

	// A nil proof set is the "this stage created nothing" case: every plan
	// asset is stat-verified, so the gate keeps its original fail-closed
	// behaviour for paths the prepare stage did not produce itself.
	if err := validateMaterializedPlanAssets(root, plan, nil); err == nil {
		t.Fatal("missing plan asset must fail before Chronon")
	}

	path := filepath.Join(root, "assets", "semantic", "missing.jpg")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateMaterializedPlanAssets(root, plan, nil); err != nil {
		t.Fatalf("materialized plan asset rejected: %v", err)
	}
}

// TestValidateMaterializedPlanAssetsTrustsProvenPaths pins both halves of the
// contract the prepare stage depends on: a path this stage itself created needs
// no stat (that is the removed duplicate I/O), while a plan path it did NOT
// create is still verified on the filesystem. Dropping the second half would
// silently turn the fail-closed boundary into a no-op.
func TestValidateMaterializedPlanAssetsTrustsProvenPaths(t *testing.T) {
	// Deliberately empty: any stat would fail, so passing proves none happened.
	root := t.TempDir()
	proven := map[string]struct{}{"assets/semantic/present.jpg": {}}
	plan := &overlay.Plan{Layers: []overlay.Layer{
		{ID: "proven", Type: "image", Asset: "assets/semantic/present.jpg"},
	}}

	if err := validateMaterializedPlanAssets(root, plan, proven); err != nil {
		t.Fatalf("a path proven present by the prepare stage must not need a stat: %v", err)
	}

	plan.Layers = append(plan.Layers, overlay.Layer{
		ID: "unproven", Type: "image", Asset: "assets/semantic/absent.jpg",
	})
	if err := validateMaterializedPlanAssets(root, plan, proven); err == nil {
		t.Fatal("an asset the prepare stage never created must still be stat-verified")
	}
}

// TestValidateMaterializedPlanAssetsRejectsUnsafePathsEvenWhenProven guards the
// ordering inside the gate: the workspace-relative check runs before the proof
// lookup, so membership in the materialized set can never smuggle an absolute
// or traversing path past the boundary.
func TestValidateMaterializedPlanAssetsRejectsUnsafePathsEvenWhenProven(t *testing.T) {
	root := t.TempDir()
	for _, asset := range []string{"/etc/passwd", "../escape.jpg"} {
		plan := &overlay.Plan{Layers: []overlay.Layer{{ID: "x", Type: "image", Asset: asset}}}
		proven := map[string]struct{}{asset: {}}
		if err := validateMaterializedPlanAssets(root, plan, proven); err == nil {
			t.Fatalf("unsafe asset %q accepted", asset)
		}
	}
}
