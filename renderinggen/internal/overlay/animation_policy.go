package overlay

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

type animationPolicySelector struct {
	target, groupID, subgroupID string
}

// policyTargets is the closed set of motion targets an animation policy may
// name. Declared here, next to the policy resolution that enforces it.
var policyTargets = map[string]struct{}{
	TargetImportantPhrase: {},
	TargetImage:           {},
	TargetCaption:         {},
	TargetDate:            {},
	TargetMetric:          {},
}

// resolveAnimationPolicies expands plan-level animation defaults onto semantic
// instances before the ordinary per-kind lowering. Subgroup rules take
// precedence over group rules; explicit item/child motion ids always win.
func resolveAnimationPolicies(src *semanticPlan) error {
	policies := make(map[animationPolicySelector]semanticAnimationPolicy, len(src.AnimationPolicies))
	for index, policy := range src.AnimationPolicies {
		if _, ok := policyTargets[policy.Target]; !ok {
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
		if strings.TrimSpace(policy.MotionID) != policy.MotionID {
			return fmt.Errorf("overlay: animation_policies[%d] motion_id must be trimmed", index)
		}
		if policy.MotionID != "" && !motionAdmitsTarget(policy.MotionID, policy.Target) {
			return fmt.Errorf("overlay: animation_policies[%d] motion %q is not supported for target %q", index, policy.MotionID, policy.Target)
		}
		if policy.MotionID == "" && len(policy.MotionParams) > 0 {
			return fmt.Errorf("overlay: animation_policies[%d] motion_params requires motion_id", index)
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
		if len(item.ImageLayers) > 0 && item.GroupMotionID != "" {
			if item.MotionID != "" && item.MotionID != item.GroupMotionID {
				return fmt.Errorf("overlay: item %q motion_id %q conflicts with group_motion_id %q", item.ID, item.MotionID, item.GroupMotionID)
			}
			if !animationUsesGroupRecipe(item.GroupMotionID) {
				return fmt.Errorf("overlay: item %q group_motion_id %q is not a certified multi-image motion", item.ID, item.GroupMotionID)
			}
			item.MotionID = item.GroupMotionID
		}
		if isTextLikeKind(kind) && len(item.Assets) == 0 && len(item.ImageLayers) == 0 && item.MotionID == "" {
			if policy, ok := selectAnimationPolicy(policies, TargetImportantPhrase, item.GroupID, item.SubgroupID); ok {
				item.MotionID, item.MotionParams = resolvePolicyMotion(src, policy, TargetImportantPhrase, item.GroupID, item.SubgroupID)
			}
		}
		if item.MotionID == "" {
			// The specialized motion target is a fact about the item's
			// template, owned by the template registry (TemplateSpec.
			// MotionTarget). This replaces the historical switch that
			// compared item.Template with the named literals — the very
			// classification the entity_template_inference rule forbids.
			target := templateSpecFor(item.Template).MotionTarget
			if target != "" {
				if policy, ok := selectAnimationPolicy(policies, target, item.GroupID, item.SubgroupID); ok {
					item.MotionID, item.MotionParams = resolvePolicyMotion(src, policy, target, item.GroupID, item.SubgroupID)
				}
			}
		}
		if strings.TrimSpace(item.EntityCaption) != "" && item.CaptionMotionID == "" {
			if policy, ok := selectAnimationPolicy(policies, TargetCaption, item.GroupID, item.SubgroupID); ok {
				item.CaptionMotionID, item.CaptionMotionParams = resolvePolicyMotion(src, policy, TargetCaption, item.GroupID, item.SubgroupID)
			}
		}
		if strings.TrimSpace(item.EntityCaption) != "" && item.CaptionMotionID != "" && !motionAdmitsTarget(item.CaptionMotionID, TargetCaption) {
			return fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", item.ID, item.CaptionMotionID)
		}
		if len(item.ImageLayers) > 0 {
			if item.MotionID == "" {
				if policy, ok := selectAnimationPolicy(policies, TargetImage, item.GroupID, item.SubgroupID); ok && policy.MotionID != "" && animationUsesMultiImageRecipe(policy.MotionID) {
					item.MotionID, item.MotionParams = resolvePolicyMotion(src, policy, TargetImage, item.GroupID, item.SubgroupID)
				}
			}
			multiImageMotion := animationUsesMultiImageRecipe(item.MotionID)
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
				if child.MotionID == "" && !multiImageMotion {
					if policy, ok := selectAnimationPolicy(policies, TargetImage, child.GroupID, child.SubgroupID); ok && (policy.MotionID == "" || !animationUsesMultiImageRecipe(policy.MotionID)) {
						child.MotionID, child.MotionParams = resolvePolicyMotion(src, policy, TargetImage, child.GroupID, child.SubgroupID)
					}
				}
				if strings.TrimSpace(child.Caption) != "" && child.CaptionMotionID == "" {
					if policy, ok := selectAnimationPolicy(policies, TargetCaption, child.GroupID, child.SubgroupID); ok {
						child.CaptionMotionID, child.CaptionMotionParams = resolvePolicyMotion(src, policy, TargetCaption, child.GroupID, child.SubgroupID)
					}
				}
				if strings.TrimSpace(child.Caption) != "" && child.CaptionMotionID != "" && !motionAdmitsTarget(child.CaptionMotionID, TargetCaption) {
					return fmt.Errorf("overlay: item %q image layer %q caption motion %q is not supported for text captions", item.ID, child.ID, child.CaptionMotionID)
				}
			}
			continue
		}

		if len(item.ImageLayers) == 0 && item.MotionID == "" && len(item.Assets) > 0 && (isImageKind(kind) || isEntityKind(kind)) {
			if policy, ok := selectAnimationPolicy(policies, TargetImage, item.GroupID, item.SubgroupID); ok {
				item.MotionID, item.MotionParams = resolvePolicyMotion(src, policy, TargetImage, item.GroupID, item.SubgroupID)
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

// resolvePolicyMotion maps an explicit group policy verbatim, or selects the
// same deterministic catalog motion for every item in an implicitly-random
// group. The selector includes subgroup_id so sibling overrides get
// independent choices while members of each subgroup stay consistent.
func resolvePolicyMotion(src *semanticPlan, policy semanticAnimationPolicy, target, groupID, subgroupID string) (string, map[string]any) {
	if policy.MotionID != "" {
		return policy.MotionID, cloneMotionParams(policy.MotionParams)
	}
	// A group-level policy shares one choice across all member subgroups; an
	// explicitly declared subgroup gets its own stable choice.
	if policy.SubgroupID != "" {
		subgroupID = policy.SubgroupID
	}
	motionID := runtimeDefaultGroupMotionID(src, target, groupID, subgroupID)
	return motionID, nil
}

func runtimeDefaultGroupMotionID(src *semanticPlan, target, groupID, subgroupID string) string {
	if src == nil {
		return ""
	}
	var pool []string
	switch target {
	case TargetImportantPhrase:
		pool = append([]string(nil), motion.Registry.PhraseAnimationIDs()...)
	case TargetImage:
		pool = append([]string(nil), runtimeDefaultImageMotionIDs...)
	case TargetCaption:
		pool = append([]string(nil), runtimeDefaultCaptionMotionIDs...)
	case TargetDate:
		pool = motion.Registry.PresentationMotionIDs("date_v1")
	case TargetMetric:
		pool = motion.Registry.PresentationMotionIDs("metric_v1")
	}
	if len(pool) == 0 {
		return ""
	}
	sort.Strings(pool)
	return pool[styleHash("runtime_default_group_motion", src.PlanID, src.VideoID, target+"\x00"+groupID+"\x00"+subgroupID, uint64(len(pool)))]
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

func animationUsesMultiImageRecipe(id string) bool {
	definition, err := resolveMotionDefinition(id)
	return err == nil && isMultiImageRecipe(definition)
}

// animationUsesGroupRecipe accepts only certified image_group_v1 choreographies,
// which carry a GroupRecipe rather than an ImageRecipe stack.
func animationUsesGroupRecipe(id string) bool {
	definition, err := resolveMotionDefinition(id)
	return err == nil && isImageGroupRecipe(definition)
}
