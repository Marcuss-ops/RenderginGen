package overlay

import "testing"

// TestTemplateRegistryIsSingleSourceOfKind pins the registry as the only owner
// of template → kind. Lowercase and uppercase spellings of the same concept
// must resolve identically.
func TestTemplateRegistryIsSingleSourceOfKind(t *testing.T) {
	cases := []struct {
		template string
		want     ItemKind
	}{
		{"PERSON", KindEntityCard},
		{"person_default", KindEntityCard},
		{"ORGANIZATION", KindOrganization},
		{"org_default", KindOrganization},
		{"LOCATION", KindLocation},
		{"gpe_default", KindLocation},
		{"IMPORTANT_PHRASE", KindImportantPhrase},
		{"IMPORTANT_WORD", KindImportantWord},
		{"NUMBER", KindNumber},
		{"MONEY", KindNumber},
		{"IMAGE_OVERLAY", KindEntityImage},
		{"PRODUCT", KindProduct},
		{"LOGO", KindLogo},
		{"LIGHT_LEAK", KindLightLeak},
		{"lower_third", KindLowerThird},
		{"image_popup", KindImagePopup},
		{"quote", KindQuote},
	}
	for _, tc := range cases {
		if got := templateSpecFor(tc.template).Kind; got != tc.want {
			t.Errorf("templateSpecFor(%q).Kind = %q, want %q", tc.template, got, tc.want)
		}
	}
	if got := templateSpecFor("SOME_UNKNOWN_TEMPLATE").Kind; got != KindPrimitive {
		t.Errorf("unknown template kind = %q, want %q", got, KindPrimitive)
	}
}

// TestTemplateRegistryPresetRequirement pins which templates must carry a
// PipelineGen-selected preset_id and which legitimately compile bare.
func TestTemplateRegistryPresetRequirement(t *testing.T) {
	for _, template := range []string{"PERSON", "ORGANIZATION", "LOCATION", "IMPORTANT_PHRASE", "IMPORTANT_WORD", "NUMBER", "IMAGE_OVERLAY", "QUOTE"} {
		if !templateSpecFor(template).RequiresPreset {
			t.Errorf("template %q must require a preset", template)
		}
	}
	for _, template := range []string{"PERSON_DEFAULT", "person_default", "org_default", "gpe_default", "concept_default", "lower_third", "image_popup", "PRODUCT", "LOGO", "LIGHT_LEAK"} {
		if templateSpecFor(template).RequiresPreset {
			t.Errorf("template %q must not require a preset", template)
		}
	}
}

// TestResolveKindFailClosed pins the kind SSOT rules: a kind that contradicts
// a known template is rejected; an absent kind defers to the registry; a
// matching kind is accepted.
func TestResolveKindFailClosed(t *testing.T) {
	spec := templateSpecFor("PERSON")
	if _, err := spec.resolveKind("important_phrase", "x"); err == nil {
		t.Fatal("a kind contradicting the template must be rejected fail-closed")
	}
	kind, err := spec.resolveKind("", "x")
	if err != nil || kind != KindEntityCard {
		t.Fatalf("empty kind must resolve to the registry kind: kind=%q err=%v", kind, err)
	}
	if kind, err := spec.resolveKind("entity_card", "x"); err != nil || kind != KindEntityCard {
		t.Fatalf("matching kind rejected: kind=%q err=%v", kind, err)
	}
}

// TestProducerKindSynonymsAreAccepted pins that PipelineGen's actual kind
// vocabulary ("image", "keyword", "text_phrase", "entity_image") is
// compatible with the registry's canonical kinds — the boundary validates
// behaviour, not exact spelling.
func TestProducerKindSynonymsAreAccepted(t *testing.T) {
	cases := []struct{ template, kind string }{
		{"IMAGE_OVERLAY", "image"},
		{"IMPORTANT_WORD", "keyword"},
		{"IMPORTANT_PHRASE", "text_phrase"},
		{"image_popup", "entity_image"},
		{"person_default", "entity_card"},
		{"gpe_default", "entity_card"},
		{"concept_default", "entity_card"},
		{"NUMBER", "number"},
		{"QUOTE", "quote"},
	}
	for _, tc := range cases {
		spec := templateSpecFor(tc.template)
		if _, err := spec.resolveKind(tc.kind, "x"); err != nil {
			t.Errorf("template %q kind %q rejected: %v", tc.template, tc.kind, err)
		}
	}
}

