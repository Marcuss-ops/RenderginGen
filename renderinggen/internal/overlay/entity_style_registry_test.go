package overlay

import (
	"encoding/json"
	"testing"
)

// The registry replaces the removed index groups: every composition class must
// be reachable as a derived-tag query with the historic membership counts.
func TestEntityStyleRegistryTagQueries(t *testing.T) {
	cases := map[string]int{
		"below":      20,
		"badge":      5,
		"camera":     5,
		"side":       5,
		"typewriter": 5,
	}
	for tag, want := range cases {
		got := entityStyleRegistry.query(entityStyleQuery{tag})
		if len(got) != want {
			t.Fatalf("tag %q matched %d styles, want %d", tag, len(got), want)
		}
		for _, idx := range got {
			style := premiumEntityStyles[idx]
			switch tag {
			case "below":
				if style.CaptionLayout != "below" && style.ImageSide != "center" {
					t.Fatalf("tag below matched style %q with layout %q side %q", style.ID, style.CaptionLayout, style.ImageSide)
				}
			case "badge":
				if !style.IsBadge {
					t.Fatalf("tag badge matched non-badge style %q", style.ID)
				}
			case "camera":
				if style.CameraMotionID == "" {
					t.Fatalf("tag camera matched style %q without camera motion", style.ID)
				}
			case "side":
				if style.CaptionLayout != "left" && style.CaptionLayout != "right" {
					t.Fatalf("tag side matched style %q with caption layout %q", style.ID, style.CaptionLayout)
				}
			case "typewriter":
				id := style.CaptionMotionID
				if len(id) < len(typewriterCaptionPrefix) || id[:len(typewriterCaptionPrefix)] != typewriterCaptionPrefix {
					t.Fatalf("tag typewriter matched style %q with caption motion %q", style.ID, id)
				}
			}
		}
	}
	if got := entityStyleRegistry.query(nil); len(got) != len(premiumEntityStyles) {
		t.Fatalf("empty query matched %d styles, want %d", len(got), len(premiumEntityStyles))
	}
	if got := entityStyleRegistry.query(entityStyleQuery{"side", "camera"}); len(got) != 0 {
		t.Fatalf("impossible side+camera query matched %v, want no styles", got)
	}
	if got := entityStyleRegistry.query(entityStyleQuery{"unknown-tag"}); len(got) != 0 {
		t.Fatalf("unknown tag query matched %v, want no styles", got)
	}
}

// Tag selectors and their _random aliases are one registry query with one
// sampling salt, and sampling is deterministic for the identity tuple.
func TestEntityStyleRegistrySelectorsSampleDeterministically(t *testing.T) {
	planID, videoID, itemID := "plan-reg", "video-reg", "item-reg"
	for _, selector := range []string{"", "random", "premium_random_v1", "testo_sotto", "below", "premium_below_random_v1", "caption_below", "badge", "badge_random", "camera", "camera_random", "side", "side_random", "typewriter", "typewriter_random"} {
		first, ok := ResolveEntityStyle(selector, planID, videoID, itemID)
		if !ok {
			t.Fatalf("selector %q did not resolve", selector)
		}
		second, _ := ResolveEntityStyle(selector, planID, videoID, itemID)
		if first.ID != second.ID {
			t.Fatalf("selector %q is not deterministic: %q then %q", selector, first.ID, second.ID)
		}
	}
	if alias, _ := ResolveEntityStyle("badge_random", planID, videoID, itemID); alias.ID == "" {
		t.Fatal("badge_random resolved empty")
	}
	base, _ := ResolveEntityStyle("badge", planID, videoID, itemID)
	alias, _ := ResolveEntityStyle("badge_random", planID, videoID, itemID)
	if base.ID != alias.ID {
		t.Fatalf("badge aliases sample differently: %q vs %q", base.ID, alias.ID)
	}
	below, _ := ResolveEntityStyle("testo_sotto", planID, videoID, itemID)
	if below.CaptionLayout != "below" && below.ImageSide != "center" {
		t.Fatalf("testo_sotto sampled %q outside the below class", below.ID)
	}
}

