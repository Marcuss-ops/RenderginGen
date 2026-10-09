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
