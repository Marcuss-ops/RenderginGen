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

// TestGroupMotionIDIsPinnedBeforeAnimationPolicies proves a policy declared for
// the same group cannot replace the group choreography chosen upstream.
func TestGroupMotionIDIsPinnedBeforeAnimationPolicies(t *testing.T) {
	src := &semanticPlan{
		Items: []semanticItem{groupPinItem("", "duo_split_reveal")},
		AnimationPolicies: []semanticAnimationPolicy{
			{Target: "image", GroupID: "g"},
		},
	}
	if err := resolveAnimationPolicies(src); err != nil {
		t.Fatalf("resolve pinned group motion: %v", err)
	}
	if got := src.Items[0].MotionID; got != "duo_split_reveal" {
		t.Fatalf("group motion replaced by policy: got %q want %q", got, "duo_split_reveal")
	}
}

func TestGroupMotionIDRejectsConflictingExplicitMotion(t *testing.T) {
	src := &semanticPlan{Items: []semanticItem{groupPinItem("duo_depth_stagger", "duo_split_reveal")}}
	if err := resolveAnimationPolicies(src); err == nil {
		t.Fatal("conflicting motion_id and group_motion_id must be rejected")
	}
}

func TestGroupMotionIDMustBeMultiImageMotion(t *testing.T) {
	src := &semanticPlan{Items: []semanticItem{groupPinItem("", "not_a_real_motion")}}
	if err := resolveAnimationPolicies(src); err == nil {
		t.Fatal("group_motion_id that is not a certified multi-image motion must be rejected")
	}
}
