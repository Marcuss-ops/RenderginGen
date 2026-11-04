package overlay

// semantic_background_compile.go owns the background plate lowering. The plate
// is top-left anchored and screen-space: a missing position would be read as a
// canvas-center offset and shrink a full-canvas background to one quadrant.
// Extracted from compileSemanticDecoded so the plan stages stay one call each.
import (
	"fmt"
	"strings"
)

// compileBackgroundLayer returns the background plate when the plan declares
// one. Fit is validated against the closed render contract and never silently
// downgraded.
func compileBackgroundLayer(src *semanticPlan, registry *assetRegistry) (Layer, bool, error) {
	bg := src.Background
	if bg == nil {
		return Layer{}, false, nil
	}
	kind := strings.ToLower(strings.TrimSpace(bg.Kind))
	// blur_cover is a clip-render-specific fit hint; store it as "video"
	// type in the layer with the fit preserved.
	layerKind := kind
	if kind == "blur_cover" {
		layerKind = "video"
	}
	if layerKind != "color" && layerKind != "image" && layerKind != "video" {
		return Layer{}, false, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
	}
	if layerKind == "color" {
		if len(bg.Color) != 4 {
			return Layer{}, false, fmt.Errorf("overlay: background color requires RGBA[4]")
		}
	} else if len(bg.AssetRefs) == 0 {
		return Layer{}, false, fmt.Errorf("overlay: %s background requires asset_refs", kind)
	}
	for _, ref := range bg.AssetRefs {
		if _, err := registry.Register(ref); err != nil {
			return Layer{}, false, fmt.Errorf("overlay: background asset: %w", err)
		}
	}
	// Color/image/video background plates are top-left anchored: the
	// renderer measures a missing position as a canvas-center offset,
	// which shrinks a full-canvas plate to the top-left quadrant and
	// leaves the rest of the frame on the black clear color (the
	// standalone entity-image portrait rendered black corners on a
	// declared white canvas for exactly this reason). Keep the plate in
	// screen space as well: when any text layer enables the 2.5D camera,
	// the background must not enter the projected layer/depth bin.
	layer := Layer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
		Size:     []float64{float64(src.Width), float64(src.Height)},
		Position: []float64{0, 0}, ScreenSpace: true,
		Fit: bg.Fit, StartFrame: 0}
	if layerKind != "color" {
		// One gate for the background fit: validated against the closed set
		// the render contract honours, defaulted to the deterministic
		// crop-to-fill, never silently downgraded.
		fit, err := resolveBackgroundFit(bg.Fit)
		if err != nil {
			return Layer{}, false, err
		}
		layer.Fit = fit
	}
	if bg.Opacity != nil {
		// Declared opacity (including an explicit 0) is carried verbatim as
		// a pointer so the wire cannot lose the "invisible" contract.
		opacity := *bg.Opacity
		layer.Opacity = &opacity
	}
	if layerKind == "color" {
		layer.Color = append([]float64(nil), bg.Color...)
	} else {
		ref := bg.AssetRefs[0]
		if layerKind == "video" {
			layer.Source = registry.Path(ref.ID)
		} else {
			layer.Asset = registry.Path(ref.ID)
		}
		layer.Loop = bg.Loop
	}
	return layer, true, nil
}
