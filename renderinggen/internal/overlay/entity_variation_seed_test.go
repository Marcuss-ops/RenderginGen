package overlay

import (
	"fmt"
	"testing"
)

func TestEntityVariationSeedKeepsDefaultSelection(t *testing.T) {
	plain := &semanticPlan{PlanID: "plan-a"}
	if got := plain.entityStyleSampleKey(); got != "plan-a" {
		t.Fatalf("no seed must sample from plan_id unchanged, got %q", got)
	}
	seeded := &semanticPlan{PlanID: "plan-a", EntityVariationSeed: "  run-2  "}
	if seeded.entityStyleSampleKey() == "plan-a" {
		t.Fatal("a non-empty seed must change the sampling key")
	}
	if seeded.entityStyleSampleKey() == (&semanticPlan{PlanID: "plan-a", EntityVariationSeed: "run-3"}).entityStyleSampleKey() {
		t.Fatal("different seeds must produce different sampling keys")
	}
}

func TestEntityVariationSeedResamplesStyles(t *testing.T) {
	sample := func(src *semanticPlan) map[string]bool {
		seen := map[string]bool{}
		for i := 0; i < 64; i++ {
			style, ok := ResolveEntityStyle("random", src.entityStyleSampleKey(), src.VideoID, fmt.Sprintf("item-%d", i))
			if !ok {
				t.Fatal("random selector must resolve")
			}
			seen[style.ID] = true
		}
		return seen
	}
	base := sample(&semanticPlan{PlanID: "plan-a", VideoID: "video-a"})
	seeded := sample(&semanticPlan{PlanID: "plan-a", VideoID: "video-a", EntityVariationSeed: "run-2"})
	differs := false
	for id := range seeded {
		if !base[id] {
			differs = true
		}
	}
	if !differs && len(seeded) == len(base) {
		// Same ID sets can still differ in order; compare the per-item pick.
		for i := 0; i < 64 && !differs; i++ {
			a, _ := ResolveEntityStyle("random", "plan-a", "video-a", fmt.Sprintf("item-%d", i))
			b, _ := ResolveEntityStyle("random", (&semanticPlan{PlanID: "plan-a", EntityVariationSeed: "run-2"}).entityStyleSampleKey(), "video-a", fmt.Sprintf("item-%d", i))
			differs = a.ID != b.ID
		}
	}
	if !differs {
		t.Fatal("a variation seed must change at least one entity style choice across 64 items")
	}
}

// TestEntityStyleSampleKeyIgnoresRunIdentity pins the retry-idempotence rule:
// plan ids are per-run, so the sampler must key on the plan's stable creative
// identity (the producer project) — or every retry re-rolls the "random"
// entity styles, changing the look of an unchanged video and defeating the
// content-addressed render cache, whose render keys cover the sampled style.
func TestEntityStyleSampleKeyIgnoresRunIdentity(t *testing.T) {
	first := &semanticPlan{PlanID: "run-111", ProjectID: "project-1"}
	retry := &semanticPlan{PlanID: "run-222", ProjectID: "project-1"}
	if first.entityStyleSampleKey() != retry.entityStyleSampleKey() {
		t.Fatalf("the same project must sample identically across runs: %q vs %q",
			first.entityStyleSampleKey(), retry.entityStyleSampleKey())
	}
	other := &semanticPlan{PlanID: "run-333", ProjectID: "project-2"}
	if first.entityStyleSampleKey() == other.entityStyleSampleKey() {
		t.Fatal("different projects must sample differently")
	}
	// No project: the plan id is the fallback identity (historic behaviour).
	noProjectA := &semanticPlan{PlanID: "run-111"}
	noProjectB := &semanticPlan{PlanID: "run-222"}
	if noProjectA.entityStyleSampleKey() == noProjectB.entityStyleSampleKey() {
		t.Fatal("without a project, distinct plan ids must still sample differently")
	}
	// The explicit re-roll knob keeps working: a seed changes the key, and the
	// same project+seed samples identically across runs.
	seededRetry := &semanticPlan{PlanID: "run-222", ProjectID: "project-1", EntityVariationSeed: "roll-1"}
	if first.entityStyleSampleKey() == seededRetry.entityStyleSampleKey() {
		t.Fatal("an explicit seed must change the sampling key")
	}
	seededRetry2 := &semanticPlan{PlanID: "run-333", ProjectID: "project-1", EntityVariationSeed: "roll-1"}
	if seededRetry.entityStyleSampleKey() != seededRetry2.entityStyleSampleKey() {
		t.Fatal("the same project and seed must sample identically across runs")
	}
}
