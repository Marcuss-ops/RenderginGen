package overlay

import "testing"

func TestRuntimeMapLayoutsAreDetachedRegistryProjection(t *testing.T) {
	if len(mapLayoutCatalog) == 0 {
		t.Fatal("canonical map primitive registry is empty")
	}
	wantID := mapLayoutCatalog[0].ID
	layouts := runtimeMapLayouts()
	if len(layouts) != len(mapLayoutCatalog) || layouts[0].ID != wantID {
		t.Fatalf("projection = %+v, registry = %+v", layouts, mapLayoutCatalog)
	}
	layouts[0].ID = "mutated"
	layouts[0].ProjectedMotionTargets = append(layouts[0].ProjectedMotionTargets, "mutated")
	if mapLayoutCatalog[0].ID != wantID {
		t.Fatalf("projection mutation changed registry ID to %q", mapLayoutCatalog[0].ID)
	}
	for _, target := range mapLayoutCatalog[0].ProjectedMotionTargets {
		if target == "mutated" {
			t.Fatal("projection mutation changed registry motion targets")
		}
	}
	if layouts[3].ProjectedMotionTargets == nil {
		t.Fatal("empty projection arrays must serialize as [] rather than null")
	}
}
