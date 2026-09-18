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
		"opacity": true,
	}
	// The text animator vocabulary is wider: it adds blur and tracking, which a
	// layer track may not carry.
	animatorProperties = map[string]bool{
		"position": true, "position_x": true, "position_y": true,
		"scale": true, "scale_x": true, "scale_y": true,
		"opacity": true, "blur": true, "tracking": true,
	}
)

// TestCatalogStaysInsideTheRendererVocabulary pins that every registered motion
// lowers to a document Chronon's schema accepts. The layer/animator property
// split is the interesting half: blur and tracking are animator-only, so a
// phrase that wants one of them must put it on its text animator.
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
				t.Errorf("motion %s: layer track %q is not a layer property (blur/tracking are animator-only)", id, track.Property)
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
