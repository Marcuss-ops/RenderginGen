package overlay

import (
	"fmt"
	"strings"
)

// validateTextElementContract is the semantic boundary for the separation
// between content and composition:
//   - Text is producer-owned content and is never looked up in a catalog;
//   - template_id is optional for a simple text primitive;
//   - template_slots are opaque producer content for a reusable composition;
//   - style_id and motion_id are independent identities, not template variants.
//
// RenderingGen only validates the shape here. It does not invent font, layout,
// color, camera or animation values.
func validateTextElementContract(item semanticItem) error {
	templateID := strings.TrimSpace(item.Template)
	styleID := strings.TrimSpace(item.StyleID)
	text := strings.TrimSpace(item.Text)
	behavior := behaviorText
	if item.Kind != "" {
		behavior = behaviorOf(ItemKind(strings.ToLower(strings.TrimSpace(item.Kind))))
	} else if templateID != "" {
		behavior = behaviorOf(templateSpecFor(templateID).Kind)
	}

	if templateID == "" && len(item.TemplateSlots) > 0 {
		return fmt.Errorf("overlay: item %q has template_slots but no template_id", item.ID)
	}
	if templateID == "" && behavior != behaviorText {
		return fmt.Errorf("overlay: item %q without template_id must be a text primitive, got kind %q", item.ID, item.Kind)
	}
	if templateID == "" && len(item.Assets) > 0 {
		return fmt.Errorf("overlay: item %q is a text primitive but carries assets; use a template instance", item.ID)
	}
	if styleID != "" && behavior != behaviorText {
		return fmt.Errorf("overlay: item %q style_id %q is only valid for text content", item.ID, item.StyleID)
	}
	if behavior != behaviorImage && behavior != behaviorVideo && text == "" {
		return fmt.Errorf("overlay: item %q requires text (RenderingGen owns content; templates do not provide phrases)", item.ID)
	}
	return nil
}
