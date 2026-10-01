package overlay

import "github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"

// ImageMotionInventory returns all registered image-layer animations grouped
// by catalog. These are motions, not visual presets, and remain selectable via
// each item's motion_id independently of preset_id.
func ImageMotionInventory() map[string][]string {
	return map[string][]string{
		"overlay_v3_image":   motion.Registry.ImageV3MotionIDs(),
		"image_25d_clean_v1": motion.Registry.Image25DCleanV1MotionIDs(),
		"editorial_image_v1": motion.Registry.EditorialImageV1MotionIDs(),
		"image_premium_v1":   motion.Registry.ImagePremiumV1MotionIDs(),
		"brush_v1":           motion.Registry.VisualAccentsV1MotionIDs("brush_v1"),
		"web_rect_v1":        motion.Registry.VisualAccentsV1MotionIDs("web_rect_v1"),
		"paint_v1":           motion.Registry.VisualAccentsV1MotionIDs("paint_v1"),
		"light_leak_v1":      motion.Registry.VisualAccentsV1MotionIDs("light_leak_v1"),
	}
}

// PresentationMotionInventory returns Date, Metric and entity-card animation
// IDs from the canonical ChrononTemplate presentation catalog. Each ID is
// selected on a semantic overlay item with motion_id; it is independent of
// the item's text preset_id.
func PresentationMotionInventory() map[string][]string {
	inventory := make(map[string][]string)
	for _, family := range motion.Registry.PresentationFamilyIDs() {
		inventory[family] = motion.Registry.PresentationMotionIDs(family)
	}
	return inventory
}
