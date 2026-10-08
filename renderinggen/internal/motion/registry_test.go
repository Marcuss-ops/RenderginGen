package motion

import (
	"fmt"
	"sort"
	"testing"
)

// The renderer's closed vocabularies, mirrored from
// chronon.render-plan.v3 ($defs/text_selector and animator properties).
// A definition written outside one of these sets compiles fine here and is then
// rejected by Chronon at render time, after the batch has already been paid for.
var (
	selectorUnits   = map[string]bool{"glyph": true, "grapheme": true, "character": true, "word": true, "line": true}
	selectorShapes  = map[string]bool{"square": true, "ramp_up": true, "ramp_down": true, "triangle": true, "round": true, "smooth": true}
	selectorOrders  = map[string]bool{"forward": true, "reverse": true, "from_center": true, "to_center": true, "random": true}
	layerProperties = map[string]bool{
		"position": true, "position_x": true, "position_y": true, "position_z": true,
		"scale": true, "scale_x": true, "scale_y": true, "scale_z": true,
		"rotation": true, "rotation_x": true, "rotation_y": true, "rotation_z": true,
		"opacity": true, "blur": true,
	}
	// Text animators also support tracking; blur is shared with image/layer
	// tracks so image entrances can resolve from blurred to sharp.
	animatorProperties = map[string]bool{
		"position": true, "position_x": true, "position_y": true,
		"scale": true, "scale_x": true, "scale_y": true,
		"opacity": true, "blur": true, "tracking": true,
		"fill_color": true,
	}
)

func TestCatalogCategoriesKeepMotionsIndependentAndComplete(t *testing.T) {
	// This registry exposes authored categories directly. Semantic groupings
	// such as modern_apple belong to the runtime catalog, not a second alias map.
	categoryCounts := map[string]int{
		"typewriter": 10, "typewriter_modern_v1": 15, "apple_v2": 42,
		"apple_v3": 16, "phrase_apple_clean_v1": 30, "apple_phrase_v1": 15,
		"brush_v1": 23, "text_3d_v1": 10, "trump_entity_text_v1": 15, "web": 14,
	}
	for category, count := range categoryCounts {
		if ids := Registry.CategoryMotionIDs(category); len(ids) != count {
			t.Errorf("%s category has %d motions, want %d", category, len(ids), count)
		}
	}
	if ids := Registry.ShortPhraseStyleIDs(); len(ids) != 48 {
		t.Errorf("short phrase catalog has %d motions, want 48", len(ids))
	}
	if ids := Registry.PhraseAnimationIDs(); len(ids) != 146 {
		t.Errorf("phrase planning pool has %d motions, want 146", len(ids))
	}
	for _, property := range []string{"rotation_z", "scale_z"} {
		if motionHas3D(MotionDefinition{Tracks: []TrackDefinition{{Property: property}}}) {
			t.Errorf("2D property %q was classified as camera-backed 3D", property)
		}
	}
	imageIDs := Registry.ImageOverlayMotionIDs()
	if len(imageIDs) != 18 {
		t.Fatalf("image motion inventory has %d entries, want 18: %v", len(imageIDs), imageIDs)
	}
	for category := range categoryCounts {
		for _, id := range Registry.CategoryMotionIDs(category) {
			if _, err := Registry.Resolve(id); err != nil {
				t.Errorf("%s motion %q does not resolve: %v", category, id, err)
			}
		}
	}
}

