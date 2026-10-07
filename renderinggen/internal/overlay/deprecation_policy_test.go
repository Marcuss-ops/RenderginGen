package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestDeprecationPolicyIsImmediate pins the owner decision of 7 October 2026: a
// retired motion has no compatibility window and no alias, and the catalog
// publishes that policy instead of leaving a consumer to infer it.
func TestDeprecationPolicyIsImmediate(t *testing.T) {
	model := runtimeDeprecationModel()
	if model.Policy != deprecationPolicyImmediate || model.WindowDays != 0 || model.AliasesAllowed {
		t.Fatalf("deprecation policy = %+v, want immediate retirement with no window and no alias", model)
	}
	if strings.TrimSpace(model.Rule) == "" {
		t.Fatal("deprecation policy publishes no rule")
	}
	if model.Deprecated == nil {
		t.Fatal("deprecated must serialize as an array so the field cannot read as missing")
	}
	published := runtimeSelectionModel(nil, nil).Deprecation
	if published.Policy != deprecationPolicyImmediate || published.Deprecated == nil {
		t.Fatalf("selection model deprecation block = %+v", published)
	}
	if got, want := len(published.Deprecated), len(retiredRegistryIDs()); got != want {
		t.Fatalf("published deprecated list has %d entries, want one per retired registry ID (%d)", got, want)
	}
}

func retiredRegistryIDs() []string {
	var ids []string
	for _, id := range motion.Registry.List() {
		if _, ok := motion.Registry.DeprecationInfo(id); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestRetiredMotionLeavesThePickerAndFailsCompilation proves the immediate
// policy end to end on an isolated registry: the ID disappears from the
// selectable projections and a plan that still names it stops at compilation
// with a diagnostic naming it, instead of being rewritten to the replacement.
func TestRetiredMotionLeavesThePickerAndFailsCompilation(t *testing.T) {
	original := motion.Registry
	t.Cleanup(func() { motion.Registry = original })

	registry := motion.NewRegistry()
	for _, id := range []string{"retired_fixture", "replacement_fixture"} {
		definition := motion.MotionDefinition{ID: id, Category: "fixture", Targets: []string{"short_phrase", "text"}}
		if err := registry.Register(id, motion.DeclarativePlugin{Definition: definition}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.MarkDeprecated(motion.Deprecation{
		MotionID: "retired_fixture", RemoveAfter: "2026-10-07",
		Reason: "owner retired the fixture", Replacement: "replacement_fixture",
	}); err != nil {
		t.Fatal(err)
	}
	motion.Registry = registry

	if motionAdmitsTarget("retired_fixture", "short_phrase") {
		t.Error("retired motion is still admitted for a picker target")
	}
	if !motionAdmitsTarget("replacement_fixture", "short_phrase") {
		t.Error("replacement motion must stay selectable")
	}
	model := runtimeDeprecationModel()
	if len(model.Deprecated) != 1 {
		t.Fatalf("published deprecated list = %+v, want the retired fixture", model.Deprecated)
	}
	entry := model.Deprecated[0]
	if entry.MotionID != "retired_fixture" || entry.Replacement != "replacement_fixture" || strings.TrimSpace(entry.Reason) == "" {
		t.Fatalf("published deprecation entry = %+v", entry)
	}
	_, err := lowerMotionForTarget("retired_fixture", 12, 0, nil, "text", 60, false, "fixture-item", "short_phrase")
	if err == nil {
		t.Fatal("compiling a retired motion succeeded; owner policy is immediate retirement")
	}
	for _, want := range []string{"retired_fixture", "deprecated", "replacement_fixture"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic %q does not name %q", err, want)
		}
	}
	if _, err := lowerMotionForTarget("replacement_fixture", 12, 0, nil, "text", 60, false, "fixture-item", "short_phrase"); err != nil {
		t.Fatalf("replacement motion must still compile: %v", err)
	}
}
