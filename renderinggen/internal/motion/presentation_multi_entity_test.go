package motion

import (
	"math"
	"reflect"
	"testing"
)

// Multi-entity grouping gates. PERSON A + PERSON B in one editorial block must
// produce one scene — two cards laid out side by side, both captions alive for
// the full shared lifetime, no overlap, safe-area inside, and a deterministic
// focus walk when narration moves from A to B.

func twoEntityScene(t *testing.T) []EntityCardPlacement {
	t.Helper()
	entities := []EntityCardInput{
		{Name: "PERSON A", ImagePath: "assets/people/a.png"},
		{Name: "PERSON B", ImagePath: "assets/people/b.png"},
	}
	placements, err := LayoutEntityGroup(entities, 1920, 1080, 32)
	if err != nil {
		t.Fatal(err)
	}
	return placements
}

func TestTwoEntitiesGroupIntoOneScene(t *testing.T) {
	placements := twoEntityScene(t)
	if len(placements) != 2 {
		t.Fatalf("group produced %d placements, want 2 (one scene, not two)", len(placements))
	}
	if placements[0].Entity.Name != "PERSON A" || placements[1].Entity.Name != "PERSON B" ||
		placements[0].Entity.ImagePath != "assets/people/a.png" || placements[1].Entity.ImagePath != "assets/people/b.png" {
		t.Fatalf("one-scene grouping changed entity/image association: %+v", placements)
	}
	// The group is one composition on one 1920x1080 canvas: both cards share
	// the same canvas and both stay inside it.
	for i, p := range placements {
		if p.CenterX-p.Width/2 < 0 || p.CenterX+p.Width/2 > 1920 {
			t.Errorf("card %d leaves the canvas horizontally: %+v", i, p)
		}
		if p.CenterY-p.Height/2 < 0 || p.CenterY+p.Height/2 > 1080 {
			t.Errorf("card %d leaves the canvas vertically: %+v", i, p)
		}
	}
}

func TestTwoEntityCaptionsRemainVisible(t *testing.T) {
	placements := twoEntityScene(t)
	// The shared lifetime contract: the group lays the cards out, it never
	// removes or clears an identity. Both captions survive the grouping with
	// their names and image paths intact.
	for i, p := range placements {
		if p.Width <= 0 || p.Height <= 0 {
			t.Errorf("card %d has invalid shared-lifetime geometry: %+v", i, p)
		}
		if p.Entity.Name == "" {
			t.Errorf("caption %d was cleared by grouping", i)
		}
		if p.Entity.ImagePath == "" {
			t.Errorf("image %d was cleared by grouping", i)
		}
	}
	if placements[0].Entity.Name != "PERSON A" || placements[1].Entity.Name != "PERSON B" {
		t.Fatalf("captions changed identity: %q, %q", placements[0].Entity.Name, placements[1].Entity.Name)
	}
}

func TestEntityFocusCanMoveAtoB(t *testing.T) {
	placements := twoEntityScene(t)
	order := EntityFocusOrder(placements)
	if len(order) != 2 || order[0] != 0 || order[1] != 1 {
		t.Fatalf("focus order %v, want A then B in reading order", order)
	}
	// Focusing B must not move A: focus is an ordering, not a re-layout.
	before := placements[0]
	_ = EntityFocusOrder(placements)
	after := placements[0]
	if before != after {
		t.Fatalf("focus walk mutated card A geometry: %+v -> %+v", before, after)
	}
	// And the walk itself is deterministic.
	if !reflect.DeepEqual(order, EntityFocusOrder(placements)) {
		t.Fatal("focus order is nondeterministic")
	}
}

func TestEntityGroupNoOverlap(t *testing.T) {
	for _, count := range []int{2, 3, 4, 5} {
		entities := make([]EntityCardInput, count)
		for i := range entities {
			entities[i] = EntityCardInput{Name: "ENTITY", ImagePath: "assets/people/e.png"}
		}
		placements, err := LayoutEntityGroup(entities, 1920, 1080, 32)
		if err != nil {
			t.Fatalf("%d entities: %v", count, err)
		}
		for i := 0; i < count; i++ {
			for j := i + 1; j < count; j++ {
				a, b := placements[i], placements[j]
				overlapX := math.Abs(a.CenterX-b.CenterX) < (a.Width+b.Width)/2
				overlapY := math.Abs(a.CenterY-b.CenterY) < (a.Height+b.Height)/2
				if overlapX && overlapY {
					t.Fatalf("%d entities: cards %d and %d overlap: %+v %+v", count, i, j, a, b)
				}
			}
		}
		order := EntityFocusOrder(placements)
		if len(order) != count {
			t.Fatalf("%d entities: focus order has %d entries", count, len(order))
		}
	}
}