func TestWebFamilyHasTwelveRenderSafeDistinctCatalogMotions(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatal(err)
	}
	definitions := make(map[string]MotionDefinition, len(catalog.Motions))
	for _, definition := range catalog.Motions {
		definitions[definition.ID] = definition
	}
	ids := Registry.CategoryMotionIDs("web")
	// Editorial Visual Motion V1 grows the certified web vocabulary to 14.
	if len(ids) != 14 {
		t.Fatalf("web motion count = %d, want 14: %v", len(ids), ids)
	}
	seenTracks := make(map[string]bool)
	for _, id := range ids {
		definition, exists := definitions[id]
		if !exists {
			t.Fatalf("web motion %q is absent from canonical catalog", id)
		}
		if definition.Category != "web" || definition.RenderSafe == nil || !*definition.RenderSafe {
			t.Errorf("web motion %q has incomplete render-safety metadata: %+v", id, definition)
		}
		properties := make(map[string]bool)
		var fingerprint string
		for _, track := range definition.Tracks {
			properties[track.Property] = true
			fingerprint += track.Property + ":"
			for _, keyframe := range track.Keyframes {
				fingerprint += fmt.Sprint(keyframe.Frame, "=", keyframe.Value, ";")
			}
		}
		for _, required := range definition.RequiredProperties {
			if !properties[required] {
				t.Errorf("web motion %q lacks required track %q", id, required)
			}
		}
		if fingerprint == "" || seenTracks[fingerprint] {
			t.Errorf("web motion %q duplicates another motion's track recipe", id)
		}
		seenTracks[fingerprint] = true
	}
}

func TestImage3DFamilyHasEightCameraBackedMotionsWithRestingFinalPose(t *testing.T) {
	ids := Registry.CategoryMotionIDs("image_25d_clean_v1")
	if len(ids) != 8 {
		t.Fatalf("image 3D family has %d motions, want 8: %v", len(ids), ids)
	}
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %q: %v", id, err)
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			t.Fatalf("%q is not catalog-backed", id)
		}
		definition := declarative.Definition
		cameraBacked := false
		for _, track := range definition.Tracks {
			if IsCameraBacked3DProperty(track.Property) {
				cameraBacked = true
			}
			if len(track.Keyframes) < 2 {
				t.Fatalf("%q track %q has no complete entrance/rest pair", id, track.Property)
			}
			last := track.Keyframes[len(track.Keyframes)-1]
			value, ok := numericMotionValue(last.Value)
			if !ok {
				t.Fatalf("%q track %q final value %T is not numeric", id, track.Property, last.Value)
			}
			want := 0.0
			switch track.Property {
			case "scale", "scale_x", "scale_y", "scale_z", "opacity":
				want = 1
			case "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z", "blur":
			default:
				t.Fatalf("%q has an unreviewed image 3D track property %q", id, track.Property)
			}
			if value != want {
				t.Errorf("%q track %q final pose = %v, want %v", id, track.Property, value, want)
			}
		}
		if !cameraBacked {
			t.Errorf("%q does not require Chronon's camera-backed 3D path", id)
		}
	}
}

func numericMotionValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func TestApplePhrasePackIsInTheCanonicalPoolAndHasNoPerGlyphBlur(t *testing.T) {
	want := []string{
		"apple_focus_rise", "apple_soft_scale", "apple_word_cascade", "apple_line_cascade",
		"apple_precision_type", "apple_tracking_reveal", "apple_expand_from_center",
		"apple_compress_in", "apple_scale_push", "apple_scale_settle", "apple_word_pulse",
		"apple_vertical_glyph_lift", "apple_line_sweep", "apple_hero_statement", "apple_cinematic_exit",
	}
	got := Registry.CategoryMotionIDs("apple_phrase_v1")
	if len(got) != len(want) {
		t.Fatalf("Apple phrase pack has %d motions, want %d: %v", len(got), len(want), got)
	}
	pool := make(map[string]bool)
	phrasePool := PhraseMotionPool()
	if len(phrasePool) != 26 {
		t.Fatalf("GPU phrase motion pool has %d entries, want 26", len(phrasePool))
	}
	for _, id := range phrasePool {
		pool[id] = true
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve pool motion %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		for _, animator := range definition.TextAnimators {
			if animator.Selector.Shape != "square" && animator.Selector.Shape != "smooth" {
				t.Errorf("pool motion %s has non-GPU selector shape %q", id, animator.Selector.Shape)
			}
			for _, property := range animator.Properties {
				if property.Property == "blur" {
					t.Errorf("pool motion %s carries per-unit blur", id)
				}
			}
		}
	}
	if pool["typewriter_neon"] {
		t.Error("typewriter_neon with per-glyph blur must not be in the GPU-native pool")
	}
	for _, id := range want {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		if definition.Category != "apple_phrase_v1" || definition.Enter != 60 {
			t.Errorf("%s: category=%q enter=%d, want apple_phrase_v1/60", id, definition.Category, definition.Enter)
		}
		if !pool[id] {
			t.Errorf("%s is absent from phrase_motion_pool", id)
		}
		for _, animator := range definition.TextAnimators {
			for _, property := range animator.Properties {
				if property.Property == "blur" {
					t.Errorf("%s carries per-unit blur", id)
				}
			}
		}
	}
}

