package overlay

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestRuntimeMotionCatalogIsCompleteStableAndDefensive(t *testing.T) {
	first := RuntimeMotionCatalog()
	if len(first) == 0 || len(first) != len(motion.Registry.List()) {
		t.Fatalf("runtime inventory has %d motions, canonical registry has %d", len(first), len(motion.Registry.List()))
	}
	for i, id := range motion.Registry.List() {
		if first[i].ID != id {
			t.Fatalf("runtime inventory order at %d = %q, want %q", i, first[i].ID, id)
		}
	}
	first[0].ID = "mutated"
	if RuntimeMotionCatalog()[0].ID == "mutated" {
		t.Fatal("runtime inventory caller mutated the canonical catalog view")
	}
	families := RuntimeMotionFamilies()
	if len(RuntimeMotionFamilyIDs()) == 0 || len(families) == 0 {
		t.Fatal("runtime catalog did not expose family groups")
	}
	for family, options := range families {
		for _, option := range options {
			wantFamily := option.Family
			if wantFamily == "" {
				wantFamily = "uncategorized"
			}
			if wantFamily != family {
				t.Fatalf("family %q contains motion %q in category %q", family, option.ID, option.Family)
			}
		}
	}
}

func TestRuntimeAnimationUseCasesSeparatePhraseSingleCompositeAndCaption(t *testing.T) {
	useCases := RuntimeAnimationUseCases()
	byID := make(map[string]RuntimeAnimationUseCase, len(useCases))
	for _, useCase := range useCases {
		if _, exists := byID[useCase.ID]; exists {
			t.Fatalf("duplicate runtime animation use case %q", useCase.ID)
		}
		byID[useCase.ID] = useCase
		if len(useCase.MotionIDs) == 0 {
			t.Errorf("use case %q has no selectable motion IDs", useCase.ID)
		}
		for _, id := range useCase.MotionIDs {
			if _, err := motion.Registry.Resolve(id); err != nil {
				t.Errorf("use case %q contains unknown motion %q", useCase.ID, id)
			}
		}
	}
	for _, id := range []string{"important_phrase", "short_important_phrase", "single_image", "composite_image_layer", "image_stack", "entity_caption"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("runtime use case %q missing", id)
		}
	}
	if byID["single_image"].Cardinality != "one image" || byID["composite_image_layer"].Cardinality != "two or more image_layers" {
		t.Fatalf("single/composite cardinalities unclear: %q / %q", byID["single_image"].Cardinality, byID["composite_image_layer"].Cardinality)
	}
	if !containsString(byID["important_phrase"].ItemKinds, string(KindImportantPhrase)) || !containsString(byID["entity_caption"].ItemKinds, string(KindEntityImage)) {
		t.Fatal("runtime use cases are not connected to semantic item kinds")
	}
}

func TestRuntimeMotionCatalogSurfacesUndeclaredLegacyTargets(t *testing.T) {
	foundLegacy := false
	for _, option := range RuntimeMotionCatalog() {
		if !option.TargetsDeclared {
			foundLegacy = true
			if len(option.Targets) != 0 {
				t.Fatalf("motion %q has undeclared targets but exposes %v", option.ID, option.Targets)
			}
		}
	}
	if !foundLegacy {
		t.Fatal("expected legacy catalog entries with undeclared targets")
	}
}

func TestAnimationPolicyUsesDeclaredTargetsAndLegacyFallbackOnly(t *testing.T) {
	for _, tc := range []struct {
		target string
		id     string
		want   bool
	}{
		{target: "important_phrase", id: "text_fade_up", want: true},
		{target: "important_phrase", id: "image_focus_reveal", want: false},
		{target: "important_phrase", id: "typewriter_clean", want: true},
		{target: "image", id: "image_focus_reveal", want: true},
		{target: "image", id: "text_fade_up", want: false},
		{target: "image", id: "brush_arrow_point", want: true},
	} {
		if got := motionAdmitsTarget(tc.id, tc.target); got != tc.want {
			t.Errorf("motionAdmitsTarget(%q, %q) = %v, want %v", tc.id, tc.target, got, tc.want)
		}
	}
	if !motionAdmitsTarget("map_image_italy_beacon_arrival", "map_view") {
		t.Error("catalog map motion was not admitted for the map viewport")
	}
	if motionAdmitsTarget("image_depth_float", "map_view") {
		t.Error("camera/depth image motion was admitted for a georeferenced map viewport")
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
	if decoded.SchemaVersion != 1 || len(decoded.Motions) != len(motion.Registry.List()) || len(decoded.UseCases) != len(RuntimeAnimationUseCases()) {
		t.Fatalf("compiled UI catalog is incomplete: schema=%d motions=%d use_cases=%d", decoded.SchemaVersion, len(decoded.Motions), len(decoded.UseCases))
	}
	if !strings.Contains(first.String(), `"use_cases"`) || !strings.Contains(first.String(), `"targets_declared"`) {
		t.Fatal("compiled UI catalog omitted semantic use cases or legacy target status")
	}
}
