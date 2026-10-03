package overlay

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// entityCardPlan builds a semantic plan with one entity_card item carrying an
// image and a preserved caption, plus optional caption motion override and
// frame treatment.
func entityCardPlan(t *testing.T, caption, captionMotion string, frame bool) []Layer {
	t.Helper()
	item := map[string]any{
		"id":              "portrait",
		"entity_id":       "person:ada-lovelace",
		"kind":            "entity_card",
		"template_id":     "PERSON",
		"preset_id":       "phrase_default",
		"text":            caption,
		"image_preset_id": "image_scale_in",
		"entity_caption":  caption,
		"start_ms":        500, "end_ms": 2500, "duration_ms": 2000,
		"asset_refs": []any{map[string]any{
			"asset_id":   "portrait-asset",
			"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url":        "https://store.example/portrait.png",
			"media_type": "image/png",
		}},
	}
	if captionMotion != "" {
		item["caption_motion_id"] = captionMotion
	}
	if frame {
		item["frame"] = map[string]any{"border": map[string]any{
			"width_px": 4.0, "color": "#FFFFFF", "radius_px": 24.0,
		}}
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "entity-caption-plan",
		"video_id":       "entity-caption-video",
		"width":          1280, "height": 720,
		"fps_num": 30, "fps_den": 1,
		"items": []any{item},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	return result.Plan.Layers
}

func TestEntityCaptionPreserved(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "", false)
	if len(layers) != 2 {
		t.Fatalf("got %d layers, want image plus caption", len(layers))
	}
	if layers[1].Text != "Ada Lovelace" {
		t.Fatalf("caption text = %q, want preserved entity name", layers[1].Text)
	}
}

func TestEntityImageCaptionSameLifetime(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "", false)
	image, caption := layers[0], layers[1]
	if image.StartFrame != caption.StartFrame || image.DurationFrames != caption.DurationFrames {
		t.Fatalf("lifetimes differ: image=(%d,%d) caption=(%d,%d)",
			image.StartFrame, image.DurationFrames, caption.StartFrame, caption.DurationFrames)
	}
}

func TestEntityCaptionAnchoredBelowImage(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "", false)
	image, caption := layers[0], layers[1]
	// Chronon positions are canvas-center relative: the image's bottom edge
	// is canvasHeight/2 + position_y + size/2.
	imageBottom := 720.0/2 + image.Position[1] + image.Size[1]/2
	captionTop := caption.Position[1] - caption.Size[1]/2
	if captionTop <= imageBottom {
		t.Fatalf("caption top (%v) must sit below image bottom (%v)", captionTop, imageBottom)
	}
	gap := captionTop - imageBottom
	if gap < 10 || gap > 60 {
		t.Fatalf("caption gap %v outside the resolver's margin band", gap)
	}
}

func TestEntityCaptionCenteredOnImage(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "", false)
	image, caption := layers[0], layers[1]
	imageCenterX := 1280.0/2 + image.Position[0]
	captionCenterX := caption.Position[0]
	if math.Abs(captionCenterX-imageCenterX) > 1 {
		t.Fatalf("caption center_x %v ~= image center_x %v", captionCenterX, imageCenterX)
	}
}

func TestEntityCaptionSafeArea(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "", false)
	caption := layers[1]
	if len(caption.Size) != 2 || caption.Size[0] <= 0 {
		t.Fatalf("caption size %v invalid", caption.Size)
	}
	halfWidth := caption.Size[0] / 2
	if caption.Position[0]-halfWidth < 0 || caption.Position[0]+halfWidth > 1280 {
		t.Fatalf("caption box %v..%v leaves the canvas", caption.Position[0]-halfWidth, caption.Position[0]+halfWidth)
	}
}

