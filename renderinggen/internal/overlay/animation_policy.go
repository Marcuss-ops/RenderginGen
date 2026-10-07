package overlay

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type animationPolicySelector struct {
	target, groupID, subgroupID string
}

// resolveAnimationPolicies expands plan-level animation defaults onto semantic
// instances before the ordinary per-kind lowering. Subgroup rules take
// precedence over group rules; explicit item/child motion ids always win.
func resolveAnimationPolicies(src *semanticPlan) error {
	policies := make(map[animationPolicySelector]semanticAnimationPolicy, len(src.AnimationPolicies))
	for index, policy := range src.AnimationPolicies {
		if policy.Target != "important_phrase" && policy.Target != "image" && policy.Target != "caption" {
			return fmt.Errorf("overlay: animation_policies[%d] has unsupported target %q", index, policy.Target)
		}
		// validateAnimationGroupIDs first: a subgroup_id without group_id is a
		// different producer bug than a missing group_id and must be named as such.
		if err := validateAnimationGroupIDs(policy.GroupID, policy.SubgroupID); err != nil {
			return fmt.Errorf("overlay: animation_policies[%d]: %w", index, err)
		}
		if policy.GroupID == "" {
			return fmt.Errorf("overlay: animation_policies[%d] requires group_id", index)
		}
		if strings.TrimSpace(policy.MotionID) == "" || strings.TrimSpace(policy.MotionID) != policy.MotionID {
			return fmt.Errorf("overlay: animation_policies[%d] requires a trimmed motion_id", index)
		}
		if !motionAdmitsTarget(policy.MotionID, policy.Target) {
			return fmt.Errorf("overlay: animation_policies[%d] motion %q is not supported for target %q", index, policy.MotionID, policy.Target)
		}
		if _, _, err := motionWindows(policy.MotionParams, 0); err != nil {
			return fmt.Errorf("overlay: animation_policies[%d]: %w", index, err)
		}
		selector := animationPolicySelector{policy.Target, policy.GroupID, policy.SubgroupID}
		if _, exists := policies[selector]; exists {
			return fmt.Errorf("overlay: duplicate animation policy for target %q group %q subgroup %q", policy.Target, policy.GroupID, policy.SubgroupID)
		}
		policies[selector] = policy
	}

	for itemIndex := range src.Items {
		item := &src.Items[itemIndex]
		if err := validateAnimationGroupIDs(item.GroupID, item.SubgroupID); err != nil {
			return fmt.Errorf("overlay: item %q: %w", item.ID, err)
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return err
		}
		if isTextLikeKind(kind) && len(item.Assets) == 0 && len(item.ImageLayers) == 0 && item.MotionID == "" {
			if policy, ok := selectAnimationPolicy(policies, "important_phrase", item.GroupID, item.SubgroupID); ok {
				item.MotionID = policy.MotionID
				item.MotionParams = cloneMotionParams(policy.MotionParams)
			}
		}
		if strings.TrimSpace(item.EntityCaption) != "" && item.CaptionMotionID == "" {
			if policy, ok := selectAnimationPolicy(policies, "caption", item.GroupID, item.SubgroupID); ok {
				item.CaptionMotionID = policy.MotionID
				item.CaptionMotionParams = cloneMotionParams(policy.MotionParams)
			}
		}
		if strings.TrimSpace(item.EntityCaption) != "" && item.CaptionMotionID != "" && !motionAdmitsTarget(item.CaptionMotionID, "caption") {
			return fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", item.ID, item.CaptionMotionID)
		}
		if len(item.ImageLayers) > 0 {
			if err := validateAnimationGroupIDs(item.GroupID, item.SubgroupID); err != nil {
				return fmt.Errorf("overlay: item %q: %w", item.ID, err)
			}
			if item.MotionID == "" {
				if policy, ok := selectAnimationPolicy(policies, "image", item.GroupID, item.SubgroupID); ok && animationIsImageStack(policy.MotionID) {
					item.MotionID = policy.MotionID
					item.MotionParams = cloneMotionParams(policy.MotionParams)
				}
			}
			stackMotion := animationIsImageStack(item.MotionID)
			for childIndex := range item.ImageLayers {
				child := &item.ImageLayers[childIndex]
				if child.GroupID == "" {
					child.GroupID = item.GroupID
					if child.SubgroupID == "" {
						child.SubgroupID = item.SubgroupID
					}
				} else if item.GroupID != "" && child.GroupID == item.GroupID && child.SubgroupID == "" {
					child.SubgroupID = item.SubgroupID
				}
				if err := validateAnimationGroupIDs(child.GroupID, child.SubgroupID); err != nil {
					return fmt.Errorf("overlay: item %q image layer %q: %w", item.ID, child.ID, err)
				}
				if child.MotionID == "" && !stackMotion {
					if policy, ok := selectAnimationPolicy(policies, "image", child.GroupID, child.SubgroupID); ok && !animationIsImageStack(policy.MotionID) {
						child.MotionID = policy.MotionID
						child.MotionParams = cloneMotionParams(policy.MotionParams)
					}
				}
				if strings.TrimSpace(child.Caption) != "" && child.CaptionMotionID == "" {
					if policy, ok := selectAnimationPolicy(policies, "caption", child.GroupID, child.SubgroupID); ok {
						child.CaptionMotionID = policy.MotionID
						child.CaptionMotionParams = cloneMotionParams(policy.MotionParams)
					}
				}
				if strings.TrimSpace(child.Caption) != "" && child.CaptionMotionID != "" && !motionAdmitsTarget(child.CaptionMotionID, "caption") {
					return fmt.Errorf("overlay: item %q image layer %q caption motion %q is not supported for text captions", item.ID, child.ID, child.CaptionMotionID)
				}
			}
			continue
		}

		if len(item.ImageLayers) == 0 && item.MotionID == "" && len(item.Assets) > 0 && (isImageKind(kind) || isEntityKind(kind)) {
			if policy, ok := selectAnimationPolicy(policies, "image", item.GroupID, item.SubgroupID); ok {
				item.MotionID = policy.MotionID
				item.MotionParams = cloneMotionParams(policy.MotionParams)
			}
		}
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

func selectAnimationPolicy(policies map[animationPolicySelector]semanticAnimationPolicy, target, groupID, subgroupID string) (semanticAnimationPolicy, bool) {
	if groupID == "" {
		return semanticAnimationPolicy{}, false
	}
	if subgroupID != "" {
		if policy, ok := policies[animationPolicySelector{target, groupID, subgroupID}]; ok {
			return policy, true
		}
	}
	policy, ok := policies[animationPolicySelector{target, groupID, ""}]
	return policy, ok
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

// isTextLikeKind derives policy eligibility from the semantic registry's
// rendering behavior instead of maintaining another list of text-oriented
// kinds. Entity cards retain text behavior when compiled without an image.
func isTextLikeKind(kind ItemKind) bool {
	behavior := behaviorOf(kind)
	return behavior == behaviorText || behavior == behaviorEntity
}

func animationIsImageStack(id string) bool {
	definition, err := premiumImageDefinition(id)
	return err == nil && definition != nil && definition.ImageRecipe != nil && definition.ImageRecipe.Stack
}
