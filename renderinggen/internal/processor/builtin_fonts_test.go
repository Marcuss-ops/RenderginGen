package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// TestMaterializeBuiltinFontsStagesEveryRuntimeFontAsset calls the production
// staging function with EVERY font asset a plan can reference, derived from the
// resolver itself (overlay.RuntimeFontAssets) rather than from a list maintained
// here.
//
// This is the regression guard for the live failure it exists because of: a
// plan asking for `inter` (a published, selectable family) reached the worker,
// which could not stage assets/fonts/Inter.ttf from the bundle and failed the
// render job with "official preset font ... is missing from the editorial_v1
// bundle" — after generation, TTS and audio compile had all been paid for. A
// family the resolver advertises must be stageable by the worker, and now that
// is a test failure instead of a render failure.
func TestMaterializeBuiltinFontsStagesEveryRuntimeFontAsset(t *testing.T) {
	assets := overlay.RuntimeFontAssets()
	if len(assets) < 5 {
		t.Fatalf("runtime font assets = %v, want the whole resolver surface", assets)
	}
	for _, asset := range assets {
		t.Run(asset, func(t *testing.T) {
			root := t.TempDir()
			plan := &overlay.Plan{Layers: []overlay.Layer{{
				ID: "phrase", Type: "text", Text: "Font staging",
				Style: &overlay.LayerStyle{Font: asset},
			}}}
			if err := materializeBuiltinFonts(root, plan); err != nil {
				t.Fatalf("materializeBuiltinFonts(%q) = %v", asset, err)
			}
			staged := filepath.Join(root, filepath.FromSlash(asset))
			info, err := os.Stat(staged)
			if err != nil {
				t.Fatalf("font %q was not staged: %v", asset, err)
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				t.Fatalf("staged font %q is not a regular non-empty file: %v", asset, info.Mode())
			}
		})
	}
}
