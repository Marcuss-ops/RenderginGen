package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func compileAnimationPolicyFixture(t *testing.T, policies []any, items []any) (CompileResult, error) {
	t.Helper()
	document := map[string]any{
		"schema_version":     SemanticSchema,
		"plan_id":            "animation-policy-test",
		"video_id":           "animation-policy-test",
		"width":              1920,
		"height":             1080,
		"fps_num":            24,
		"fps_den":            1,
		"animation_policies": policies,
		"items":              items,
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return CompileSemantic(raw)
}

// loweredMotionFingerprint identifies the selected motion behind a lowered
// text layer: the per-unit animator id when the motion carries one, else the
// lowered layer tracks. The implicit random fallback may select a track-only
// motion (no per-unit animators), so comparing animator ids alone would read
// two identical selections as different.
func loweredMotionFingerprint(layer Layer) string {
	if layer.Animation == nil {
		return ""
	}
	if len(layer.Animation.TextAnimators) > 0 {
		return "animator:" + layer.Animation.TextAnimators[0].ID
	}
	raw, err := json.Marshal(layer.Animation.Tracks)
	if err != nil || len(layer.Animation.Tracks) == 0 {
		return ""
	}
	return "tracks:" + string(raw)
}

func TestAnimationPolicyMissingMotionSelectsOneStableRandomChoicePerGroup(t *testing.T) {
	policies := []any{map[string]any{"target": "important_phrase", "group_id": "launch"}}
	items := []any{
		map[string]any{"id": "first", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "text": "First phrase", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "second", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "text": "Second phrase", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "untouched", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "other", "text": "Untouched phrase", "start_ms": 0, "end_ms": 3000},
	}
	first, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile implicit random group policy: %v", err)
	}
	second, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("retry implicit random group policy: %v", err)
	}
	motionFor := func(result CompileResult, id string) string {
		for _, layer := range result.Plan.Layers {
			if layer.ID == id && layer.Animation != nil {
				return loweredMotionFingerprint(layer)
			}
		}
		return ""
	}
	firstMotion, secondMotion := motionFor(first, "first"), motionFor(first, "second")
	if firstMotion == "" || firstMotion != secondMotion || firstMotion != motionFor(second, "first") {
		t.Fatalf("declared group must have one retry-stable random motion: %q %q %q", firstMotion, secondMotion, motionFor(second, "first"))
	}
	if motionFor(first, "untouched") != motionFor(second, "untouched") {
		t.Fatalf("undeclared group must keep stable existing default, first=%q second=%q", motionFor(first, "untouched"), motionFor(second, "untouched"))
	}
	if motionFor(first, "untouched") == firstMotion {
		t.Fatalf("undeclared group should retain its item-specific automatic motion, group=%q item=%q", firstMotion, motionFor(first, "untouched"))
	}
}

func TestAnimationPolicyMissingMotionSelectsOneStableRandomChoicePerSubgroup(t *testing.T) {
	policies := []any{
		map[string]any{"target": "important_phrase", "group_id": "launch", "subgroup_id": "hero"},
	}
	items := []any{
		map[string]any{"id": "hero-a", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "subgroup_id": "hero", "text": "Hero one", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "hero-b", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "subgroup_id": "hero", "text": "Hero two", "start_ms": 0, "end_ms": 3000},
	}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile implicit random subgroup policy: %v", err)
	}
	byID := make(map[string]Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		if layer.Type == "text" {
			byID[layer.ID] = layer
		}
	}
	a, okA := byID["hero-a"]
	b, okB := byID["hero-b"]
	if !okA || !okB || a.Animation == nil || b.Animation == nil {
		t.Fatalf("subgroup members did not lower to animated text layers: %+v", result.Plan.Layers)
	}
	if fa, fb := loweredMotionFingerprint(a), loweredMotionFingerprint(b); fa == "" || fa != fb {
		t.Fatalf("subgroup members did not share their selected motion: %q vs %q", fa, fb)
	}
}