// TestCatalogStaysInsideTheRendererVocabulary pins that every registered motion
// lowers to a document Chronon's schema accepts. The layer/animator property
// split is the interesting half: tracking is animator-only, while blur is
// supported both for a layer and for per-glyph text animation.
func TestCatalogStaysInsideTheRendererVocabulary(t *testing.T) {
	for _, id := range Registry.List() {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			continue
		}
		for _, track := range declarative.Definition.Tracks {
			if !layerProperties[track.Property] {
				t.Errorf("motion %s: layer track %q is not a renderer-supported layer property", id, track.Property)
			}
		}
		for _, animator := range declarative.Definition.TextAnimators {
			selector := animator.Selector
			if selector.Kind != "" && !selectorUnits[selector.Kind] {
				t.Errorf("motion %s: selector unit %q is not in the renderer vocabulary", id, selector.Kind)
			}
			if selector.Shape != "" && !selectorShapes[selector.Shape] {
				t.Errorf("motion %s: selector shape %q is not in the renderer vocabulary", id, selector.Shape)
			}
			if selector.Order != "" && !selectorOrders[selector.Order] {
				t.Errorf("motion %s: selector order %q is not in the renderer vocabulary", id, selector.Order)
			}
			for _, property := range animator.Properties {
				if !animatorProperties[property.Property] {
					t.Errorf("motion %s: animator property %q is not in the renderer vocabulary", id, property.Property)
				}
			}
		}
	}
}

func TestShortPhraseColorShorthandLowersToNativeRGBA(t *testing.T) {
	for _, id := range Registry.ShortPhraseStyleIDs() {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		for _, animator := range definition.TextAnimators {
			for _, property := range animator.Properties {
				if property.Property == "fill_blue" || property.Property == "fill_gray" || property.Property == "fill_orange" {
					t.Errorf("%s retains non-renderer color shorthand %q", id, property.Property)
				}
				if property.Property != "fill_color" {
					continue
				}
				for _, key := range property.Keyframes {
					color, ok := key.Value.([]float64)
					if !ok || len(color) != 4 {
						t.Errorf("%s fill_color frame %d = %#v, want RGBA", id, key.Frame, key.Value)
					}
				}
			}
		}
	}
}

func TestClaudeInspiredShortPhrasePaletteLowersToBlackAndOrange(t *testing.T) {
	id := "short_phrase_editorial_claude_diff_patch"
	plugin, err := Registry.Resolve(id)
	if err != nil {
		t.Fatalf("resolve %s: %v", id, err)
	}
	definition := plugin.(DeclarativePlugin).Definition
	if definition.Category != "short_phrase_style" {
		t.Fatalf("category = %q, want short_phrase_style", definition.Category)
	}
	if len(definition.TextAnimators) != 2 {
		t.Fatalf("%s animators = %d, want 2 (word entrance + orange selection)", id, len(definition.TextAnimators))
	}
	orange := definition.TextAnimators[1].Properties[0]
	if orange.Property != "fill_color" || len(orange.Keyframes) == 0 {
		t.Fatalf("orange accent lowered to %+v, want animated fill_color", orange)
	}
	got, ok := orange.Keyframes[0].Value.([]float64)
	if !ok || len(got) != 4 {
		t.Fatalf("orange value = %#v, want RGBA", orange.Keyframes[0].Value)
	}
	want := []float64{1, 0.38, 0.12, 1}
	for channel := range want {
		if got[channel] != want[channel] {
			t.Fatalf("orange channel %d = %v, want %v", channel, got[channel], want[channel])
		}
	}
}