// Entity styles hand the catalog's own motions to Chronon: the scene camera
// stays opt-in (only the camera selectors carry one) and no image/caption
// motion is rewritten on the way out. enable_3d must agree with the tracks
// that actually reached the layer. The strict GPU lane executes the 2.5D
// image family — this was verified by rendering such a card on
// vulkan_native; recorded strict-GPU failures are swapped by
// entityCardRuntimeImageMotion instead of a blanket 2D downgrade.
func TestRuntimeEntityStyleSelectorsKeepCatalogMotions(t *testing.T) {
	for _, selector := range []string{"", "random", "premium_random_v1", "testo_sotto", "below", "side", "badge", "typewriter"} {
		style, ok := ResolveEntityStyle(selector, "garlasco-plan", "garlasco-video", "chiara-poggi")
		if !ok {
			t.Fatalf("runtime selector %q did not resolve", selector)
		}
		if style.CameraMotionID != "" {
			t.Fatalf("runtime selector %q hijacked the scene camera: %+v", selector, style)
		}
		catalog, ok := entityStyleByID(style.ID)
		if !ok {
			t.Fatalf("selector %q sampled unknown style %q", selector, style.ID)
		}
		if style.ImageMotionID != catalog.ImageMotionID || style.CaptionMotionID != catalog.CaptionMotionID {
			t.Fatalf("selector %q rewrote style motions: image %q -> %q, caption %q -> %q",
				selector, catalog.ImageMotionID, style.ImageMotionID, catalog.CaptionMotionID, style.CaptionMotionID)
		}
	}

	item := map[string]any{
		"id": "runtime-portrait", "entity_id": "person:chiara-poggi", "kind": "entity_card", "template_id": "PERSON",
		"preset_id": "phrase_default", "image_preset_id": "image_scale_in",
		"entity_caption": "Chiara Poggi", "entity_style_id": "random",
		"text": "Chiara Poggi", "start_ms": 0, "end_ms": 5000, "duration_ms": 5000,
		"asset_refs": []any{map[string]any{
			"asset_id": "chiara", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url": "https://example.test/chiara.jpg", "media_type": "image/jpeg",
		}},
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": SemanticSchema, "plan_id": "garlasco-runtime-style",
		"video_id": "garlasco-runtime-style", "width": 1920, "height": 1080,
		"fps_num": 24, "fps_den": 1, "items": []any{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile runtime-selected entity style: %v", err)
	}
	if len(compiled.Plan.Layers) < 2 {
		t.Fatalf("runtime entity style produced %d layers, want image and caption", len(compiled.Plan.Layers))
	}
	for _, layer := range compiled.Plan.Layers {
		if layer.Animation != nil && layerUses3D(layer.Animation) && !layer.Enable3D {
			t.Errorf("runtime entity layer %q lowers 3D tracks without enable_3d: %+v", layer.ID, layer.Animation)
		}
		if layer.Enable3D && layer.Animation != nil && !layerUses3D(layer.Animation) && len(layer.TextAnimators) == 0 {
			t.Errorf("runtime entity layer %q enables the 3D path without 3D tracks: %+v", layer.ID, layer.Animation)
		}
	}
}

// A producer-named motion reaches the renderer exactly as declared: the
// entity image keeps image_tilt_settle's camera-backed tracks with enable_3d
// derived from them, and the explicit caption motion stays on its layer
// instead of collapsing to text_fade_up. Only a motion the recorded
// strict-GPU run failed would be swapped by entityCardRuntimeImageMotion.
func TestRuntimeEntityImageKeepsExplicit3DMotions(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-2d","video_id":"runtime-2d","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"portrait","entity_id":"person:chiara-poggi","kind":"entity_image","template_id":"image_popup","preset_id":"image_slide_right","motion_id":"image_tilt_settle","entity_caption":"Chiara Poggi","caption_motion_id":"trump_entity_text_15","start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"chiara","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/chiara.jpg","media_type":"image/jpeg"}]}]}`)
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile runtime entity image with explicit 3D motions: %v", err)
	}
	if len(compiled.Plan.Layers) != 2 {
		t.Fatalf("runtime entity produced %d layers, want image and caption", len(compiled.Plan.Layers))
	}
	image, caption := compiled.Plan.Layers[0], compiled.Plan.Layers[1]
	if image.ID != "portrait:image" || image.Animation == nil {
		t.Fatalf("entity image layer = %+v, want portrait:image with tracks", image)
	}
	if !layerUses3D(image.Animation) || !image.Enable3D {
		t.Fatalf("explicit motion image_tilt_settle did not reach the entity image as 3D tracks with enable_3d: tracks=%+v enable3d=%v", image.Animation.Tracks, image.Enable3D)
	}
	if caption.ID == image.ID {
		t.Fatalf("caption layer collapsed onto the image layer: %+v", caption)
	}
	if caption.TextAnimators == nil && caption.Animation == nil {
		t.Fatalf("explicit caption motion trump_entity_text_15 reached no animation: %+v", caption)
	}
}

// Every catalog style must remain individually resolvable through the same
// registry path the producers use.
func TestEntityStyleRegistryLookupCoversAllStyles(t *testing.T) {
	for _, style := range premiumEntityStyles {
		got, ok := ResolveEntityStyle(style.ID, "p", "v", "i")
		if !ok {
			t.Fatalf("style %q no longer resolves", style.ID)
		}
		want := applyBadgeRandomizationForTest(style, "p", "v", "i")
		if got.BadgeColor != want.BadgeColor || got.CaptionColor != want.CaptionColor {
			t.Fatalf("style %q badge colors drifted: %+v vs %+v", style.ID, got, want)
		}
	}
	for alias, wantID := range entityStyleAliases {
		got, ok := ResolveEntityStyle(alias, "p", "v", "i")
		if !ok || got.ID != wantID {
			t.Fatalf("alias %q resolved to %+v, %v; want style %q", alias, got, ok, wantID)
		}
	}
	if _, ok := ResolveEntityStyle("01", "p", "v", "i"); !ok {
		t.Fatal("legacy reference lookup lost")
	}
	if _, ok := ResolveEntityStyle("1", "p", "v", "i"); !ok {
		t.Fatal("unpadded legacy reference lookup lost")
	}
}

func applyBadgeRandomizationForTest(style entityStyleVariant, planID, videoID, itemID string) entityStyleVariant {
	applyBadgeRuntimeRandomization(&style, style.ID, planID, videoID, itemID)
	return style
}