func TestAnimationPoliciesApplyGroupSubgroupAndExplicitPrecedence(t *testing.T) {
	policies := []any{
		map[string]any{"target": "important_phrase", "group_id": "launch", "motion_id": "text_fade_up"},
		map[string]any{"target": "important_phrase", "group_id": "launch", "subgroup_id": "hero", "motion_id": "text_word_stagger"},
	}
	items := []any{
		map[string]any{"id": "group-default", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "text": "Group default", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "subgroup-default", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "subgroup_id": "hero", "text": "Subgroup default", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "explicit-override", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "launch", "subgroup_id": "hero", "motion_id": "text_word_rise", "text": "Explicit choice", "start_ms": 0, "end_ms": 3000},
	}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile grouped phrase policies: %v", err)
	}
	layers := make(map[string]Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		layers[layer.ID] = layer
	}
	for _, id := range []string{"group-default", "subgroup-default", "explicit-override"} {
		if _, ok := layers[id]; !ok {
			t.Fatalf("compiled phrase layer %q missing; layers=%v", id, result.Plan.Layers)
		}
	}
	if len(layers["group-default"].Animation.Tracks) == 0 {
		t.Fatal("group policy text_fade_up did not select an animation")
	}
	if len(layers["subgroup-default"].TextAnimators) == 0 {
		t.Fatal("subgroup policy text_word_stagger did not override its group policy")
	}
	if len(layers["explicit-override"].TextAnimators) == 0 {
		t.Fatal("explicit text_word_rise did not take precedence over subgroup policy")
	}
}

func TestAnimationPoliciesRouteDateAndMetricPresentationGroups(t *testing.T) {
	policies := []any{
		map[string]any{"target": "date", "group_id": "timeline", "motion_id": "date_timeline_sweep"},
		map[string]any{"target": "metric", "group_id": "timeline", "motion_id": "metric_counter_rise"},
	}
	items := []any{
		map[string]any{"id": "date", "kind": "number", "template_id": "TIMELINE_DATE_CARD", "preset_id": PhraseDefaultPresetID, "group_id": "timeline", "subgroup_id": "date", "text": "March 5, 2026", "start_ms": 0, "end_ms": 3000},
		map[string]any{"id": "metric", "kind": "number", "template_id": "METRIC_STAT_CARD", "preset_id": PhraseDefaultPresetID, "group_id": "timeline", "subgroup_id": "metric", "text": "59.99", "start_ms": 3000, "end_ms": 6000},
	}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile date/metric policies: %v", err)
	}
	if len(result.Plan.Layers) != 2 {
		t.Fatalf("layers = %d, want two presentation targets", len(result.Plan.Layers))
	}
	for _, layer := range result.Plan.Layers {
		if layer.Animation == nil || (len(layer.Animation.Tracks) == 0 && len(layer.TextAnimators) == 0) {
			t.Errorf("%s policy did not lower to an animation: %+v", layer.ID, layer)
		}
	}
}