func TestEntityCaptionLongNameFits(t *testing.T) {
	for _, name := range []string{
		"Pablo Diego José Francisco de Paula Juan Nepomuceno María de los Remedios Cipriano de la Santísima Trinidad Ruiz y Picasso",
		"Alexander Boris de Pfeffel Johnson",
	} {
		layers := entityCardPlan(t, name, "", false)
		caption := layers[1]
		if strings.Join(strings.Fields(caption.Text), " ") != name {
			t.Fatalf("caption text = %q, want full name preserved", caption.Text)
		}
		halfWidth := caption.Size[0] / 2
		if caption.Position[0]-halfWidth < 0 || caption.Position[0]+halfWidth > 1280 {
			t.Fatalf("long-name caption box leaves the canvas: size %v pos %v", caption.Size, caption.Position)
		}
		if caption.Style == nil || caption.Style.FontSize < 12 || caption.Style.FontSize > 30 {
			t.Fatalf("long-name font size %v outside the fitted band", caption.Style.FontSize)
		}
	}
}

func TestEntityCaptionUnicode(t *testing.T) {
	for _, name := range []string{"习近平", "Владимир Путин", "محمد بن سلمان"} {
		layers := entityCardPlan(t, name, "", false)
		caption := layers[1]
		if caption.Text != name {
			t.Fatalf("unicode caption = %q, want %q", caption.Text, name)
		}
		if len(caption.Text) == 0 {
			t.Fatal("unicode caption empty")
		}
	}
}

func TestEntityCaptionMotionCompiles(t *testing.T) {
	for _, motionID := range []string{"text_word_rise", "text_fade_up", "text_scale_punch"} {
		layers := entityCardPlan(t, "Ada Lovelace", motionID, false)
		caption := layers[1]
		if caption.Animation == nil || len(caption.Animation.Tracks) == 0 {
			t.Fatalf("caption motion %q lowered no animation tracks", motionID)
		}
	}
}

func TestEntityCaptionMotionRejectsNonText(t *testing.T) {
	layers := entityCardPlan(t, "Ada Lovelace", "image_slide_left", false)
	caption := layers[1]
	if caption.Animation != nil && len(caption.Animation.Tracks) > 0 {
		// An image motion on a caption must not lower image tracks onto the
		// text layer; the resolver refuses it and the caption stays static
		// (preset-owned) rather than carrying impossible tracks.
		for _, track := range caption.Animation.Tracks {
			if track.Property == "position_z" || track.Property == "rotation_x" || track.Property == "rotation_y" {
				t.Fatalf("caption carried image-motion 3D track %q", track.Property)
			}
		}
	}
}

func TestEntityCardResolverUnit(t *testing.T) {
	// bottom_center anchor with the resolver's margin.
	bounds := EntityCardImageBoundsFromCenter(1280, 720, []float64{0, 0}, 480, 480)
	if bounds.CenterX() != 640 || bounds.Bottom() != 600 {
		t.Fatalf("image bounds = %+v", bounds)
	}
	layout, err := ResolveEntityCardLayout(1280, 720, bounds, "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	if layout.CaptionAnchor.Reference != "bottom_center" || layout.CaptionAnchor.Margin != 28 {
		t.Fatalf("anchor = %+v", layout.CaptionAnchor)
	}
	if layout.CaptionBounds.Top < layout.ImageBounds.Bottom() {
		t.Fatalf("caption top %v above image bottom %v", layout.CaptionBounds.Top, layout.ImageBounds.Bottom())
	}
	if !layout.CaptionPreserved {
		t.Fatal("caption_preserved must be true with a caption")
	}
	// Image-only layout: no caption bounds, still valid.
	empty, err := ResolveEntityCardLayout(1280, 720, bounds, "")
	if err != nil {
		t.Fatal(err)
	}
	if empty.CaptionPreserved {
		t.Fatal("empty caption must report preserved=false")
	}
	// Invalid geometry fails closed.
	if _, err := ResolveEntityCardLayout(0, 720, bounds, "x"); err == nil {
		t.Fatal("zero canvas must fail closed")
	}
	if _, err := ResolveEntityCardLayout(1280, 720, EntityCardImageBounds{Width: 0, Height: 10}, "x"); err == nil {
		t.Fatal("degenerate image must fail closed")
	}
}

func TestEntityCaptionDefaultMotionFromCatalog(t *testing.T) {
	if got := EntityCaptionMotionID(""); got != "text_fade_up" {
		t.Fatalf("default caption motion = %q", got)
	}
	if got := EntityCaptionMotionID("text_word_stagger"); got != "text_word_stagger" {
		t.Fatalf("requested caption motion = %q", got)
	}
}
