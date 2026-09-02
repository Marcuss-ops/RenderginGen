package queue

import "testing"

func child(index int, start, end int64, state State) *Job {
	return &Job{ChunkIndex: index, FrameRange: &FrameRange{Start: start, End: end}, State: state, Artifact: &Artifact{StorageKey: "sha"}}
}

func TestValidateChildrenAcceptsCompleteContiguousChunks(t *testing.T) {
	children := []*Job{child(0, 0, 240, StateCompleted), child(1, 240, 480, StateCompleted), child(2, 480, 720, StateCompleted)}
	if err := ValidateChildren(children, 3, 0, 720); err != nil {
		t.Fatal(err)
	}
}

func TestValidateChildrenRejectsMissingChunk(t *testing.T) {
	children := []*Job{child(0, 0, 240, StateCompleted), child(2, 480, 720, StateCompleted)}
	if err := ValidateChildren(children, 3, 0, 720); err == nil {
		t.Fatal("expected missing chunk rejection")
	}
}

func TestValidateChildrenRejectsGap(t *testing.T) {
	children := []*Job{child(0, 0, 240, StateCompleted), child(1, 300, 480, StateCompleted)}
	if err := ValidateChildren(children, 2, 0, 480); err == nil {
		t.Fatal("expected gap rejection")
	}
}

func TestValidateChildrenRejectsOverlap(t *testing.T) {
	children := []*Job{child(0, 0, 300, StateCompleted), child(1, 240, 480, StateCompleted)}
	if err := ValidateChildren(children, 2, 0, 480); err == nil {
		t.Fatal("expected overlap rejection")
	}
}

func TestValidateChildrenRejectsIncompleteArtifact(t *testing.T) {
	c := child(0, 0, 10, StateRunning)
	if err := ValidateChildren([]*Job{c}, 1, 0, 10); err == nil {
		t.Fatal("expected incomplete rejection")
	}
}
