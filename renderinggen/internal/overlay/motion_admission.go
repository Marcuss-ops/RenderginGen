package overlay

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

type resolvedMotion struct {
	plugin     motion.MotionPlugin
	definition *motion.MotionDefinition
}

func resolveRegisteredMotion(id string) (*resolvedMotion, error) {
	if _, deprecated := motion.Registry.DeprecationInfo(id); deprecated {
		plugin, err := motion.Registry.Resolve(id)
		if err != nil || plugin == nil {
			return nil, err
		}
		if declarative, ok := plugin.(motion.DeclarativePlugin); ok {
			definition := declarative.Definition
			return &resolvedMotion{plugin: plugin, definition: &definition}, nil
		}
		return &resolvedMotion{plugin: plugin}, nil
	}
	plugin, err := motion.Registry.Resolve(id)
	if err != nil || plugin == nil {
		return nil, err
	}
	resolved := &resolvedMotion{plugin: plugin}
	if declarative, ok := plugin.(motion.DeclarativePlugin); ok {
		definition := declarative.Definition
		resolved.definition = &definition
	}
	return resolved, nil
}

func resolveMotionDefinition(id string) (*motion.MotionDefinition, error) {
	resolved, err := resolveRegisteredMotion(id)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	return resolved.definition, nil
}

// deprecationDiagnostic renders the deterministic diagnostic for a retired
// motion. It is a pure function over the registry record, so the wording is
// testable without mutating the production registry.
func deprecationDiagnostic(id string, info motion.Deprecation) error {
	message := fmt.Sprintf("overlay: motion %q is deprecated and no longer resolvable", id)
	if reason := strings.TrimSpace(info.Reason); reason != "" {
		message += ": " + reason
	}
	if retired := strings.TrimSpace(info.RemoveAfter); retired != "" {
		message += fmt.Sprintf(" (retired %s)", retired)
	}
	if replacement := strings.TrimSpace(info.Replacement); replacement != "" {
		message += fmt.Sprintf("; migrate to %q", replacement)
	}
	return errors.New(message)
}

// motionDeprecationError enforces the single published policy: a deprecated
// motion is immediately non-selectable AND not resolvable — no compatibility
// window, no alias. The picker gate (motionAdmitsTarget), the lowering gate
// (lowerMotionForTarget) and this plan gate all fail the same way, so the
// catalog ("immediate", window 0) finally describes the compiler instead of
// contradicting it. It is also clock-free: the same plan and binary compile
// identically today and tomorrow, which the old remove_after grace check
// (time.Now in the compile path) could not promise.
func motionDeprecationError(id string) error {
	info, ok := motion.Registry.DeprecationInfo(id)
	if !ok {
		return nil
	}
	if strings.TrimSpace(info.RemoveAfter) == "" {
		return fmt.Errorf("overlay: motion %q has invalid deprecation record: remove_after is required", id)
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(info.RemoveAfter)); err != nil {
		return fmt.Errorf("overlay: motion %q has invalid deprecation date %q: %w", id, info.RemoveAfter, err)
	}
	return deprecationDiagnostic(id, info)
}

// semanticItemMotionIDs lists every motion ID an item can name, including its
// composited image layers and its map block, so the retirement gate covers all
// of them instead of only the item-level field.
func semanticItemMotionIDs(item semanticItem) []string {
	ids := []string{item.MotionID, item.CaptionMotionID}
	for _, layer := range item.ImageLayers {
		ids = append(ids, layer.MotionID, layer.CaptionMotionID)
	}
	if item.Map != nil {
		ids = append(ids, item.Map.MotionID)
	}
	return ids
}

// validateDeprecatedMotions rejects a plan that names a retired motion on any
// item, image layer, caption or animation policy. It runs before any lowering so
// the failure is the same every time and names the ID, the item and the reason:
// a retired motion is never replaced by a similar one, and no partially lowered
// plan reaches the renderer.
func validateDeprecatedMotions(src *semanticPlan) error {
	for _, policy := range src.AnimationPolicies {
		if err := motionDeprecationError(policy.MotionID); err != nil {
			return fmt.Errorf("overlay: animation_policies motion %q: %w", policy.MotionID, err)
		}
	}
	for _, item := range src.Items {
		for _, id := range semanticItemMotionIDs(item) {
			if err := motionDeprecationError(id); err != nil {
				return fmt.Errorf("overlay: item %q: %w", item.ID, err)
			}
		}
	}
	return nil
}

