package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// backgroundFixtureSHA is a syntactically valid content address for background
// fixtures (the registry requires 64 hex chars).
const backgroundFixtureSHA = "1111111111111111111111111111111111111111111111111111111111111111"

// backgroundOnlyPlanJSON builds the smallest semantic plan that carries ONLY a
// background layer, so a test can assert the lowered layer in isolation.
func backgroundOnlyPlanJSON(background string) string {
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"bg","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"duration_ms":4000,"background":%s,"items":[]}`, background)
}

func backgroundAssetJSON(kind, url, mediaType string) string {
	return fmt.Sprintf(
		`{"kind":%q,"asset_refs":[{"asset_id":"bg-plate","sha256":%q,"url":%q,"media_type":%q}]}`,
		kind, backgroundFixtureSHA, url, mediaType)
}

// TestBackgroundImageLowersToImageLayer is the Goal-3 lowering contract: an
// image background becomes an IMAGE layer (not a video layer with an image
// file behind it), and it keeps the canvas geometry and the declared fit.
// Before the background kind existed on the wire, every asset background was
// lowered as a video layer regardless of the bytes.
func TestBackgroundImageLowersToImageLayer(t *testing.T) {
	for _, url := range []string{
		"assets/semantic/bg-plate/background.png",
		"assets/semantic/bg-plate/background.jpg",
		"https://store.example/plate.webp",
	} {
		t.Run(url, func(t *testing.T) {
			raw := backgroundOnlyPlanJSON(backgroundAssetJSON("image", url, "image/png"))
			result, err := CompileSemantic([]byte(raw))
			if err != nil {
				t.Fatalf("compile image background: %v", err)
			}
			plan := result.Plan
			if len(plan.Layers) != 1 {
				t.Fatalf("layers = %d, want 1 (background only)", len(plan.Layers))
			}
			layer := plan.Layers[0]
			if layer.ID != "background" || layer.Type != "image" {
				t.Fatalf("layer = %+v, want an image layer named background", layer)
			}
			if layer.Fit != "cover" {
				t.Errorf("fit = %q, want the deterministic cover default", layer.Fit)
			}
			if layer.Asset == "" {
				t.Error("image layer carries no asset path")
			}
			if layer.Source != "" {
				t.Errorf("an image plate must not be lowered as a video source (source=%q)", layer.Source)
			}
			if layer.Size[0] != 1920 || layer.Size[1] != 1080 {
				t.Errorf("size = %v, want the canvas geometry", layer.Size)
			}
		})
	}
}

// TestBackgroundVideoLowersToVideoLayer is the sibling contract: a video plate
// keeps flowing through the video layer, loop-capable, so the two families are
// never confused by the lowering.
func TestBackgroundVideoLowersToVideoLayer(t *testing.T) {
	raw := backgroundOnlyPlanJSON(backgroundAssetJSON("video", "assets/semantic/bg-plate/background.mp4", "video/mp4"))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile video background: %v", err)
	}
	layer := result.Plan.Layers[0]
	if layer.Type != "video" || layer.Source == "" {
		t.Fatalf("layer = %+v, want a video layer with a source", layer)
	}
}

// TestBackgroundFitIsValidatedFailClosed is the "deterministic fit" gate. The
// plan decoder maps every unknown fit to cover, so an unsupported value used to
// reach the renderer as a silent visual substitution (the historical
// "blur_cover" background hint did exactly that). The compiler now owns the
// closed set and refuses anything outside it.
func TestBackgroundFitIsValidatedFailClosed(t *testing.T) {
	for _, fit := range []string{"cover", "contain", "stretch", "none"} {
		t.Run("accepted_"+fit, func(t *testing.T) {
			raw := backgroundOnlyPlanJSON(fmt.Sprintf(
				`{"kind":"image","fit":%q,"asset_refs":[{"asset_id":"bg-plate","sha256":%q,"url":"assets/semantic/bg-plate/background.png","media_type":"image/png"}]}`,
				fit, backgroundFixtureSHA))
			result, err := CompileSemantic([]byte(raw))
			if err != nil {
				t.Fatalf("fit %q must be accepted: %v", fit, err)
			}
			if got := result.Plan.Layers[0].Fit; got != fit {
				t.Errorf("fit = %q, want %q (requested fits must not be rewritten)", got, fit)
			}
		})
	}

	for _, fit := range []string{"blur_cover", "fill", "COVER_AND_CROP", "auto", "fit_width"} {
		t.Run("rejected_"+fit, func(t *testing.T) {
			raw := backgroundOnlyPlanJSON(fmt.Sprintf(
				`{"kind":"image","fit":%q,"asset_refs":[{"asset_id":"bg-plate","sha256":%q,"url":"assets/semantic/bg-plate/background.png","media_type":"image/png"}]}`,
				fit, backgroundFixtureSHA))
			_, err := CompileSemantic([]byte(raw))
			if err == nil {
				t.Fatalf("fit %q must fail closed instead of being silently downgraded", fit)
			}
			if !strings.Contains(err.Error(), "unsupported background fit") {
				t.Fatalf("error must name the unsupported fit, got %v", err)
			}
		})
	}
}

// TestBackgroundFitNormalizesSurroundingWhitespace pins the only leniency the
// gate allows: surrounding whitespace is normalized to the canonical value, so
// the fit that reaches the renderer is always one of the closed set (never a
// near miss the decoder would silently turn into cover).
func TestBackgroundFitNormalizesSurroundingWhitespace(t *testing.T) {
	raw := backgroundOnlyPlanJSON(fmt.Sprintf(
		`{"kind":"image","fit":"  cover  ","asset_refs":[{"asset_id":"bg-plate","sha256":%q,"url":"assets/semantic/bg-plate/background.png","media_type":"image/png"}]}`,
		backgroundFixtureSHA))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := result.Plan.Layers[0].Fit; got != "cover" {
		t.Fatalf("fit = %q, want the normalized canonical value", got)
	}
}

// TestBackgroundFitDefaultsToCover pins the default: a background that declares
// no fit is crop-to-fill, deterministically, for both families.
func TestBackgroundFitDefaultsToCover(t *testing.T) {
	raw := backgroundOnlyPlanJSON(backgroundAssetJSON("image", "assets/semantic/bg-plate/background.png", "image/png"))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := result.Plan.Layers[0].Fit; got != "cover" {
		t.Fatalf("fit = %q, want cover", got)
	}
}

// TestBackgroundColorKeepsNoFitContract guards the third branch: a color
// background owns no plate, so the fit gate must not rewrite (or reject) it.
func TestBackgroundColorKeepsNoFitContract(t *testing.T) {
	raw := backgroundOnlyPlanJSON(fmt.Sprintf(`{"kind":"color","color":%s}`, certificationBackgroundRGBA))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile color background: %v", err)
	}
	if got := result.Plan.Layers[0].Fit; got != "" {
		t.Fatalf("color background fit = %q, want empty (no plate to fit)", got)
	}
}
