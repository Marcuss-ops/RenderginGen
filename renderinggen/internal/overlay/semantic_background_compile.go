package overlay

// semantic_background_compile.go owns the background plate lowering. The plate
// is top-left anchored and screen-space. Position is explicit because the three
// plate kinds do not share one position contract (see canvasBoxPosition):
// image/color values are canvas-centre offsets, while a video value names the
// box top-left in canvas-centred coordinates and the engine adds half the box.
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
	// Every plate is anchored at the canvas top-left in its own contract.
	// Image and color layers take a centre offset, so [0,0] is the full
	// canvas. A video layer takes the box top-left in canvas-centred
	// coordinates and the engine adds half the box, so a full-canvas video
	// needs [-W/2,-H/2]; [0,0] puts its centre at (+W/2,+H/2), i.e. the
	// plate covers only the bottom-right quadrant. Keep the plate in screen
	// space as well: when any text layer enables the 2.5D camera, the
	// background must not enter the projected layer/depth bin.
	position := []float64{0, 0}
	if layerKind == "video" {
		position = canvasBoxPosition("video", 0, 0,
			float64(src.Width), float64(src.Height), src.Width, src.Height)
	}
	layer := Layer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
		Size:     []float64{float64(src.Width), float64(src.Height)},
		Position: position, ScreenSpace: true,
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
