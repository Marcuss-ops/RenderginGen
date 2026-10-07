package motion

import (
	"strings"
	"testing"
)

func TestDeprecationKeepsLegacyResolveButRemovesPickerSelection(t *testing.T) {
	registry := NewRegistry()
	for _, id := range []string{"legacy_motion", "replacement_motion"} {
		definition := MotionDefinition{ID: id, Category: "fixture", Targets: []string{"text"}}
		if err := registry.Register(id, DeclarativePlugin{Definition: definition}); err != nil {
			t.Fatal(err)
		}
	}
	if got := registry.SelectableCategoryMotionIDs("fixture"); len(got) != 2 {
		t.Fatalf("initial picker choices = %v, want both fixture motions", got)
	}
	deprecation := Deprecation{
		MotionID: "legacy_motion", RemoveAfter: "2099-01-01",
		Reason: "fixture migration", Replacement: "replacement_motion",
	}
	if err := registry.MarkDeprecated(deprecation); err != nil {
		t.Fatal(err)
	}
	if got := Registry.Selectable("typewriter_modern_01_monospace_block_cursor"); got != true {
		t.Fatal("fixture-only isolated deprecation contaminated the production registry")
	}
	if got := registry.SelectableCategoryMotionIDs("fixture"); len(got) != 1 || got[0] != "replacement_motion" {
		t.Fatalf("deprecated ID remains selectable: %v", got)
	}
	if got := registry.CategoryMotionIDs("fixture"); len(got) != 2 {
		t.Fatalf("compatibility inventory lost a legacy ID: %v", got)
	}
	if _, err := registry.Resolve("legacy_motion"); err != nil {
		t.Fatalf("saved legacy plan must still resolve during the compatibility window: %v", err)
	}
	if reason, ok := registry.DeprecationReason("legacy_motion"); !ok || !strings.Contains(reason, "fixture migration") {
		t.Fatalf("deprecation notice = %q, present=%v", reason, ok)
	}
	if err := registry.MarkDeprecated(deprecation); err == nil {
		t.Fatal("duplicate deprecation record must fail")
	}
}

func TestDeprecationRejectsUnknownAndInvalidReplacement(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("legacy_motion", DeclarativePlugin{Definition: MotionDefinition{ID: "legacy_motion"}}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []Deprecation{
		{MotionID: "missing", RemoveAfter: "2099-01-01", Reason: "x"},
		{MotionID: "legacy_motion", RemoveAfter: "", Reason: "x"},
		{MotionID: "legacy_motion", RemoveAfter: "2099-01-01", Reason: "x", Replacement: "missing"},
		{MotionID: "legacy_motion", RemoveAfter: "2099-01-01", Reason: "x", Replacement: "legacy_motion"},
	} {
		if err := registry.MarkDeprecated(value); err == nil {
			t.Errorf("MarkDeprecated(%+v) unexpectedly succeeded", value)
		}
	}
}
