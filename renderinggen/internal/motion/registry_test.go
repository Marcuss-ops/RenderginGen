package motion

import "testing"

// The renderer's closed vocabularies, mirrored from
// chronon.render-plan.v2 ($defs/text_selector and layers[].animation.tracks[]).
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
	}
)

func TestMotionFamiliesKeepStylesIndependentAndComplete(t *testing.T) {
	want := []string{"typewriter", "classic_apple", "modern_apple", "web", "3d"}
	got := Registry.MotionFamilies()
	if len(got) != len(want) {
		t.Fatalf("motion families = %v, want %v", got, want)
	}
	for i, family := range want {
		if got[i] != family {
			t.Fatalf("motion family[%d] = %q, want %q", i, got[i], family)
		}
	}
	for family, count := range map[string]int{"typewriter": 5, "classic_apple": 42, "modern_apple": 60, "web": 0} {
		if ids := Registry.FamilyMotionIDs(family); len(ids) != count {
			t.Errorf("%s family has %d motions, want %d", family, len(ids), count)
		}
	}
	threeD := Registry.FamilyMotionIDs("3d")
	if len(threeD) == 0 {
		t.Fatal("3D family has no catalog motions")
	}
	for _, id := range threeD {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve 3D family motion %q: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		if !motionHas3D(definition) {
			t.Errorf("3D family motion %q has no camera-backed 3D property", id)
		}
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
	for _, family := range want {
		for _, id := range Registry.FamilyMotionIDs(family) {
			if _, err := Registry.Resolve(id); err != nil {
				t.Errorf("%s motion %q does not resolve: %v", family, id, err)
			}
		}
	}
}

func TestApplePhrasePackIsInTheCanonicalPoolAndHasNoPerGlyphBlur(t *testing.T) {
	want := []string{
		"apple_focus_rise", "apple_soft_scale", "apple_word_cascade", "apple_line_cascade",
		"apple_precision_type", "apple_tracking_reveal", "apple_expand_from_center",
		"apple_compress_in", "apple_scale_push", "apple_scale_settle", "apple_word_pulse",
		"apple_vertical_glyph_lift", "apple_line_sweep", "apple_hero_statement", "apple_cinematic_exit",
	}
	got := Registry.ApplePhrasePackMotionIDs()
	if len(got) != len(want) {
		t.Fatalf("Apple phrase pack has %d motions, want %d: %v", len(got), len(want), got)
	}
	pool := make(map[string]bool)
	phrasePool := PhraseMotionPool()
	if len(phrasePool) != 22 {
		t.Fatalf("GPU phrase motion pool has %d entries, want 22", len(phrasePool))
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
	ids := Registry.AppleV2MotionIDs()
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

func TestAppleV3OverlayTextPackIs15ModernAnd2Point5DReady(t *testing.T) {
	ids := Registry.AppleV3MotionIDs()
	if len(ids) != 15 {
		t.Fatalf("Apple V3 motion count = %d, want 15: %v", len(ids), ids)
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
	ids := Registry.ImageV3MotionIDs()
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