func TestAnimationPoliciesApplyIndependentImageAndCaptionDefaults(t *testing.T) {
	policies := []any{
		map[string]any{"target": "image", "group_id": "people", "motion_id": "image_focus_reveal"},
		map[string]any{"target": "image", "group_id": "people", "subgroup_id": "featured", "motion_id": "image_25d_yaw_flip_in"},
		map[string]any{"target": "caption", "group_id": "people", "motion_id": "text_fade_up", "motion_params": map[string]any{"enter_frames": 4}},
	}
	items := []any{map[string]any{
		"id": "portraits", "kind": "entity_image", "template_id": "image_popup", "preset_id": "image_focus_in",
		"group_id": "people", "start_ms": 0, "end_ms": 5000, "duration_ms": 5000,
		"asset_refs": []any{
			map[string]any{"asset_id": "featured-asset", "sha256": strings.Repeat("a", 64), "url": "https://example.test/featured.png", "media_type": "image/png"},
			map[string]any{"asset_id": "regular-asset", "sha256": strings.Repeat("b", 64), "url": "https://example.test/regular.png", "media_type": "image/png"},
		},
		"image_layers": []any{
			map[string]any{"id": "featured", "group_id": "people", "subgroup_id": "featured", "asset_id": "featured-asset", "start_ms": 0, "end_ms": 5000, "preset_id": "image_focus_in", "caption": "Featured", "params": map[string]any{"width": 420, "height": 480, "position_x": -460.0}},
			map[string]any{"id": "regular", "group_id": "people", "asset_id": "regular-asset", "start_ms": 0, "end_ms": 5000, "preset_id": "image_focus_in", "caption": "Regular", "params": map[string]any{"width": 420, "height": 480, "position_x": 460.0}},
		},
	}}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile independently animated image/caption policies: %v", err)
	}
	layers := make(map[string]Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		layers[layer.ID] = layer
	}
	featured, regular := layers["portraits:featured:image"], layers["portraits:regular:image"]
	if featured.Animation == nil || regular.Animation == nil {
		t.Fatal("group/subgroup policies did not animate both image children")
	}
	if len(featured.Animation.Tracks) == 0 || len(regular.Animation.Tracks) == 0 {
		t.Fatalf("image policy tracks missing: featured=%+v regular=%+v", featured.Animation, regular.Animation)
	}
	if featured.Animation.Tracks[0].Property == regular.Animation.Tracks[0].Property && featured.Animation.Tracks[0].Keyframes[0].Value == regular.Animation.Tracks[0].Keyframes[0].Value {
		t.Fatal("featured subgroup motion did not override the group image motion")
	}
	for _, id := range []string{"portraits:featured:caption", "portraits:regular:caption"} {
		caption, ok := layers[id]
		if !ok || caption.Animation == nil || len(caption.Animation.Tracks) == 0 {
			t.Fatalf("caption policy did not lower independent text motion for %q: %+v", id, caption)
		}
	}
}

func TestAnimationPolicyAppliesToStandaloneImageAndEntityCaption(t *testing.T) {
	policies := []any{
		map[string]any{"target": "image", "group_id": "assets", "motion_id": "image_focus_reveal", "motion_params": map[string]any{"enter_frames": 5}},
		map[string]any{"target": "caption", "group_id": "assets", "motion_id": "text_word_rise", "motion_params": map[string]any{"enter_frames": 6}},
	}
	items := []any{
		map[string]any{
			"id": "standalone", "kind": "image", "template_id": "image_popup", "preset_id": "image_focus_in", "group_id": "assets",
			"start_ms": 0, "end_ms": 5000, "asset_refs": []any{map[string]any{"asset_id": "solo", "sha256": strings.Repeat("c", 64), "url": "https://example.test/solo.png"}},
		},
		map[string]any{
			"id": "entity", "kind": "entity_image", "template_id": "image_popup", "preset_id": "image_focus_in", "group_id": "assets",
			"start_ms": 0, "end_ms": 5000, "entity_caption": "Policy Caption",
			"asset_refs": []any{map[string]any{"asset_id": "person", "sha256": strings.Repeat("d", 64), "url": "https://example.test/person.png"}},
		},
	}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile stand-alone image and entity caption policy: %v", err)
	}
	layers := make(map[string]Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		layers[layer.ID] = layer
	}
	image := layers["standalone:image"]
	if image.Animation == nil || len(image.Animation.Tracks) == 0 {
		t.Fatalf("single-image group policy did not lower: %+v", image)
	}
	caption := layers["entity:entity:caption"]
	if caption.Text != "Policy Caption" || caption.Animation == nil || len(caption.Animation.Tracks) == 0 {
		t.Fatalf("entity caption group policy did not lower: %+v", caption)
	}
}

