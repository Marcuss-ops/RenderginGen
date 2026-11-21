package overlay

import (
	"strings"
	"testing"
)

// TestFitImageLayersToSizesOwnsPostCompileGeometry pins the boundary moved out
// of the processor: overlay turns probed source sizes into final card, caption
// and premium geometry, with no file I/O and no processor-side mutation of
// transient layer flags.
func TestFitImageLayersToSizesOwnsPostCompileGeometry(t *testing.T) {
	plan := &Plan{
		Canvas: Canvas{Width: 1920, Height: 1080},
		Layers: []Layer{
			{
				ID: "portrait:image", Type: "image", Asset: "assets/semantic/portrait.png",
				BoxWidth: 480, BoxHeight: 480, Size: []float64{480, 480}, Fit: FitContain,
				EntityImage: true, Position: []float64{0, 0}, StartFrame: 0, DurationFrames: 120,
			},
			{
				ID: "portrait:caption", Type: "text", Text: "Ada Lovelace",
				Style:             &LayerStyle{Font: "assets/fonts/Inter.ttf", FontSize: 42},
				Position:          []float64{0, 300},
				Size:              []float64{300, 80},
				CaptionForImageID: "portrait:image", CaptionLayout: "below",
				StartFrame: 0, DurationFrames: 120,
			},
		},
	}
	changed, err := FitImageLayersToSizes(plan, map[string]ImageSize{
		"assets/semantic/portrait.png": {Width: 1600, Height: 900},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("fitting a materialized portrait must report a plan change")
	}
	image := plan.Layers[0]
	if image.BoxWidth != 480 || image.BoxHeight != 270 || image.Size[0] != 480 || image.Size[1] != 270 {
		t.Fatalf("fitted box = %dx%d size %v, want 480x270", image.BoxWidth, image.BoxHeight, image.Size)
	}
	if image.EntityImage {
		t.Fatal("transient entity-image marker survived the fit")
	}
	if image.Fit != FitCover {
		t.Fatalf("fitted fit = %q, want cover", image.Fit)
	}
	caption := plan.Layers[1]
	if caption.Style == nil || caption.Style.FitMode != FitModeShrinkOnly {
		t.Fatalf("caption style = %+v, want the pinned shrink_only fit contract", caption.Style)
	}
	if caption.Style.MinFontSize != caption.Style.FontSize || caption.Style.MaxFontSize != caption.Style.FontSize {
		t.Fatalf("caption limits = %v/%v, want both pinned to %v", caption.Style.MinFontSize, caption.Style.MaxFontSize, caption.Style.FontSize)
	}
}

// TestFitImageLayersToSizesDerivesAnAuthoredHeight proves the height-only
// standalone card is derived from the probed aspect inside overlay.
func TestFitImageLayersToSizesDerivesAnAuthoredHeight(t *testing.T) {
	plan := &Plan{Layers: []Layer{{
		ID: "pin:image", Type: "image", Asset: "assets/semantic/photo.png",
		BoxWidth: 700, BoxHeight: 700, Size: []float64{700, 700}, Fit: FitCover,
		DeriveWidthFromAsset: true,
	}}}
	changed, err := FitImageLayersToSizes(plan, map[string]ImageSize{
		"assets/semantic/photo.png": {Width: 1280, Height: 1519},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("deriving a card width must report a plan change")
	}
	layer := plan.Layers[0]
	if layer.BoxWidth != 590 || layer.BoxHeight != 700 || layer.Size[0] != 590 || layer.Size[1] != 700 {
		t.Fatalf("derived geometry = box %dx%d size %v, want 590x700", layer.BoxWidth, layer.BoxHeight, layer.Size)
	}
	if layer.DeriveWidthFromAsset {
		t.Fatal("transient width-derivation marker survived the fit")
	}
}

// TestFitImageLayersToSizesFailsClosedWithoutAProbe makes the probe/layout
// contract explicit: a layer that awaits source geometry without a probed size
// is an error, never a silently guessed box.
func TestFitImageLayersToSizesFailsClosedWithoutAProbe(t *testing.T) {
	plan := &Plan{Layers: []Layer{{
		ID: "portrait:image", Type: "image", Asset: "assets/semantic/missing.png",
		BoxWidth: 480, BoxHeight: 480, Size: []float64{480, 480}, EntityImage: true,
	}}}
	_, err := FitImageLayersToSizes(plan, nil)
	if err == nil {
		t.Fatal("fitting without a probed size was accepted")
	}
	if !strings.Contains(err.Error(), "missing.png") {
		t.Fatalf("error %q does not name the unprobed asset", err)
	}
}
