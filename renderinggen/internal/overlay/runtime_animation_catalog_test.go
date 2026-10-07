package overlay

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

func TestRuntimeMotionCatalogIsCompleteStableAndDefensive(t *testing.T) {
	first, _ := runtimeMotionCatalog()
	if len(first) == 0 || len(first) != len(motion.Registry.List()) {
		t.Fatalf("runtime inventory has %d motions, canonical registry has %d", len(first), len(motion.Registry.List()))
	}
	for i, id := range motion.Registry.List() {
		if first[i].ID != id {
			t.Fatalf("runtime inventory order at %d = %q, want %q", i, first[i].ID, id)
		}
	}
	first[0].ID = "mutated"
	second, _ := runtimeMotionCatalog()
	if second[0].ID == "mutated" {
		t.Fatal("runtime inventory caller mutated the canonical catalog view")
	}
	families := runtimeMotionFamilies(first)
	if len(runtimeMotionFamilyIDs(families)) == 0 || len(families) == 0 {
		t.Fatal("runtime catalog did not expose family groups")
	}
	if _, ok := families["3d"]; ok {
		t.Fatal("renderer capability 3d must not appear as an overlapping motion family")
	}
	if len(families["text_3d_v1"]) == 0 {
		t.Fatal("named text_3d_v1 category is missing from runtime families")
	}
	for family, options := range families {
		for _, option := range options {
			if !option.TargetsDeclared || len(option.Targets) == 0 {
				t.Fatalf("family picker %q contains unselectable motion %q", family, option.ID)
			}
			if option.Family != family {
				t.Fatalf("family %q contains motion %q in category %q", family, option.ID, option.Family)
			}
			if family == "text_3d_v1" && !option.Requires3D {
				t.Errorf("text 3D motion %q lost its renderer capability metadata", option.ID)
			}
		}
	}
}