// motionAdmitsTarget is the overlay admission gate. Motion applicability comes
// from the canonical catalog; semantic targets keep specialized content
// (metrics, dates and entities) out of generic phrase/caption choices.
func motionAdmitsTarget(id, target string) bool {
	if _, deprecated := motion.Registry.DeprecationInfo(id); deprecated {
		return false
	}
	definition, err := resolveMotionDefinition(id)
	if err != nil {
		return false
	}
	return motionDefinitionAdmitsTarget(definition, target)
}

// targetAdmissionRule is the single source for one motion target: which
// catalog target tags admit a motion and which deny it, evaluated
// deny-first. Rules that are not pure set membership (ordering-sensitive
// caption, predicate-based map_view) use custom instead. Adding a target
// means adding one map entry — never extending a switch in a second file.
type targetAdmissionRule struct {
	admit  []string
	deny   []string
	custom func(*motion.MotionDefinition) bool
}

var targetAdmissionRules = map[string]targetAdmissionRule{
	"important_phrase": {
		deny:  []string{"metric", "date", "entity"},
		admit: []string{"text", "phrase", "short_phrase", "caption", "important_phrase"},
	},
	"text": {
		deny:  []string{"metric", "date"},
		admit: []string{"text", "phrase", "short_phrase", "caption", "entity"},
	},
	"entity": {
		deny:  []string{"metric", "date"},
		admit: []string{"entity", "caption", "text", "phrase"},
	},
	"phrase": {
		deny:  []string{"metric", "date"},
		admit: []string{"phrase", "text", "caption", "entity"},
	},
	"caption": {custom: captionTargetAdmits},
	"image":   {admit: []string{"image"}},
	"metric":  {admit: []string{"metric"}},
	"date":    {admit: []string{"date"}},
	"short_phrase": {
		admit: []string{"short_phrase"},
	},
	"map_view": {custom: func(definition *motion.MotionDefinition) bool {
		return centeredMapMotion(*definition)
	}},
}

// captionTargetAdmits checks conflicting specialized tags before any caption
// allow tag. A catalog entry that declares both caption and metric/date is
// ambiguous and must not bypass the specialized-target exclusion.
func captionTargetAdmits(definition *motion.MotionDefinition) bool {
	if containsString(definition.Targets, "metric") || containsString(definition.Targets, "date") {
		return false
	}
	return containsString(definition.Targets, "caption") ||
		containsString(definition.Targets, "text") ||
		containsString(definition.Targets, "phrase") ||
		containsString(definition.Targets, "entity")
}

func motionDefinitionAdmitsTarget(definition *motion.MotionDefinition, target string) bool {
	if definition == nil {
		return false
	}
	rule, ok := targetAdmissionRules[target]
	if !ok {
		return false
	}
	if rule.custom != nil {
		return rule.custom(definition)
	}
	for _, deny := range rule.deny {
		if containsString(definition.Targets, deny) {
			return false
		}
	}
	for _, admit := range rule.admit {
		if containsString(definition.Targets, admit) {
			return true
		}
	}
	return false
}

// semanticKindMotionTargets is the single kind → admission-target table for
// items whose template declares no MotionTarget. Anything not listed is a
// generic text item; adding a kind means adding one map entry.
var semanticKindMotionTargets = map[ItemKind]string{
	KindImportantPhrase: "important_phrase",
	KindMetricStat:      "metric",
	KindTimelineDate:    "date",
}

func semanticTextMotionTarget(item semanticItem, kind ItemKind) string {
	if target := templateSpecFor(item.Template).MotionTarget; target != "" {
		return target
	}
	if target, ok := semanticKindMotionTargets[kind]; ok {
		return target
	}
	return "text"
}

func centeredMapMotion(definition motion.MotionDefinition) bool {
	// Map applicability comes from the canonical catalog. This track restriction
	// prevents pins and labels from drifting relative to the raster.
	if !containsString(definition.Targets, "map_view") || len(definition.Tracks) == 0 {
		return false
	}
	for _, track := range definition.Tracks {
		switch track.Property {
		case "opacity", "scale", "scale_x", "scale_y", "blur":
		default:
			return false
		}
	}
	return true
}
