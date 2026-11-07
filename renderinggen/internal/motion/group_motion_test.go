package motion

import "testing"

func TestReusableImageGroupTransitionsIgnoreImageCardinality(t *testing.T) {
	ids := ReusableImageGroupMotionIDs()
	if len(ids) != 14 {
		t.Fatalf("reusable image-group inventory has %d motions, want 14: %v", len(ids), ids)
	}
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		definition := plugin.(DeclarativePlugin).Definition
		recipe := definition.GroupRecipe
		if recipe == nil || !recipe.ApplyToAllMembers {
			t.Errorf("%s is not a reusable group animation: %+v", id, recipe)
			continue
		}
		for count := 2; count <= 5; count++ {
			if !recipe.SupportsCount(count) || len(recipe.TracksForMember(count-1)) != 4 {
				t.Errorf("%s does not supply the same four tracks for group size %d", id, count)
			}
		}
		if err := ValidateDefinition(definition); err != nil {
			t.Errorf("%s is invalid: %v", id, err)
		}
	}
}

func TestImageGroupCatalogKeepsCountSpecificReplaySeparate(t *testing.T) {
	all := Registry.CategoryMotionIDs("image_group_v1")
	if len(all) != 35 {
		t.Fatalf("image_group_v1 catalog has %d motions, want 35", len(all))
	}
	reusable := ReusableImageGroupMotionIDs()
	if len(reusable) != 14 {
		t.Fatalf("reusable image-group inventory has %d motions, want 14: %v", len(reusable), reusable)
	}
	reusableSet := make(map[string]bool, len(reusable))
	for _, id := range reusable {
		reusableSet[id] = true
	}
	counts := map[int]int{}
	for _, id := range all {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %q: %v", id, err)
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok || declarative.Definition.GroupRecipe == nil {
			t.Fatalf("%q has no group recipe", id)
		}
		recipe := declarative.Definition.GroupRecipe
		if recipe.ApplyToAllMembers {
			continue
		}
		if reusableSet[id] {
			t.Errorf("count-specific motion %q leaked into the reusable inventory", id)
		}
		counts[recipe.ImageCount]++
	}
	want := map[int]int{2: 9, 3: 4, 4: 4, 5: 4}
	for count, wantCount := range want {
		if counts[count] != wantCount {
			t.Errorf("count-specific image groups with %d images = %d, want %d", count, counts[count], wantCount)
		}
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	if total != 21 {
		t.Errorf("count-specific image-group rows = %d, want 21", total)
	}
}