func TestCharacterTrackingRevealMakesGlyphsVisible(t *testing.T) {
	plugin, err := Registry.Resolve("short_phrase_character_tracking_reveal")
	if err != nil {
		t.Fatal(err)
	}
	definition := plugin.(DeclarativePlugin).Definition
	for _, animator := range definition.TextAnimators {
		for _, property := range animator.Properties {
			if property.Property != "opacity" {
				continue
			}
			if len(property.Keyframes) < 2 {
				t.Fatalf("opacity has %d keyframes, want an entrance", len(property.Keyframes))
			}
			last, ok := shortPhraseNumericValue(property.Keyframes[len(property.Keyframes)-1].Value)
			if !ok || last < 0.99 {
				t.Fatalf("final glyph opacity = %#v, want fully visible", property.Keyframes[len(property.Keyframes)-1].Value)
			}
			return
		}
	}
	t.Fatal("character tracking reveal has no glyph opacity track")
}

// TestAppleV2PhraseFamilyKeepsTheFamilyContract pins the invariants the Apple
// V2 phrase family shares, so a row added to the canonical catalog cannot land
// half-formed (no layer tracks, or no per-unit animator). The definitions now
// come from the emitted catalog rather than a Go family function, so this is
// also the check that the emitter shipped every row intact.
func TestAppleV2PhraseFamilyKeepsTheFamilyContract(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("canonical catalog: %v", err)
	}
	var family []MotionDefinition
	for _, d := range catalog.Motions {
		if d.Category == "apple_v2" {
			family = append(family, d)
		}
	}
	if len(family) < 20 {
		t.Fatalf("apple_v2 phrase family = %d definitions, want at least 20", len(family))
	}
	for _, d := range family {
		if d.Category != "apple_v2" {
			t.Errorf("%s: category = %q, want apple_v2", d.ID, d.Category)
		}
		if d.Enter != 72 {
			t.Errorf("%s: enter = %d, want the family's 72-frame window", d.ID, d.Enter)
		}
		if len(d.Tracks) == 0 {
			t.Errorf("%s: no composition-level tracks", d.ID)
		}
		if len(d.TextAnimators) == 0 || len(d.TextAnimators[0].Properties) == 0 {
			t.Errorf("%s: no per-unit text animator", d.ID)
		}
		if d.TextAnimators[0].Selector.Kind != d.Unit {
			t.Errorf("%s: selector kind %q must match the declared unit %q", d.ID, d.TextAnimators[0].Selector.Kind, d.Unit)
		}
		if _, err := Registry.Resolve(d.ID); err != nil {
			t.Errorf("%s is in the canonical catalog but not registered: %v", d.ID, err)
		}
		for _, track := range d.Tracks {
			for _, kf := range track.Keyframes {
				if kf.Frame < 0 || kf.Frame > int64(d.Enter) {
					t.Errorf("%s/%s: keyframe %v falls outside the %d-frame enter window", d.ID, track.Property, kf.Frame, d.Enter)
				}
			}
		}
	}
}

// TestPlannerSelectableTextMotionsCarryBothHalves pins the fix for the reported
// "the phrases have no animation". The generated phrase planner selects from
// these ids only, and each must lower to BOTH a composition-level entrance and a
// per-unit text animator: a selector-only motion has no layer dynamics at all,
// so a backend that cannot prepare the selector renders it as a static line.
func TestPlannerSelectableTextMotionsCarryBothHalves(t *testing.T) {
	// Keep in lockstep with PipelineGen's phraseMotionCandidates.
	selectable := []string{"word_reveal", "character_cascade", "char_wave", "opacity_wave", "center_expansion"}
	for _, id := range selectable {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			t.Fatalf("%s is not a declarative definition", id)
		}
		d := declarative.Definition
		if len(d.Tracks) == 0 {
			t.Errorf("%s: no composition-level entrance (renders static without the selector)", id)
		}
		if len(d.TextAnimators) == 0 || len(d.TextAnimators[0].Properties) == 0 {
			t.Errorf("%s: no per-unit text animator", id)
		}
		faded := false
		for _, track := range d.Tracks {
			if track.Property == "opacity" {
				faded = len(track.Keyframes) >= 2 && track.Keyframes[0].Value == 0.0
			}
			for _, kf := range track.Keyframes {
				if kf.Frame < 0 || kf.Frame > int64(d.Enter) {
					t.Errorf("%s/%s: keyframe %v falls outside the %d-frame entrance window", id, track.Property, kf.Frame, d.Enter)
				}
			}
		}
		if !faded {
			t.Errorf("%s: composition-level entrance must start transparent", id)
		}
	}
}

