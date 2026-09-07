package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

func TestValidateMaterializedPlanAssetsFailsBeforeChronon(t *testing.T) {
	root := t.TempDir()
	plan := &overlay.Plan{Layers: []overlay.Layer{{ID: "entity-image", Type: "image", Asset: "assets/semantic/missing.jpg"}}}

	if err := validateMaterializedPlanAssets(root, plan); err == nil {
		t.Fatal("missing plan asset must fail before Chronon")
	}

	path := filepath.Join(root, "assets", "semantic", "missing.jpg")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateMaterializedPlanAssets(root, plan); err != nil {
		t.Fatalf("materialized plan asset rejected: %v", err)
	}
}
