package overlay

import (
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestDeprecatedMotionIsVisibleButNotSelectable(t *testing.T) {
	registry := motion.NewRegistry()
	for _, id := range []string{"legacy", "replacement"} {
		definition := motion.MotionDefinition{ID: id, Category: "fixture", Targets: []string{"short_phrase"}}
		if err := registry.Register(id, motion.DeclarativePlugin{Definition: definition}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.MarkDeprecated(motion.Deprecation{
		MotionID: "legacy", RemoveAfter: "2099-01-01", Reason: "fixture reason", Replacement: "replacement",
	}); err != nil {
		t.Fatal(err)
	}
	option := RuntimeMotionOption{ID: "legacy", Family: "fixture", Targets: []string{"short_phrase"}, TargetsDeclared: true, Deprecated: true, RemoveAfter: "2099-01-01", DeprecationNote: "fixture reason"}
	families := runtimeMotionFamilies([]RuntimeMotionOption{option, {ID: "replacement", Family: "fixture", Targets: []string{"short_phrase"}, TargetsDeclared: true}})
	if len(families["fixture"]) != 1 || families["fixture"][0].ID != "replacement" {
		t.Fatalf("deprecated motion still selectable in family projection: %+v", families)
	}
	groups := collectRuntimeAnimationMotionGroups(
		[]RuntimeMotionOption{option, {ID: "replacement", Targets: []string{"short_phrase"}, TargetsDeclared: true}},
		map[string]*motion.MotionDefinition{
			"legacy":      {ID: "legacy", Targets: []string{"short_phrase"}},
			"replacement": {ID: "replacement", Targets: []string{"short_phrase"}},
		},
	)
	if containsString(groups.shortPhrases, "legacy") || !containsString(groups.shortPhrases, "replacement") {
		t.Fatalf("deprecated motion not excluded from picker use case: %+v", groups.shortPhrases)
	}
	if option.RemoveAfter == "" || option.DeprecationNote == "" {
		t.Fatalf("deprecated diagnostic state missing: %+v", option)
	}
	if motion.Registry.Selectable("typewriter_modern_01_monospace_block_cursor") != true {
		t.Fatal("isolated deprecation must not modify the production registry")
	}
}

func TestDeprecatedMotionHasDeterministicSemanticCompileError(t *testing.T) {
	id := "typewriter_modern_01_monospace_block_cursor"
	reason := "fixture removal after the compatibility window"
	registry := motion.Registry
	motion.Registry = motion.NewRegistry()
	defer func() { motion.Registry = registry }()
	if err := motion.Registry.Register(id, motion.DeclarativePlugin{Definition: motion.MotionDefinition{
		ID: id, Category: "typewriter_modern_v1", Targets: []string{"short_phrase", "text"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := motion.Registry.Register("typewriter_modern_02_kinetic_scramble", motion.DeclarativePlugin{Definition: motion.MotionDefinition{
		ID: "typewriter_modern_02_kinetic_scramble", Category: "typewriter_modern_v1", Targets: []string{"short_phrase", "text"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := motion.Registry.MarkDeprecated(motion.Deprecation{
		MotionID: id, RemoveAfter: "2099-01-01", Reason: reason,
		Replacement: "typewriter_modern_02_kinetic_scramble",
	}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"retired-motion","video_id":"fixture","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"typewriter_modern_01_monospace_block_cursor","text":"Fixture","start_ms":0,"end_ms":3000}]}`)
	_, firstErr := CompileSemantic(raw)
	_, secondErr := CompileSemantic(raw)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("saved plan must remain compilable within its compatibility window: first=%v second=%v", firstErr, secondErr)
	}
	info, ok := motion.Registry.DeprecationInfo(id)
	if !ok {
		t.Fatal("isolated registry lost its deprecation record")
	}
	before := deprecationErrorAfter(id, info, time.Date(2098, 12, 31, 0, 0, 0, 0, time.UTC))
	atRemoval := deprecationErrorAfter(id, info, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	if before != nil {
		t.Fatalf("motion rejected before remove_after: %v", before)
	}
	if atRemoval == nil || !strings.Contains(atRemoval.Error(), "deprecated and no longer resolvable") || !strings.Contains(atRemoval.Error(), reason) {
		t.Fatalf("removal diagnostic = %v, want stable deprecation reason at boundary", atRemoval)
	}
	if second := deprecationErrorAfter(id, info, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)); second == nil || atRemoval.Error() != second.Error() {
		t.Fatalf("post-window diagnostic is not deterministic: first=%v second=%v", atRemoval, second)
	}
}