type testPlugin struct{}

func (testPlugin) ID() string            { return "test_plugin" }
func (testPlugin) Validate(Params) error { return nil }
func (testPlugin) Compile(MotionContext, Params) ([]AnimationTrack, error) {
	return []AnimationTrack{{Property: "opacity", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 4, Value: 1.0}}}}, nil
}

func TestRegistryResolvesDeclarativeAndCustomPlugins(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("test_plugin", testPlugin{}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve("test_plugin")
	if err != nil || p.ID() != "test_plugin" {
		t.Fatalf("resolved plugin=%v err=%v", p, err)
	}
	if err := r.Register("test_plugin", testPlugin{}); err == nil {
		t.Fatal("duplicate registration must fail")
	}
	if _, err := r.Resolve("missing"); err == nil {
		t.Fatal("unknown plugin must fail closed")
	}
}

func TestDeclarativePluginCompilesGenericTracks(t *testing.T) {
	p := DeclarativePlugin{Definition: MotionDefinition{ID: "declarative", Tracks: []TrackDefinition{{
		Property: "scale", Easing: "out_back",
		Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.4}, {Frame: 10, Value: 1.0}},
	}}}}
	tracks, err := p.Compile(MotionContext{DurationFrames: 10}, nil)
	if err != nil || len(tracks) != 1 || tracks[0].Property != "scale" {
		t.Fatalf("tracks=%+v err=%v", tracks, err)
	}
}

func TestAppleV2MotionsAreComplete(t *testing.T) {
	// The count is pinned so a definition that is added to a family file but
	// never reaches the registry (a missing familyMotions() entry in the init)
	// fails here instead of silently shrinking the published catalog.
	// 16 classic apple_v2 motions + 26 modern v3 phrase motions.
	ids := Registry.CategoryMotionIDs("apple_v2")
	if len(ids) != 42 {
		t.Fatalf("Apple V2 motion count = %d, want 42", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate Apple V2 motion id %q", id)
		}
		seen[id] = true
	}
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		tracks, err := plugin.Compile(MotionContext{DurationFrames: 120}, nil)
		if err != nil {
			t.Fatalf("compile %s: %v", id, err)
		}
		textPlugin, ok := plugin.(TextMotionPlugin)
		if !ok {
			t.Fatalf("%s is not a text motion", id)
		}
		animators, err := textPlugin.CompileText(MotionContext{DurationFrames: 120}, nil)
		if err != nil || len(animators) == 0 {
			t.Fatalf("%s has no text animation: animators=%+v err=%v", id, animators, err)
		}
		if len(tracks) == 0 && len(animators[0].Properties) == 0 {
			t.Fatalf("%s is empty", id)
		}
	}
}

func TestAppleV3OverlayTextPackIs16ModernAnd2Point5DReady(t *testing.T) {
	ids := Registry.CategoryMotionIDs("apple_v3")
	if len(ids) != 16 {
		t.Fatalf("Apple V3 motion count = %d, want 16: %v", len(ids), ids)
	}
	hasDepth := false
	hasRotation := false
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		if definition.Unit == "" || definition.Enter != 72 || len(definition.TextAnimators) == 0 {
			t.Fatalf("%s is not a complete Apple V3 text motion: %+v", id, definition)
		}
		for _, track := range definition.Tracks {
			if track.Property == "position_z" {
				hasDepth = true
			}
			if track.Property == "rotation_x" || track.Property == "rotation_y" || track.Property == "rotation_z" {
				hasRotation = true
			}
		}
	}
	if !hasDepth || !hasRotation {
		t.Fatalf("Apple V3 pack has no 2.5D depth/rotation coverage: depth=%v rotation=%v", hasDepth, hasRotation)
	}
}

