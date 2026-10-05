package overlay

import (
	"crypto/sha256"
	"encoding/binary"
)

// entityStyleVariant contains only composition choices. Image pixels, caption
// text and the plan's background remain producer supplied at runtime.
type entityStyleVariant struct {
	Name              string
	LegacyReference   string
	ImageMotionID     string
	CaptionMotionID   string
	CaptionLayout     string
	CaptionFontFamily string
	CaptionColor      string
	ImageSide         string
}

// premiumEntityStyles is intentionally capped at 15 variants. The variants
// reuse registered Chronon motions and runtime font families, keeping each
// composition executable by the standard Renderigen worker.
var premiumEntityStyles = [...]entityStyleVariant{
	{"Rule portrait", "08", "image_card_push", "trump_entity_text_01", "right", "inter", "#F2EFE6", "left"},
	{"Amber split", "09", "image_diagonal_sweep", "trump_entity_text_02", "left", "poppins", "#F8F5EA", "right"},
	{"Goldline split", "10", "image_fade_reveal", "trump_entity_text_03", "right", "dejavu_sans", "#FFFFFF", "left"},
	{"Cream gold", "12", "image_focus_reveal", "trump_entity_text_04", "right", "inter", "#F2EFE6", "left"},
	{"Center top", "13", "image_parallax_depth_reveal", "trump_entity_text_05", "center", "poppins", "#F8F5EA", "below"},
	{"Left close", "29", "image_scale_reveal", "trump_entity_text_06", "left", "dejavu_sans", "#FFFFFF", "right"},
	{"Soft gold", "17", "image_slide_left_reveal", "trump_entity_text_07", "left", "inter", "#F2EFE6", "right"},
	{"Red glow", "18", "image_slide_right_reveal", "trump_entity_text_08", "left", "poppins", "#F8F5EA", "right"},
	{"Thin rule", "19", "image_soft_focus_reveal", "trump_entity_text_09", "left", "dejavu_sans", "#FFFFFF", "right"},
	{"Top rule", "20", "image_tilt_settle", "trump_entity_text_10", "left", "inter", "#F2EFE6", "right"},
	{"Vertical editorial", "21", "image_card_flip", "trump_entity_text_11", "center", "poppins", "#F8F5EA", "below"},
	{"Reflection", "24", "image_depth_cascade", "trump_entity_text_12", "left", "dejavu_sans", "#FFFFFF", "right"},
	{"Archive metadata", "25", "image_depth_dolly", "trump_entity_text_13", "right", "inter", "#F2EFE6", "left"},
	{"Bright type", "27", "image_orbit_enter", "trump_entity_text_14", "left", "poppins", "#F8F5EA", "right"},
	{"Centered signature", "28", "image_yaw_reveal", "trump_entity_text_15", "center", "dejavu_sans", "#FFFFFF", "below"},
}

// selectRandomEntityStyle chooses a stable pseudo-random variant. Queue
// retries therefore render the same composition while different plans and
// entities distribute across the 15 options.
func selectRandomEntityStyle(planID, videoID, itemID string) entityStyleVariant {
	h := sha256.Sum256([]byte(planID + "\x00" + videoID + "\x00" + itemID))
	index := binary.LittleEndian.Uint64(h[:8]) % uint64(len(premiumEntityStyles))
	return premiumEntityStyles[index]
}