// TestProducerKindBehaviorMismatchRejected pins the fail-closed side: a kind
// whose behaviour contradicts the template is rejected.
func TestProducerKindBehaviorMismatchRejected(t *testing.T) {
	cases := []struct{ template, kind string }{
		{"PERSON", "important_phrase"},
		{"IMPORTANT_PHRASE", "entity_card"},
		{"IMAGE_OVERLAY", "important_word"},
		{"PRODUCT", "entity_card"},
	}
	for _, tc := range cases {
		spec := templateSpecFor(tc.template)
		if _, err := spec.resolveKind(tc.kind, "x"); err == nil {
			t.Errorf("template %q kind %q must be rejected", tc.template, tc.kind)
		}
	}
}

// TestCompileSemanticAcceptsPlannerVocabulary compiles a planner-shaped image
// item (kind "image" on IMAGE_OVERLAY) end to end.
func TestCompileSemanticAcceptsPlannerVocabulary(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[{"id":"image_overlay","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":0,"end_ms":1000,"params":{"position":"right"},
        "asset_refs":[{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/a.png","media_type":"image/png"}]}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("planner vocabulary must compile: %v", err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Type != "image" {
		t.Fatalf("layers = %+v", result.Plan.Layers)
	}
}

func TestCompileSemanticRejectsKindTemplateMismatch(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[{"id":"x","kind":"important_phrase","template_id":"PERSON","preset_id":"lower_third_safe","text":"Ada","start_ms":0,"end_ms":1000}]}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("kind/template mismatch must be rejected")
	}
}

// TestCompileSemanticReturnsStatsFromSamePass proves the ledger counters come
// out of the compile pass, not a second reading of the raw document.
func TestCompileSemanticReturnsStatsFromSamePass(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"e","entity_id":"entity:ada","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","text":"Ada","start_ms":0,"end_ms":1000,"duration_ms":1000},
        {"id":"p","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"hi","start_ms":1000,"end_ms":2000},
        {"id":"w","kind":"important_word","template_id":"IMPORTANT_WORD","preset_id":"active_word_pop","text":"WOW","start_ms":2000,"end_ms":3000},
        {"id":"i","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_scale_in","start_ms":3000,"end_ms":4000,
         "asset_refs":[{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/a.png","media_type":"image/png"}]}
      ]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	stats := result.Stats
	if stats.EntityCount != 1 || stats.ImportantPhraseCnt != 1 || stats.ImportantWordCnt != 1 || stats.ImageCount != 1 {
		t.Fatalf("stats from compile pass = %+v", stats)
	}
	if len(result.Plan.Layers) != 4 {
		t.Fatalf("layers = %d, want 4", len(result.Plan.Layers))
	}
}

// TestEntityCardLayerIDsAreDistinct pins the centralized layer-id rule: an
// entity card that emits image + text derives both ids from the item id, so
// Chronon can never collapse them into one layer.
func TestEntityCardLayerIDsAreDistinct(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[{"id":"jordan-42","entity_id":"person:michael-jordan","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","image_preset_id":"image_focus_in","text":"Michael Jordan","start_ms":0,"end_ms":1000,"duration_ms":1000,
        "asset_refs":[{"asset_id":"jordan","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/j.jpg","media_type":"image/jpeg"}]}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) != 2 {
		t.Fatalf("layers = %+v", result.Plan.Layers)
	}
	if result.Plan.Layers[0].ID != "jordan-42:image" || result.Plan.Layers[1].ID != "jordan-42:text" {
		t.Fatalf("layer ids = %q, %q", result.Plan.Layers[0].ID, result.Plan.Layers[1].ID)
	}
}