func TestImageV3OverlayPackIs10ModernAndLayerRenderable(t *testing.T) {
	ids := Registry.CategoryMotionIDs("overlay_v3_image")
	if len(ids) != 10 {
		t.Fatalf("Overlay V3 image motion count = %d, want 10: %v", len(ids), ids)
	}
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		if definition.Unit != "layer" || definition.Enter != 60 || len(definition.Tracks) == 0 {
			t.Fatalf("%s is not a complete image layer motion: %+v", id, definition)
		}
		tracks, err := plugin.Compile(MotionContext{DurationFrames: 120}, nil)
		if err != nil || len(tracks) == 0 {
			t.Fatalf("%s did not compile to layer tracks: tracks=%+v err=%v", id, tracks, err)
		}
	}
}

func TestChrononTemplateModern15PackIsRenderableAndNonStatic(t *testing.T) {
	want := []string{
		"kinetic_split_word", "dynamic_island_expansion", "masked_upward_reveal",
		"staggered_char_float", "high_specular_light_sweep", "depth_of_field_rack_focus",
		"micro_tracker_kerning_compression", "isometric_3d_fold", "soft_edge_spotlight_dissolve",
		"chromatic_aberration_pop", "fluid_gradient_text_flow", "velocity_inertia_snap",
		"vertical_rolling_counter", "glassmorphism_card_tilt", "pixel_grid_alpha_matrix",
	}
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("canonical catalog: %v", err)
	}
	definitions := make(map[string]MotionDefinition, len(catalog.Motions))
	for _, definition := range catalog.Motions {
		definitions[definition.ID] = definition
	}
	for _, id := range want {
		d, ok := definitions[id]
		if !ok {
			t.Fatalf("modern pack is missing %q", id)
		}
		if d.Category != "apple_v2" || d.Enter != 72 || d.Unit == "" {
			t.Errorf("%s: category=%q enter=%d unit=%q; want apple_v2/72/non-empty", id, d.Category, d.Enter, d.Unit)
		}
		if len(d.Tracks) == 0 || len(d.TextAnimators) == 0 {
			t.Errorf("%s: must carry both layer tracks and text animators", id)
			continue
		}
		varyingLayer := false
		for _, track := range d.Tracks {
			if len(track.Keyframes) > 1 && track.Keyframes[0].Value != track.Keyframes[len(track.Keyframes)-1].Value {
				varyingLayer = true
			}
		}
		if !varyingLayer {
			t.Errorf("%s: layer animation is constant", id)
		}
		for _, animator := range d.TextAnimators {
			if animator.Selector.Kind != d.Unit {
				t.Errorf("%s: selector kind=%q, want declared unit %q", id, animator.Selector.Kind, d.Unit)
			}
			varyingText := false
			for _, property := range animator.Properties {
				if len(property.Keyframes) > 1 && property.Keyframes[0].Value != property.Keyframes[len(property.Keyframes)-1].Value {
					varyingText = true
				}
			}
			if !varyingText {
				t.Errorf("%s: text animation is constant", id)
			}
		}
	}
}

// TestPhrasePoolClassificationIsExhaustive pins the 2.0 rule for the seeded
// phrase pool: every category in the catalog must be either a phrase-planning
// family, an explicitly preview-gated one, or an explicitly excluded one with
// a reason. A new catalog category that appears in none of the three lists
// fails here instead of silently dropping out of (or leaking into) the pool,
// and the pool itself must equal the union of the classified families.
func TestPhrasePoolClassificationIsExhaustive(t *testing.T) {
	planned := map[string]bool{}
	for _, category := range phrasePlanningCategories {
		planned[category] = true
	}
	for _, category := range Registry.Categories() {
		if planned[category] {
			continue
		}
		if reason, gated := previewGatedCategories[category]; gated && reason != "" {
			continue
		}
		if reason, excluded := phrasePoolExcludedCategories[category]; excluded && reason != "" {
			continue
		}
		t.Errorf("category %q is in no classification list (phrasePlanningCategories, previewGatedCategories, phrasePoolExcludedCategories): classify it with a reason", category)
	}
	want := map[string]bool{}
	for _, category := range phrasePlanningCategories {
		for _, id := range Registry.CategoryMotionIDs(category) {
			want[id] = true
		}
	}
	got := map[string]bool{}
	for _, id := range Registry.PhraseAnimationIDs() {
		got[id] = true
	}
	if fmt.Sprint(sortedKeys(got)) != fmt.Sprint(sortedKeys(want)) {
		t.Fatalf("PhraseAnimationIDs drifted from the classified families")
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