func TestRuntimeAnimationUseCasesSeparateCountedCompositions(t *testing.T) {
	options, definitions := runtimeMotionCatalog()
	useCases := runtimeAnimationUseCases(options, definitions)
	byID := make(map[string]RuntimeAnimationUseCase, len(useCases))
	for _, useCase := range useCases {
		if _, exists := byID[useCase.ID]; exists {
			t.Fatalf("duplicate runtime animation use case %q", useCase.ID)
		}
		byID[useCase.ID] = useCase
		for _, id := range useCase.MotionIDs {
			if _, err := motion.Registry.Resolve(id); err != nil {
				t.Errorf("use case %q contains unknown motion %q", useCase.ID, id)
			}
		}
	}
	for _, id := range []string{
		"important_phrase", "short_important_phrase", "single_image", "image_double", "image_triplet", "image_four", "image_five",
		"single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text",
		"one_map", "two_maps", "background_color", "background_image", "background_video",
		"entity_caption", "metric_stat", "timeline_date",
	} {
		if _, ok := byID[id]; !ok {
			t.Errorf("runtime use case %q missing", id)
		}
	}
	if _, ok := byID["image_stack"]; ok {
		t.Fatal("image_stack must not be a picker category")
	}
	for _, id := range byID["short_important_phrase"].MotionIDs {
		if containsString(byID["important_phrase"].MotionIDs, id) {
			t.Errorf("short-phrase motion %q also appears in the important_phrase picker", id)
		}
	}
	// The counted compositions reuse the item motions of their own target
	// (image layers, map plates) instead of publishing a second family; the
	// plan-owned background sources are the only use cases with no family, and
	// they must keep serializing as an explicit null.
	for _, id := range []string{
		"image_double", "image_triplet", "image_four", "image_five",
		"single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text",
		"two_maps",
	} {
		if len(byID[id].MotionIDs) == 0 {
			t.Errorf("use case %q must expose the admitted motions of its target; got %v", id, byID[id].MotionIDs)
		}
	}
	for _, id := range []string{"background_color", "background_image", "background_video"} {
		if byID[id].MotionIDs != nil {
			t.Errorf("use case %q must serialize as null until it has a dedicated family; got %v", id, byID[id].MotionIDs)
		}
	}
	if byID["single_image"].Cardinality != "one image" || byID["image_double"].Cardinality != "two images" || byID["one_map"].Cardinality != "one map" || byID["two_maps"].Cardinality != "two maps" {
		t.Fatalf("image/map cardinalities unclear: single=%q double=%q one_map=%q two_maps=%q", byID["single_image"].Cardinality, byID["image_double"].Cardinality, byID["one_map"].Cardinality, byID["two_maps"].Cardinality)
	}
	if !strings.Contains(byID["single_image"].Description, "single-image target") {
		t.Errorf("single_image description must describe target compatibility, got %q", byID["single_image"].Description)
	}
	for id, count := range map[string]int{
		"single_image": 1, "image_double": 2, "image_triplet": 3, "image_four": 4, "image_five": 5,
		"single_image_with_text": 1, "image_double_with_text": 2, "image_triplet_with_text": 3, "image_four_with_text": 4, "image_five_with_text": 5,
	} {
		composition := byID[id].Composition
		if composition == nil || composition.ImageCount != count || composition.MapCount != 0 {
			t.Errorf("use case %q composition = %+v, want image_count=%d", id, composition, count)
			continue
		}
		if strings.HasSuffix(id, "_with_text") {
			if composition.Caption == nil || composition.Caption.Minimum != 1 || composition.Caption.Maximum != count {
				t.Errorf("use case %q caption bounds = %+v, want [1,%d]", id, composition.Caption, count)
			}
		} else if composition.Caption != nil {
			t.Errorf("image-only use case %q unexpectedly declares captions: %+v", id, composition.Caption)
		}
		word := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five"}[count]
		cardinality := word + " image"
		if count != 1 {
			cardinality += "s"
		}
		if strings.HasSuffix(id, "_with_text") {
			cardinality += " with text"
		}
		if byID[id].Cardinality != cardinality {
			t.Errorf("use case %q cardinality = %q, want %q", id, byID[id].Cardinality, cardinality)
		}
	}
	if byID["one_map"].Composition == nil || byID["one_map"].Composition.MapCount != 1 || byID["two_maps"].Composition == nil || byID["two_maps"].Composition.MapCount != 2 {
		t.Errorf("map composition counts are not explicit: one=%+v two=%+v", byID["one_map"].Composition, byID["two_maps"].Composition)
	}
	for _, id := range byID["single_image"].MotionIDs {
		if animationUsesMultiImageRecipe(id) {
			t.Errorf("legacy stack recipe %q leaked into the single-image category", id)
		}
	}
	if len(byID["one_map"].MotionIDs) == 0 {
		t.Fatal("one_map should expose its existing dedicated map family")
	}
	for kind, id := range map[string]string{
		"color": "background_color", "image": "background_image", "video": "background_video",
	} {
		background := byID[id]
		if background.Scope != "plan" || background.BackgroundKind != kind || background.ItemKinds != nil || background.MotionIDs != nil {
			t.Errorf("background use case %q = %+v, want plan scope, kind %q, no item kinds and no motion family", id, background, kind)
		}
	}
	mapDescription := strings.ToLower(byID["one_map"].Description)
	if !strings.Contains(mapDescription, "static maps") || !strings.Contains(mapDescription, "camera_move") {
		t.Errorf("one_map description must distinguish static motion from fly-to: %q", byID["one_map"].Description)
	}
	for id, targetAndFamily := range map[string][2]string{
		"metric_stat":   {"metric", "metric_v1"},
		"timeline_date": {"date", "date_v1"},
	} {
		target, family := targetAndFamily[0], targetAndFamily[1]
		if len(byID[id].MotionIDs) == 0 {
			t.Errorf("%s should expose its authored %s motion family", id, target)
		}
		for _, motionID := range byID[id].MotionIDs {
			if !motionAdmitsTarget(motionID, target) {
				t.Errorf("%s contains motion %q that does not admit target %q", id, motionID, target)
			}
			definition, err := resolveMotionDefinition(motionID)
			if err != nil || definition == nil || definition.Category != family {
				t.Errorf("%s contains motion %q outside canonical family %q", id, motionID, family)
			}
		}
	}
	for _, id := range []string{"important_phrase", "entity_caption"} {
		for _, motionID := range byID[id].MotionIDs {
			definition, err := resolveMotionDefinition(motionID)
			if err != nil || definition == nil {
				t.Errorf("%s contains unresolvable motion %q", id, motionID)
				continue
			}
			if containsString(definition.Targets, "metric") || containsString(definition.Targets, "date") {
				t.Errorf("specialized motion %q leaked into generic use case %q", motionID, id)
			}
			if id == "important_phrase" && containsString(definition.Targets, "entity") {
				t.Errorf("entity motion %q leaked into generic phrase use case", motionID)
			}
		}
	}
	if !containsString(byID["important_phrase"].ItemKinds, string(KindImportantPhrase)) {
		t.Fatal("runtime use cases are not connected to semantic item kinds")
	}
	for _, kind := range []ItemKind{KindEntityCard, KindOrganization, KindLocation, KindConcept, KindEntityImage, KindImagePopup, KindProduct, KindLogo, KindLightLeak} {
		if !containsString(byID["entity_caption"].ItemKinds, string(kind)) {
			t.Errorf("entity_caption use case does not advertise supported image-bearing kind %q", kind)
		}
	}
	if !containsString(byID["entity_caption"].ItemKinds, "image") {
		t.Error("entity_caption use case does not advertise generic image layers")
	}
}

