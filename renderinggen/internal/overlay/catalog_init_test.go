package overlay

import "testing"

// TestEmbeddedCatalogsInitialize exercises the package-level initializers whose
// panic is an initialization invariant. A broken embedded catalog must fail
// here (or at init) rather than produce a partially empty vocabulary.
func TestEmbeddedCatalogsInitialize(t *testing.T) {
	if len(certifiedMapCompositions) == 0 {
		t.Fatal("embedded ChrononPlugin map catalog produced no compositions")
	}
	if len(certifiedMapCameraAnimations) == 0 || len(certifiedMapLabelAnimations) == 0 {
		t.Fatal("embedded ChrononPlugin map catalog produced an empty animation vocabulary")
	}
	if len(geographicTagStylePresets) == 0 {
		t.Fatal("embedded geographic tag style catalog produced no presets")
	}
}
