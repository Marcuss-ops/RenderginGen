// This file is an EXTERNAL test package on purpose: the version package
// derives its advertised schema integer from this package's contract, so an
// in-package test importing version would be an import cycle. Only exported
// symbols are needed, so the external package is the right tool.
package overlay_test

import (
	"strconv"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/version"
)

// TestAdvertisedOverlaySchemaDerivesFromContract pins the identity fact the
// worker publishes: the integer on /health and in the worker registry must be
// derivable from the semantic schema string it enforces. It drifted before
// (advertised 3, enforced v1), which made the advertised value meaningless to
// any consumer trying to decide compatibility.
func TestAdvertisedOverlaySchemaDerivesFromContract(t *testing.T) {
	want := "renderinggen.overlay-plan.v" + strconv.Itoa(overlay.SemanticSchemaVersion)
	if overlay.SemanticSchema != want {
		t.Fatalf("SemanticSchema = %q, want %q (derived from SemanticSchemaVersion=%d)",
			overlay.SemanticSchema, want, overlay.SemanticSchemaVersion)
	}
	if version.OverlaySchema != overlay.SemanticSchemaVersion {
		t.Fatalf("version.OverlaySchema = %d, want %d (the enforced contract version)",
			version.OverlaySchema, overlay.SemanticSchemaVersion)
	}
}
