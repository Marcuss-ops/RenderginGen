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

// motionDeprecationError keeps saved plans compilable until remove_after. The
// picker is gated immediately by the deprecated state; after the published date
// the compiler fails with a deterministic diagnostic instead of substituting a
// similar animation.
func motionDeprecationError(id string) error {
	info, ok := motion.Registry.DeprecationInfo(id)
	if !ok {
		return nil
	}
	return deprecationErrorAfter(id, info, time.Now().UTC())
}

func deprecationErrorAfter(id string, info motion.Deprecation, now time.Time) error {
	removeAfter, err := time.Parse("2006-01-02", strings.TrimSpace(info.RemoveAfter))
	if err != nil {
		return fmt.Errorf("overlay: motion %q has invalid deprecation date %q: %w", id, info.RemoveAfter, err)
	}
	if now.UTC().Before(removeAfter) {
		return nil
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

func motionDefinitionAdmitsTarget(definition *motion.MotionDefinition, target string) bool {
	if definition == nil {
		return false
	}
	switch target {
	case "important_phrase":
		if containsString(definition.Targets, "metric") || containsString(definition.Targets, "date") || containsString(definition.Targets, "entity") {
			return false
		}
		return containsString(definition.Targets, "text") || containsString(definition.Targets, "phrase") ||
			containsString(definition.Targets, "short_phrase") || containsString(definition.Targets, "caption")
	case "text":
		if containsString(definition.Targets, "metric") || containsString(definition.Targets, "date") {
			return false
		}
		return containsString(definition.Targets, "text") || containsString(definition.Targets, "phrase") ||
			containsString(definition.Targets, "short_phrase") || containsString(definition.Targets, "caption") ||
			containsString(definition.Targets, "entity")
	case "caption":
		if containsString(definition.Targets, "caption") {
			return true
		}
		if containsString(definition.Targets, "metric") || containsString(definition.Targets, "date") {
			return false
		}
		return containsString(definition.Targets, "text") || containsString(definition.Targets, "phrase") || containsString(definition.Targets, "entity")
	case "image":
		return containsString(definition.Targets, "image")
	case "metric":
		return containsString(definition.Targets, "metric")
	case "date":
		return containsString(definition.Targets, "date")
	case "short_phrase":
		return containsString(definition.Targets, "short_phrase")
	case "map_view":
		return centeredMapMotion(*definition)
	default:
		return false
	}
}

func semanticTextMotionTarget(item semanticItem, kind ItemKind) string {
	if target := templateSpecFor(item.Template).MotionTarget; target != "" {
		return target
	}
	switch kind {
	case KindImportantPhrase:
		return "important_phrase"
	case KindMetricStat:
		return "metric"
	case KindTimelineDate:
		return "date"
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
