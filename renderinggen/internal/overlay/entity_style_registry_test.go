package overlay

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
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
// vulkan_native. A failed strict-GPU motion must remain named in the plan
// until the runtime itself supports it; the compiler must not swap recipes.
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
// entity image keeps image_card_flip_soft's camera-backed tracks with enable_3d
// derived from them, and the explicit caption motion stays on its layer
// instead of collapsing to text_fade_up. A failed certification snapshot does
// not replace the selected motion ID.
func TestRuntimeEntityImageKeepsExplicit3DMotions(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-2d","video_id":"runtime-2d","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"portrait","entity_id":"person:chiara-poggi","kind":"entity_image","template_id":"image_popup","preset_id":"image_slide_right","motion_id":"image_card_flip_soft","entity_caption":"Chiara Poggi","caption_motion_id":"trump_entity_text_15","start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"chiara","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/chiara.jpg","media_type":"image/jpeg"}]}]}`)
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile runtime entity image with explicit 3D motions: %v", err)
	}
	if len(compiled.Plan.Layers) < 2 {
		t.Fatalf("runtime entity produced %d layers, want image and caption", len(compiled.Plan.Layers))
	}
	image, caption := compiled.Plan.Layers[0], compiled.Plan.Layers[len(compiled.Plan.Layers)-1]
	if image.ID != "portrait:image" || image.Animation == nil {
		t.Fatalf("entity image layer = %+v, want portrait:image with tracks", image)
	}
	if !layerUses3D(image.Animation) || !image.Enable3D {
		t.Fatalf("explicit motion image_card_flip_soft did not reach the entity image as 3D tracks with enable_3d: tracks=%+v enable3d=%v", image.Animation.Tracks, image.Enable3D)
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

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}

func TestEntityCaptionMotionsAreCatalogBackedAndVariedByIdentity(t *testing.T) {
	var want []string
	for _, category := range []string{"entity_caption_v1", "trump_entity_text_v1", "typewriter", "typewriter_glitch", "typewriter_modern_v1", "entity_card_v1"} {
		want = append(want, motion.Registry.SelectableCategoryMotionIDs(category)...)
	}
	filtered := want[:0]
	for _, id := range want {
		definition, err := resolveMotionDefinition(id)
		if err == nil && motionDefinitionAdmitsTarget(definition, "caption") && definitionHasCaptionAnimation(definition) {
			filtered = append(filtered, id)
		}
	}
	want = filtered
	sort.Strings(want)
	want = compactStrings(want)
	pool := append([]string(nil), entityCaptionMotionPool...)
	sort.Strings(pool)
	pool = compactStrings(pool)
	if !reflect.DeepEqual(pool, want) {
		t.Fatalf("runtime entity-caption pool differs from selectable catalog: got %v want %v", pool, want)
	}
	if len(pool) < 20 {
		t.Fatalf("entity caption pool has %d motions, want broad catalog coverage", len(pool))
	}
	choose := func(planID, videoID, itemID string) string {
		return entityCaptionMotionIDForIdentity("", planID, videoID, itemID)
	}
	first := choose("plan", "video-a", "entity-1")
	if first == "" || choose("plan", "video-a", "entity-1") != first {
		t.Fatalf("caption selection for stable identity was empty or nondeterministic: %q", first)
	}
	seen := map[string]bool{first: true}
	for i := 2; i < 48 && len(seen) < 2; i++ {
		seen[choose("plan", "video-a", fmt.Sprintf("entity-%d", i))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("caption motion did not vary across entity identities: %v", seen)
	}
	if other := choose("plan", "video-b", "entity-1"); other == first {
		t.Fatalf("caption selection did not vary across videos: both selected %q", first)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			id := choose("plan", "parallel-video", fmt.Sprintf("entity-%d", index))
			if !containsString(pool, id) {
				t.Errorf("concurrent caption selector returned non-catalog motion %q", id)
			}
		}(i)
	}
	wg.Wait()
}

func TestCaptionMotionPickerKeepsGPUBlurTracksSelectable(t *testing.T) {
	definition, err := resolveMotionDefinition("trump_entity_text_04")
	if err != nil {
		t.Fatalf("resolve blur caption motion: %v", err)
	}
	if len(definition.Tracks) == 0 || definition.Tracks[0].Property != "blur" {
		t.Fatalf("test motion no longer exercises a normal blur track: %+v", definition.Tracks)
	}
	if !entityCaptionMotionSelectable(definition.ID) {
		t.Fatalf("catalog caption motion %q with a blur track was excluded from runtime selection", definition.ID)
	}
}