func TestRuntimeMotionCatalogKeepsUndeclaredTargetsUnselectable(t *testing.T) {
	options, _ := runtimeMotionCatalog()
	for _, option := range options {
		if option.Deprecated {
			families := runtimeMotionFamilies(options)
			for family, selectable := range families {
				for _, item := range selectable {
					if item.ID == option.ID {
						t.Errorf("deprecated motion %q entered selectable family %q", option.ID, family)
					}
				}
			}
		}
		if !option.TargetsDeclared {
			if len(option.Targets) != 0 {
				t.Fatalf("motion %q has undeclared targets but exposes %v", option.ID, option.Targets)
			}
			for _, target := range []string{"important_phrase", "short_phrase", "caption", "image", "map_view", "metric", "date"} {
				if motionAdmitsTarget(option.ID, target) {
					t.Errorf("motion %q without declared targets was admitted for %q", option.ID, target)
				}
			}
		}
	}
}

func TestAnimationPolicyUsesDeclaredTargets(t *testing.T) {
	for _, tc := range []struct {
		target string
		id     string
		want   bool
	}{
		{target: "important_phrase", id: "text_fade_up", want: true},
		{target: "important_phrase", id: "image_focus_reveal", want: false},
		{target: "important_phrase", id: "typewriter_clean", want: true},
		{target: "important_phrase", id: "typewriter_modern_01_monospace_block_cursor", want: true},
		{target: "important_phrase", id: "blur_focus_in", want: true},
		{target: "image", id: "image_focus_reveal", want: true},
		{target: "image", id: "text_fade_up", want: false},
		{target: "image", id: "brush_arrow_point", want: true},
		{target: "image", id: "image_card_push", want: true},
		{target: "image", id: "image_parallax_depth_reveal", want: true},
		{target: "image", id: "map_image_italy_beacon_arrival", want: false},
		{target: "caption", id: "blur_focus_in", want: true},
		{target: "caption", id: "trump_entity_text_01", want: true},
		{target: "important_phrase", id: "metric_counter_rise", want: false},
		{target: "important_phrase", id: "date_timeline_sweep", want: false},
		{target: "important_phrase", id: "entity_caption_rise", want: false},
		{target: "caption", id: "metric_counter_rise", want: false},
		{target: "caption", id: "date_timeline_sweep", want: false},
		{target: "caption", id: "entity_caption_rise", want: true},
		{target: "metric", id: "metric_counter_rise", want: true},
		{target: "metric", id: "date_timeline_sweep", want: false},
		{target: "date", id: "date_timeline_sweep", want: true},
		{target: "date", id: "metric_counter_rise", want: false},
		{target: "short_phrase", id: "typewriter_modern_01_monospace_block_cursor", want: true},
	} {
		if got := motionAdmitsTarget(tc.id, tc.target); got != tc.want {
			t.Errorf("motionAdmitsTarget(%q, %q) = %v, want %v", tc.id, tc.target, got, tc.want)
		}
	}
	if !motionAdmitsTarget("map_image_italy_beacon_arrival", "map_view") {
		t.Error("canonical map_image_v1 target should admit the map motion")
	}
	if motionAdmitsTarget("image_depth_float", "map_view") || motionAdmitsTarget("image_card_push", "map_view") {
		t.Error("image-only camera or depth motion was admitted for a georeferenced map viewport")
	}
	if !motionAdmitsTarget("image_fade_reveal", "map_view") {
		t.Error("centered image overlay with canonical map_view target was not admitted")
	}
	options, definitions := runtimeMotionCatalog()
	var shortUseCase RuntimeAnimationUseCase
	for _, useCase := range runtimeAnimationUseCases(options, definitions) {
		if useCase.ID == "short_important_phrase" {
			shortUseCase = useCase
			break
		}
	}
	if shortUseCase.ID == "" {
		t.Fatal("short_important_phrase use case is missing")
	}
	for _, option := range options {
		wantShortPhrase := option.TargetsDeclared && containsString(option.Targets, "short_phrase")
		if got := motionAdmitsTarget(option.ID, "short_phrase"); got != wantShortPhrase {
			t.Errorf("short-phrase motion %q admission = %v, target metadata says %v", option.ID, got, wantShortPhrase)
		}
		if wantShortPhrase && !containsString(shortUseCase.MotionIDs, option.ID) {
			t.Errorf("catalog target short_phrase for %q was omitted from its picker", option.ID)
		}
		if !wantShortPhrase && containsString(shortUseCase.MotionIDs, option.ID) {
			t.Errorf("motion %q without short_phrase target entered its picker", option.ID)
		}
	}
}

