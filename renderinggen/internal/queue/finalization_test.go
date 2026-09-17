package queue

import "testing"

// copySafeArtifact is the certification a completed chunk carries on the
// production lane: structurally probed (copy_eligible, closed GOP, first frame
// keyframe). Tests that need a specific failure mutate one fact.
func copySafeArtifact(storageKey string) *Artifact {
	return &Artifact{StorageKey: storageKey, CopyEligible: true, ClosedGOP: true, FirstFrameKeyframe: true}
}

func child(index int, start, end int64, state State) *Job {
	return &Job{ChunkIndex: index, FrameRange: &FrameRange{Start: start, End: end}, State: state, Artifact: copySafeArtifact("sha")}
}

func TestValidateChildrenAcceptsCompleteContiguousChunks(t *testing.T) {
	children := []*Job{child(0, 0, 240, StateCompleted), child(1, 240, 480, StateCompleted), child(2, 480, 720, StateCompleted)}
	if err := ValidateChildren(children, 0, 720); err != nil {
		t.Fatal(err)
	}
}

func TestValidateChildrenRejectsMissingChunk(t *testing.T) {
	// A missing middle chunk breaks the dense index + coverage contract even
	// without a separate expected-count parameter.
	children := []*Job{child(0, 0, 240, StateCompleted), child(2, 480, 720, StateCompleted)}
	if err := ValidateChildren(children, 0, 720); err == nil {
		t.Fatal("expected missing chunk rejection")
	}
}

func TestValidateChildrenRejectsGap(t *testing.T) {
	children := []*Job{child(0, 0, 240, StateCompleted), child(1, 300, 480, StateCompleted)}
	if err := ValidateChildren(children, 0, 480); err == nil {
		t.Fatal("expected gap rejection")
	}
}

func TestValidateChildrenRejectsOverlap(t *testing.T) {
	children := []*Job{child(0, 0, 300, StateCompleted), child(1, 240, 480, StateCompleted)}
	if err := ValidateChildren(children, 0, 480); err == nil {
		t.Fatal("expected overlap rejection")
	}
}

func TestValidateChildrenRejectsIncompleteArtifact(t *testing.T) {
	c := child(0, 0, 10, StateRunning)
	if err := ValidateChildren([]*Job{c}, 0, 10); err == nil {
		t.Fatal("expected incomplete rejection")
	}
}

// TestValidateChildrenRejectsUnsafeCopyFacts is the copy-safety half of the
// gate. The parent is assembled by copying packets without decoding, so each of
// these three facts being unproven must refuse the family — the alternative is
// a silently broken concat boundary.
func TestValidateChildrenRejectsUnsafeCopyFacts(t *testing.T) {
	cases := map[string]func(*Artifact){
		"not copy eligible":        func(a *Artifact) { a.CopyEligible = false },
		"open or unproven GOP":     func(a *Artifact) { a.ClosedGOP = false },
		"first frame not keyframe": func(a *Artifact) { a.FirstFrameKeyframe = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			unsafe := child(0, 0, 10, StateCompleted)
			mutate(unsafe.Artifact)
			if err := ValidateChildren([]*Job{unsafe}, 0, 10); err == nil {
				t.Fatalf("%s must refuse the chunk family", name)
			}
		})
	}
}