func TestDefaultImageMotionCatalogCoversSingleImageMotionFamilies(t *testing.T) {
	var want []string
	for _, family := range []string{"overlay_v3_image", "image_25d_clean_v1", "editorial_image_v1"} {
		want = append(want, motion.Registry.SelectableCategoryMotionIDs(family)...)
	}
	sort.Strings(want)
	want = compactStrings(want)
	if !reflect.DeepEqual(runtimeDefaultImageMotionIDs, want) {
		t.Fatalf("runtime default image inventory differs from the selectable single-image catalog: got %v want %v", runtimeDefaultImageMotionIDs, want)
	}
	for _, excluded := range []string{"image_corner_bloom", "image_stack_focus", "image_caption_frame_combo", "brush_arrow_point", "map_image_australia_sunset_drift"} {
		if containsString(runtimeDefaultImageMotionIDs, excluded) {
			t.Errorf("specialized/uncertified recipe %q leaked into the automatic single-image pool", excluded)
		}
	}
}

func TestRuntimeDefaultImageMotionVariesByVideoAndItem(t *testing.T) {
	pool := []string{"image_a", "image_b", "image_c", "image_d", "image_e", "image_f", "image_g", "image_h"}
	plan := &semanticPlan{PlanID: "plan", VideoID: "video-a"}
	first := runtimeDefaultImageMotionIDFor(pool, plan, "image-1")
	if got := runtimeDefaultImageMotionIDFor(pool, plan, "image-1"); got != first {
		t.Fatalf("same image identity selected %q then %q; selection must be reproducible", first, got)
	}
	seen := map[string]bool{first: true}
	for index := 2; index <= 32 && len(seen) < 2; index++ {
		seen[runtimeDefaultImageMotionIDFor(pool, plan, fmt.Sprintf("image-%d", index))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("different images in the same video never varied their default motion: %v", seen)
	}
	otherVideo := &semanticPlan{PlanID: "plan", VideoID: "video-b"}
	if got := runtimeDefaultImageMotionIDFor(pool, otherVideo, "image-1"); got == first {
		t.Fatalf("same image identity in distinct videos both selected %q", first)
	}
}

func TestUnspecifiedSingleImageMotionUsesRuntimePoolAndExplicitMotionWins(t *testing.T) {
	makePlan := func(planID, videoID, itemID, explicit string) []byte {
		document := map[string]any{
			"schema_version": SemanticSchema, "plan_id": planID, "video_id": videoID,
			"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
			"items": []any{map[string]any{
				"id": itemID, "kind": "image", "template_id": "IMAGE_OVERLAY", "preset_id": "image_focus_in",
				"motion_id": explicit, "start_ms": 0, "end_ms": 3000,
				"asset_refs": []any{map[string]any{"asset_id": "photo", "sha256": strings.Repeat("a", 64), "url": "https://example.test/photo.jpg", "media_type": "image/jpeg"}},
			}},
		}
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	compileMotion := func(raw []byte) (string, Layer) {
		t.Helper()
		compiled, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("compile image: %v", err)
		}
		if len(compiled.Plan.Layers) != 1 {
			t.Fatalf("compiled %d layers, want one image: %+v", len(compiled.Plan.Layers), compiled.Plan.Layers)
		}
		layer := compiled.Plan.Layers[0]
		if layer.Type != "image" || layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Fatalf("image has no lowered animation tracks: %+v", layer)
		}
		for _, id := range runtimeDefaultImageMotionIDs {
			want, err := animationForMotion(id, nil, "", layer.DurationFrames, imagePresetExitForTest(t, "image_focus_in"))
			if err == nil && reflect.DeepEqual(layer.Animation.Tracks, want.Tracks) {
				return id, layer
			}
		}
		t.Fatalf("lowered image tracks do not match any automatic catalog motion: %+v", layer.Animation.Tracks)
		return "", Layer{}
	}
	id1, _ := compileMotion(makePlan("plan", "video-a", "image-1", ""))
	id1Again, _ := compileMotion(makePlan("plan", "video-a", "image-1", ""))
	if id1Again != id1 {
		t.Fatalf("same image identity selected %q then %q", id1, id1Again)
	}
	seen := map[string]bool{id1: true}
	for index := 2; index <= 32 && len(seen) < 2; index++ {
		id, _ := compileMotion(makePlan("plan", "video-a", fmt.Sprintf("image-%d", index), ""))
		seen[id] = true
	}
	if len(seen) < 2 {
		t.Fatalf("different images in one video all reused motion %q", id1)
	}
	id2, _ := compileMotion(makePlan("plan", "video-b", "image-1", ""))
	if id2 == id1 {
		t.Fatalf("same image in different videos reused motion %q", id1)
	}
	explicit, _ := compileMotion(makePlan("plan", "video-a", "image-1", "image_focus_reveal"))
	if explicit != "image_focus_reveal" {
		t.Fatalf("explicit motion_id was replaced with %q", explicit)
	}
}
