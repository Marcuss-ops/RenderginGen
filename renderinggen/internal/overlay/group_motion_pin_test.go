package overlay

import "testing"

// groupPinItem is a two-image composite whose choreography was already chosen
// upstream by PipelineGen (group_motion_id).
func groupPinItem(motionID, groupMotionID string) semanticItem {
	return semanticItem{
		ID:            "group-1",
		Kind:          string(KindEntityImage),
		Template:      "IMAGE_POPUP",
		GroupID:       "g",
		MotionID:      motionID,
		GroupMotionID: groupMotionID,
		ImageLayers: []SemanticImageLayer{
			{ID: "a", AssetID: "asset-a"},
			{ID: "b", AssetID: "asset-b"},
		},
	}
}

func TestGroupMotionIDRejectsConflictingExplicitMotion(t *testing.T) {
	src := &semanticPlan{Items: []semanticItem{groupPinItem("duo_depth_stagger", "duo_split_reveal")}}
	if err := pinGroupMotionIDs(src); err == nil {
		t.Fatal("conflicting motion_id and group_motion_id must be rejected")
	}
}

func TestGroupMotionIDMustBeMultiImageMotion(t *testing.T) {
	src := &semanticPlan{Items: []semanticItem{groupPinItem("", "not_a_real_motion")}}
	if err := pinGroupMotionIDs(src); err == nil {
		t.Fatal("group_motion_id that is not a certified multi-image motion must be rejected")
	}
}

func TestGroupMotionIDIsPinnedOnTheItem(t *testing.T) {
	src := &semanticPlan{Items: []semanticItem{groupPinItem("", "duo_split_reveal")}}
	if err := pinGroupMotionIDs(src); err != nil {
		t.Fatalf("pin valid group motion: %v", err)
	}
	if got := src.Items[0].MotionID; got != "duo_split_reveal" {
		t.Fatalf("group motion not pinned: got %q want %q", got, "duo_split_reveal")
	}
}
