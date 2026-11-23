package overlay

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSanitizePreparedPackageReplacesNonFinite(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	pkg := PreparedPackage{
		PlanID:   "run",
		Language: "en",
		Overlays: []PreparedOverlay{{
			ID:   "short-phrase-web-typewriter-staccato__demo__short_phrase__01",
			Kind: "text",
			Dynamic: DynamicOverlayState{
				Position: []float64{nan, 12},
				Scale:    []float64{inf, 1},
				Opacity:  &nan,
			},
		}},
	}

	sanitizePreparedPackage(&pkg)

	dynamic := pkg.Overlays[0].Dynamic
	if !finiteFloat(dynamic.Position[0]) || !finiteFloat(dynamic.Scale[0]) || !finiteFloat(*dynamic.Opacity) {
		t.Fatalf("finite fields not sanitized: position=%v scale=%v opacity=%v", dynamic.Position, dynamic.Scale, *dynamic.Opacity)
	}
	if dynamic.Position[1] != 12 || dynamic.Scale[1] != 1 || *dynamic.Opacity != 0 {
		t.Fatalf("sanitizer changed finite values: position=%v scale=%v opacity=%v", dynamic.Position, dynamic.Scale, *dynamic.Opacity)
	}
	if _, err := json.Marshal(pkg); err != nil {
		t.Fatalf("sanitized package must marshal: %v", err)
	}
}
