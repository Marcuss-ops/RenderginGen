package metricnames

import (
	"sort"
	"strings"
	"testing"
)

// TestUnitsAgreeWithNameSuffix pins the contract the queue's processing_metrics
// projection relies on: the unit is encoded in the name. A declared unit that
// contradicts the suffix would be persisted under the wrong unit (the queue
// derives it with metricUnit(name)).
func TestUnitsAgreeWithNameSuffix(t *testing.T) {
	for name, unit := range vocab {
		want := ""
		switch {
		case strings.HasSuffix(name, "_us"):
			want = UnitUS
		case strings.HasSuffix(name, "_ms"):
			want = UnitMS
		case strings.HasSuffix(name, "bytes"):
			want = UnitBytes
		}
		if want != "" && want != unit {
			t.Errorf("%s: declared unit %q contradicts the name suffix (want %q)", name, unit, want)
		}
		if want == "" && (unit == UnitUS || unit == UnitMS || unit == UnitBytes) {
			t.Errorf("%s: declared unit %q but the name carries no matching suffix — the queue would persist it as a count", name, unit)
		}
	}
}

// TestVocabularyIsUniqueAndSorted pins that the exported list is stable, so a
// consumer can diff it and a duplicate constant cannot hide another name.
func TestVocabularyIsUniqueAndSorted(t *testing.T) {
	all := All()
	sort.Strings(all)
	seen := map[string]bool{}
	for _, name := range all {
		if name == "" {
			t.Error("empty metric name in the vocabulary")
		}
		if seen[name] {
			t.Errorf("duplicate metric name %q in the vocabulary", name)
		}
		seen[name] = true
	}
	if len(all) != len(vocab) {
		t.Fatalf("All() returned %d names for %d vocabulary entries", len(all), len(vocab))
	}
}

// TestUndeclaredNamesAreRejected pins that the vocabulary is the gate for
// typos: an invented name is not silently accepted as a metric.
func TestUndeclaredNamesAreRejected(t *testing.T) {
	for _, name := range []string{"", "render_micros", "overlay_compile", "unknown_templates_us"} {
		if Declared(name) {
			t.Errorf("%q must not be a declared metric name", name)
		}
	}
	for _, name := range []string{OverlayCompileUS, TotalUS, UnknownTemplates, FPS} {
		if !Declared(name) {
			t.Errorf("%q must be declared", name)
		}
	}
}

// TestChrononPrefixedNamesHaveTheirOwner pins that the engine telemetry
// projection is recognized without the worker claiming to own its unit, and
// that a worker-declared name carrying the same prefix keeps its own unit.
func TestChrononPrefixedNamesHaveTheirOwner(t *testing.T) {
	for _, name := range []string{ChrononSummaryPrefix + "p50_frame_ms", ChrononTimingPrefix + "frames"} {
		if !Declared(name) {
			t.Errorf("%q must be recognized as a projected Chronon telemetry key", name)
		}
		if _, ok := vocab[name]; ok {
			t.Errorf("%q must not be declared as a worker-owned metric", name)
		}
	}
}