func TestAnimationPolicyPreservesEntityStyleCompositionAndCaptionTimingSeparation(t *testing.T) {
	policies := []any{
		map[string]any{"target": "image", "group_id": "styled", "motion_id": "image_focus_reveal", "motion_params": map[string]any{"enter_frames": 5}},
		map[string]any{"target": "caption", "group_id": "styled", "motion_id": "text_word_rise", "motion_params": map[string]any{"enter_frames": 6}},
	}
	items := []any{map[string]any{
		"id": "portrait", "entity_id": "person:styled", "kind": "entity_card", "template_id": "PERSON", "preset_id": "phrase_default", "entity_style_id": "01_entity_pitch_lift_text_below",
		"text": "Styled Person", "group_id": "styled", "image_preset_id": "image_scale_in", "entity_caption": "Styled policy",
		"start_ms": 0, "end_ms": 5000, "duration_ms": 5000,
		"asset_refs": []any{map[string]any{"asset_id": "person", "sha256": strings.Repeat("e", 64), "url": "https://example.test/person.png"}},
	}}
	result, err := compileAnimationPolicyFixture(t, policies, items)
	if err != nil {
		t.Fatalf("compile styled policy: %v", err)
	}
	layers := make(map[string]Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		layers[layer.ID] = layer
	}
	image, caption := layers["portrait:image"], layers["portrait:entity:caption"]
	if image.Animation == nil || image.Animation.Tracks[0].Property == "rotation_y" {
		t.Fatalf("image policy should override only the style's default motion, got %+v", image.Animation)
	}
	if caption.Animation == nil || len(caption.Animation.Tracks) == 0 {
		t.Fatalf("caption policy should override the style's caption motion: %+v", caption)
	}
	if caption.Animation.Tracks[0].Property != "position_y" {
		t.Fatalf("caption motion should be selected by its policy, got %+v", caption.Animation)
	}
}

func TestAnimationPolicyPhraseUsesCanonicalTextTargets(t *testing.T) {
	declaredPhrase := []any{map[string]any{"target": "important_phrase", "group_id": "g", "motion_id": "text_fade_up"}}
	if _, err := compileAnimationPolicyFixture(t, declaredPhrase, []any{
		map[string]any{"id": "phrase", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "g", "text": "Declared target", "start_ms": 0, "end_ms": 3000},
	}); err != nil {
		t.Fatalf("declared text target should be accepted: %v", err)
	}

	catalogPhrase := []any{map[string]any{"target": "important_phrase", "group_id": "g", "motion_id": "apple_focus_rise"}}
	if _, err := compileAnimationPolicyFixture(t, catalogPhrase, []any{
		map[string]any{"id": "phrase", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "group_id": "g", "text": "Catalog target", "start_ms": 0, "end_ms": 3000},
	}); err != nil {
		t.Fatalf("catalog-declared text target should be accepted: %v", err)
	}
	definition, err := resolveMotionDefinition("apple_focus_rise")
	if err != nil || definition == nil || !containsString(definition.Targets, "text") {
		t.Fatalf("apple_focus_rise must declare its text target in the canonical catalog: definition=%+v err=%v", definition, err)
	}
}

func TestAnimationPoliciesRejectInvalidSelectorsAndMotionTargets(t *testing.T) {
	cases := []struct {
		name     string
		policies []any
		want     string
	}{
		{name: "unsupported target", policies: []any{map[string]any{"target": "camera", "group_id": "g", "motion_id": "text_fade_up"}}, want: "unsupported target"},
		{name: "unknown motion", policies: []any{map[string]any{"target": "image", "group_id": "g", "motion_id": "not_a_motion"}}, want: "not supported"},
		{name: "params without motion", policies: []any{map[string]any{"target": "caption", "group_id": "g", "motion_params": map[string]any{"enter_frames": 2}}}, want: "motion_params requires motion_id"},
		{name: "mismatched motion target", policies: []any{map[string]any{"target": "image", "group_id": "g", "motion_id": "text_fade_up"}}, want: "not supported"},
		{name: "subgroup without group", policies: []any{map[string]any{"target": "caption", "subgroup_id": "child", "motion_id": "text_fade_up"}}, want: "subgroup_id requires group_id"},
		{name: "invalid timing parameter", policies: []any{map[string]any{"target": "caption", "group_id": "g", "motion_id": "text_fade_up", "motion_params": map[string]any{"enter_frames": 1.5}}}, want: "motion_params.enter_frames"},
		{name: "duplicate selector", policies: []any{
			map[string]any{"target": "caption", "group_id": "g", "motion_id": "text_fade_up"},
			map[string]any{"target": "caption", "group_id": "g", "motion_id": "text_word_rise"},
		}, want: "duplicate animation policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileAnimationPolicyFixture(t, tc.policies, []any{
				map[string]any{"id": "phrase", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": PhraseDefaultPresetID, "text": "Policy validation", "start_ms": 0, "end_ms": 3000},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid animation policy error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
