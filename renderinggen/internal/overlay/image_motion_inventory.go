package overlay

import "github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"

// ImageMotionInventory returns all registered image-layer animations grouped
// by catalog. These are motions, not visual presets, and remain selectable via
// each item's motion_id independently of preset_id.
func ImageMotionInventory() map[string][]string {
	return map[string][]string{
		"overlay_v3_image":   motion.Registry.ImageV3MotionIDs(),
		"image_25d_clean_v1": motion.Registry.Image25DCleanV1MotionIDs(),
	}
}
