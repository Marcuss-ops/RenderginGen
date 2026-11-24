package overlay

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// pinGroupMotionIDs validates the composite group floor before the v2 animation
// groups run. It owns the part of the retired animation-policy pass that is
// still live: a certified group_motion_id is authoritative, must not conflict
// with an explicit motion_id, and must name a certified multi-image recipe.
//
// The historical plan-level animation_policies default resolver was removed on
// 2026-11-18. PipelineGen no longer emits that pre-v2 wire and no saved plan in
// the workspace carries it (docs/operations/animation-policies-census-2026-11-18.md).
func pinGroupMotionIDs(src *semanticPlan) error {
	for itemIndex := range src.Items {
		item := &src.Items[itemIndex]
		if err := validateAnimationGroupIDs(item.GroupID, item.SubgroupID); err != nil {
			return fmt.Errorf("overlay: item %q: %w", item.ID, err)
		}
		if len(item.ImageLayers) == 0 || item.GroupMotionID == "" {
			continue
		}
		if item.MotionID != "" && item.MotionID != item.GroupMotionID {
			return fmt.Errorf("overlay: item %q motion_id %q conflicts with group_motion_id %q", item.ID, item.MotionID, item.GroupMotionID)
		}
		if !animationUsesGroupRecipe(item.GroupMotionID) {
			return fmt.Errorf("overlay: item %q group_motion_id %q is not a certified multi-image motion", item.ID, item.GroupMotionID)
		}
		item.MotionID = item.GroupMotionID
	}
	return nil
}

func validateAnimationGroupIDs(groupID, subgroupID string) error {
	if subgroupID != "" && groupID == "" {
		return fmt.Errorf("subgroup_id requires group_id")
	}
	for _, value := range []struct{ name, value string }{{"group_id", groupID}, {"subgroup_id", subgroupID}} {
		if value.value == "" {
			continue
		}
		if strings.TrimSpace(value.value) != value.value || utf8.RuneCountInString(value.value) > 128 {
			return fmt.Errorf("%s must be trimmed and at most 128 characters", value.name)
		}
	}
	return nil
}

func cloneMotionParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}

// animationUsesGroupRecipe accepts only certified image_group_v1 choreographies,
// which carry a GroupRecipe rather than an ImageRecipe stack.
func animationUsesGroupRecipe(id string) bool {
	definition, err := resolveMotionDefinition(id)
	return err == nil && isImageGroupRecipe(definition)
}