func TestExplicitMotionTargetsAreValidatedByLowering(t *testing.T) {
	cases := []struct {
		id, target string
		wantError  bool
	}{
		{id: "image_focus_reveal", target: "image"},
		{id: "text_fade_up", target: "image", wantError: true},
		{id: "metric_counter_rise", target: "metric"},
		{id: "metric_counter_rise", target: "date", wantError: true},
		{id: "date_timeline_sweep", target: "date"},
		{id: "date_timeline_sweep", target: "metric", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.id+"/"+tc.target, func(t *testing.T) {
			_, err := animationForMotionTarget(tc.id, nil, "test text", 120, 0, "test-item", tc.target, false)
			if (err != nil) != tc.wantError {
				t.Fatalf("lowering motion %q for %q error = %v, wantError=%v", tc.id, tc.target, err, tc.wantError)
			}
		})
	}
	if _, err := imageMotionAnimation("text_fade_up", nil, 120, 0, "test-image", "image"); err == nil || !strings.Contains(err.Error(), "not supported for target \"image\"") {
		t.Fatalf("image motion target error = %v, want item-level image target diagnostic", err)
	}
}

func TestSemanticItemsRejectExplicitMotionsForAnotherTarget(t *testing.T) {
	cases := []struct {
		name, item, want string
	}{
		{
			name: "phrase cannot use metric motion",
			item: `{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"metric_counter_rise","text":"Revenue","start_ms":0,"end_ms":3000}`,
			want: `target "important_phrase"`,
		},
		{
			name: "image cannot use text motion",
			item: `{"id":"image","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","motion_id":"text_fade_up","start_ms":0,"end_ms":3000,"asset_refs":[{"asset_id":"image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/image.png"}]}`,
			want: `target "image"`,
		},
		{
			name: "metric template cannot use date motion",
			item: `{"id":"metric","template_id":"METRIC_STAT_CARD","preset_id":"phrase_default","motion_id":"date_timeline_sweep","text":"59.99","start_ms":0,"end_ms":3000}`,
			want: `target "metric"`,
		},
		{
			name: "quote cannot use image motion",
			item: `{"id":"quote","kind":"quote","template_id":"QUOTE","preset_id":"phrase_default","motion_id":"image_focus_reveal","text":"Words","start_ms":0,"end_ms":3000}`,
			want: `target "text"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"target-check","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[` + tc.item + `]}`)
			_, err := CompileSemantic(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile mismatch error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestTextLikeKindsFollowSemanticBehaviorRegistry(t *testing.T) {
	for _, kind := range []ItemKind{
		KindImportantPhrase, KindImportantWord, KindQuote, KindNumber, KindMetricStat,
		KindTimelineDate, KindLowerThird, KindPrimitive, KindEntityCard, KindOrganization,
		KindLocation, KindConcept,
	} {
		if !isTextLikeKind(kind) {
			t.Errorf("text/entity kind %q was not classified text-like", kind)
		}
	}
	for _, kind := range []ItemKind{KindEntityImage, KindImagePopup, KindProduct, KindLogo, KindMap, KindShape, KindVideoOverlay} {
		if isTextLikeKind(kind) {
			t.Errorf("non-text kind %q was classified text-like", kind)
		}
	}
}

func TestCompiledRuntimeAnimationCatalogIsDeterministicAndConsumable(t *testing.T) {
	var first, second bytes.Buffer
	if err := WriteRuntimeAnimationCatalog(&first); err != nil {
		t.Fatal(err)
	}
	if err := WriteRuntimeAnimationCatalog(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("compiled runtime animation catalog is not deterministic")
	}
	var decoded RuntimeAnimationCatalog
	if err := json.Unmarshal(first.Bytes(), &decoded); err != nil {
		t.Fatalf("decode UI catalog artifact: %v", err)
	}
	compiled := CompiledRuntimeAnimationCatalog()
	if decoded.SchemaVersion != 1 || len(decoded.Motions) != len(motion.Registry.List()) || len(decoded.UseCases) != len(compiled.UseCases) {
		t.Fatalf("compiled UI catalog is incomplete: schema=%d motions=%d use_cases=%d", decoded.SchemaVersion, len(decoded.Motions), len(decoded.UseCases))
	}
	if !strings.Contains(first.String(), `"use_cases"`) || !strings.Contains(first.String(), `"targets_declared"`) {
		t.Fatal("compiled UI catalog omitted semantic use cases or legacy target status")
	}
	if !strings.Contains(first.String(), `"id": "background_video"`) || !strings.Contains(first.String(), `"motion_ids": null`) {
		t.Fatal("compiled picker catalog must represent an unimplemented composition family as explicit null")
	}
	if !strings.Contains(first.String(), `"image_count": 5`) || !strings.Contains(first.String(), `"caption_count"`) || !strings.Contains(first.String(), `"map_count": 2`) {
		t.Fatal("compiled picker catalog omitted structured image/caption/map cardinality")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalog contract test")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "runtime-animation-catalog.v1.schema.json")
	if err := schemaval.ValidateFile(first.Bytes(), schemaPath); err != nil {
		t.Fatalf("compiled picker payload violates its versioned JSON Schema: %v", err)
	}
}
