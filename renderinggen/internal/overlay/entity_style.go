package overlay

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// entityStyleVariant contains only composition choices. Image pixels, caption
// text and the plan's background remain producer supplied at runtime.
type entityStyleVariant struct {
	ID                string
	Name              string
	LegacyReference   string
	Group             int
	ImageMotionID     string
	CaptionMotionID   string
	CaptionLayout     string
	CaptionFontFamily string
	CaptionColor      string
	ImageSide         string
	IsBadge           bool
	BadgeColor        string
	CameraMotionID    string
}

// premiumEntityStyles defines the 25 official Apple-style entity card compositions:
// Group 1 (01-05): 3D Entrances (Pitch / Yaw / Pop / Swing / Depth) + Centered Text Below
// Group 2 (06-10): 3D Entrances (Swipe / Orbit / Tilt / Dolly / Drift) + Side-by-Side Text
// Group 3 (11-15): 3D Entrances + Typewriter / Cyber / Editorial / Code / Dolly Characters Below
// Group 4 (16-20): Highlighting Badges (Runtime Yellow/Red randomized) + Text Below
// Group 5 (21-25): 3D Camera Movements (Dolly / Orbit / Crane / Dutch / Flyby) + Text Below
var premiumEntityStyles = [...]entityStyleVariant{
	// --- GROUP 1: Centered Hero Portrait + Text Below ---
	{
		ID:                "01_entity_pitch_lift_text_below",
		Name:              "Apple Spatial Pitch Lift",
		LegacyReference:   "01",
		Group:             1,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},
	{
		ID:                "02_entity_yaw_flip_text_below",
		Name:              "Apple Spatial Yaw Flip",
		LegacyReference:   "02",
		Group:             1,
		ImageMotionID:     "image_25d_yaw_flip_in",
		CaptionMotionID:   "text_fade_up",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},
	{
		ID:                "03_entity_pop_bounce_text_below",
		Name:              "Apple Spatial Pop Bounce",
		LegacyReference:   "03",
		Group:             1,
		ImageMotionID:     "image_25d_pop_z_bounce",
		CaptionMotionID:   "text_scale_punch",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},
	{
		ID:                "04_entity_card_swing_text_below",
		Name:              "Apple Spatial Card Swing",
		LegacyReference:   "04",
		Group:             1,
		ImageMotionID:     "image_25d_card_swing",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},
	{
		ID:                "05_entity_depth_float_text_below",
		Name:              "Apple Spatial Depth Float",
		LegacyReference:   "05",
		Group:             1,
		ImageMotionID:     "image_25d_depth_float_in",
		CaptionMotionID:   "text_word_stagger",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},

	// --- GROUP 2: Side-by-Side Spatial Layout (Text Right / Left) ---
	{
		ID:                "06_entity_swipe_text_right",
		Name:              "Apple Spatial Swipe Right",
		LegacyReference:   "06",
		Group:             2,
		ImageMotionID:     "image_25d_swipe_3d",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "right",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "left",
	},
	{
		ID:                "07_entity_orbit_text_left",
		Name:              "Apple Spatial Orbit Left",
		LegacyReference:   "07",
		Group:             2,
		ImageMotionID:     "image_25d_yaw_flip_in",
		CaptionMotionID:   "text_word_stagger",
		CaptionLayout:     "left",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "right",
	},
	{
		ID:                "08_entity_counter_tilt_text_right",
		Name:              "Apple Spatial Counter Tilt Right",
		LegacyReference:   "08",
		Group:             2,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "text_fade_up",
		CaptionLayout:     "right",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "left",
	},
	{
		ID:                "09_entity_dolly_settle_text_left",
		Name:              "Apple Spatial Dolly Settle Left",
		LegacyReference:   "09",
		Group:             2,
		ImageMotionID:     "image_25d_depth_float_in",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "left",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "right",
	},
	{
		ID:                "10_entity_parallax_drift_text_right",
		Name:              "Apple Spatial Parallax Drift Right",
		LegacyReference:   "10",
		Group:             2,
		ImageMotionID:     "image_25d_swipe_3d",
		CaptionMotionID:   "text_scale_punch",
		CaptionLayout:     "right",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "left",
	},

	// --- GROUP 3: Typewriter / Computer Characters Text Below ---
	{
		ID:                "11_entity_typewriter_classic",
		Name:              "Apple Spatial Typewriter Classic",
		LegacyReference:   "11",
		Group:             3,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "typewriter_clean",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},
	{
		ID:                "12_entity_terminal_cyber",
		Name:              "Apple Spatial Terminal Cyber",
		LegacyReference:   "12",
		Group:             3,
		ImageMotionID:     "image_25d_pop_z_bounce",
		CaptionMotionID:   "typewriter_neon",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#00F0FF",
		ImageSide:         "center",
	},
	{
		ID:                "13_entity_typewriter_editorial_gold",
		Name:              "Apple Spatial Typewriter Gold",
		LegacyReference:   "13",
		Group:             3,
		ImageMotionID:     "image_25d_yaw_flip_in",
		CaptionMotionID:   "typewriter_soft_lift",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#F5E6C8",
		ImageSide:         "center",
	},
	{
		ID:                "14_entity_code_prompt_reveal",
		Name:              "Apple Spatial Code Prompt",
		LegacyReference:   "14",
		Group:             3,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "typewriter_glitch",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#50FA7B",
		ImageSide:         "center",
	},
	{
		ID:                "15_entity_typewriter_dolly_breath",
		Name:              "Apple Spatial Typewriter Breath",
		LegacyReference:   "15",
		Group:             3,
		ImageMotionID:     "image_25d_depth_float_in",
		CaptionMotionID:   "typewriter_tracking",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
	},

	// --- GROUP 4: Highlighting Badges (Runtime Yellow/Red randomized) ---
	{
		ID:                "16_entity_badge_yellow_wipe",
		Name:              "Apple Spatial Badge Wipe",
		LegacyReference:   "16",
		Group:             4,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#0A0B0E",
		ImageSide:         "center",
		IsBadge:           true,
		BadgeColor:        "#FFDE00",
	},
	{
		ID:                "17_entity_badge_red_impact",
		Name:              "Apple Spatial Badge Impact",
		LegacyReference:   "17",
		Group:             4,
		ImageMotionID:     "image_25d_pop_z_bounce",
		CaptionMotionID:   "text_scale_punch",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		IsBadge:           true,
		BadgeColor:        "#EB1E2D",
	},
	{
		ID:                "18_entity_badge_cyan_electric",
		Name:              "Apple Spatial Badge Electric",
		LegacyReference:   "18",
		Group:             4,
		ImageMotionID:     "image_25d_yaw_flip_in",
		CaptionMotionID:   "text_fade_up",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#0A0B0E",
		ImageSide:         "center",
		IsBadge:           true,
		BadgeColor:        "#FFDE00",
	},
	{
		ID:                "19_entity_badge_orange_amber",
		Name:              "Apple Spatial Badge Amber",
		LegacyReference:   "19",
		Group:             4,
		ImageMotionID:     "image_25d_card_swing",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		IsBadge:           true,
		BadgeColor:        "#EB1E2D",
	},
	{
		ID:                "20_entity_badge_green_emerald",
		Name:              "Apple Spatial Badge Emerald",
		LegacyReference:   "20",
		Group:             4,
		ImageMotionID:     "image_25d_depth_float_in",
		CaptionMotionID:   "text_word_stagger",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#0A0B0E",
		ImageSide:         "center",
		IsBadge:           true,
		BadgeColor:        "#FFDE00",
	},

	// --- GROUP 5: 3D Camera Movements ---
	{
		ID:                "21_entity_camera_dolly_push",
		Name:              "Apple Spatial Camera Dolly Push",
		LegacyReference:   "21",
		Group:             5,
		ImageMotionID:     "image_25d_depth_float_in",
		CaptionMotionID:   "text_fade_up",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		CameraMotionID:    "camera_dolly_push",
	},
	{
		ID:                "22_entity_camera_orbit_yaw",
		Name:              "Apple Spatial Camera Orbit Yaw",
		LegacyReference:   "22",
		Group:             5,
		ImageMotionID:     "image_25d_yaw_flip_in",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		CameraMotionID:    "camera_orbit_yaw",
	},
	{
		ID:                "23_entity_camera_crane_rise",
		Name:              "Apple Spatial Camera Crane Rise",
		LegacyReference:   "23",
		Group:             5,
		ImageMotionID:     "image_25d_pitch_lift",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		CameraMotionID:    "camera_crane_rise",
	},
	{
		ID:                "24_entity_camera_dutch_spatial",
		Name:              "Apple Spatial Camera Dutch Spatial",
		LegacyReference:   "24",
		Group:             5,
		ImageMotionID:     "image_25d_card_swing",
		CaptionMotionID:   "text_scale_punch",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		CameraMotionID:    "camera_dutch_spatial",
	},
	{
		ID:                "25_entity_camera_flyby_parallax",
		Name:              "Apple Spatial Camera Flyby Parallax",
		LegacyReference:   "25",
		Group:             5,
		ImageMotionID:     "image_25d_swipe_3d",
		CaptionMotionID:   "text_word_rise",
		CaptionLayout:     "below",
		CaptionFontFamily: "inter",
		CaptionColor:      "#FFFFFF",
		ImageSide:         "center",
		CameraMotionID:    "camera_flyby_parallax",
	},
}

// belowStyleIndices contains all styles that place the caption below the portrait:
// Group 1 (0..4), Group 3 (10..14), Group 4 (15..19), Group 5 (20..24).
var belowStyleIndices = [...]int{
	0, 1, 2, 3, 4,
	10, 11, 12, 13, 14,
	15, 16, 17, 18, 19,
	20, 21, 22, 23, 24,
}

var badgeStyleIndices = [...]int{15, 16, 17, 18, 19}
var cameraStyleIndices = [...]int{20, 21, 22, 23, 24}
var sideStyleIndices = [...]int{5, 6, 7, 8, 9}
var typewriterStyleIndices = [...]int{10, 11, 12, 13, 14}

func normalizeStyleKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// applyBadgeRuntimeRandomization enforces the runtime yellow/red contrast rule:
// badges are always randomized between Yellow (#FFDE00, dark lettering #0A0B0E)
// and Red (#EB1E2D, white lettering #FFFFFF).
func applyBadgeRuntimeRandomization(style *entityStyleVariant, query, planID, videoID, itemID string) {
	if !style.IsBadge {
		return
	}
	norm := normalizeStyleKey(query)
	if strings.Contains(norm, "yellow") {
		style.BadgeColor = "#FFDE00"
		style.CaptionColor = "#0A0B0E"
		return
	}
	if strings.Contains(norm, "red") {
		style.BadgeColor = "#EB1E2D"
		style.CaptionColor = "#FFFFFF"
		return
	}
	// Deterministic runtime randomization between yellow and red
	h := sha256.Sum256([]byte("badge_color\x00" + planID + "\x00" + videoID + "\x00" + itemID))
	if binary.LittleEndian.Uint64(h[:8])%2 == 0 {
		style.BadgeColor = "#FFDE00"
		style.CaptionColor = "#0A0B0E"
	} else {
		style.BadgeColor = "#EB1E2D"
		style.CaptionColor = "#FFFFFF"
	}
}

// LookupEntityStyle matches an entity style variant by ID, name, slug, reference
// number, or motion id.
func LookupEntityStyle(query string) (entityStyleVariant, bool) {
	norm := normalizeStyleKey(query)
	// Backward-compatibility aliases for golden tests
	switch norm {
	case "center_top":
		return premiumEntityStyles[0], true
	case "vertical_editorial":
		return premiumEntityStyles[1], true
	case "centered_signature":
		return premiumEntityStyles[4], true
	}
	for _, style := range premiumEntityStyles {
		if normalizeStyleKey(style.ID) == norm ||
			normalizeStyleKey(style.Name) == norm ||
			style.LegacyReference == strings.TrimSpace(query) ||
			strings.TrimLeft(style.LegacyReference, "0") == strings.TrimSpace(query) ||
			style.CaptionMotionID == strings.TrimSpace(query) ||
			style.ImageMotionID == strings.TrimSpace(query) {
			return style, true
		}
	}
	return entityStyleVariant{}, false
}

// selectRandomEntityStyle chooses a stable pseudo-random variant across all 25
// Apple-style options.
func selectRandomEntityStyle(planID, videoID, itemID string) entityStyleVariant {
	h := sha256.Sum256([]byte(planID + "\x00" + videoID + "\x00" + itemID))
	index := binary.LittleEndian.Uint64(h[:8]) % uint64(len(premiumEntityStyles))
	return premiumEntityStyles[index]
}

// selectRandomBelowEntityStyle deterministically selects from the 20 "Testo Sotto"
// (caption below image) layouts.
func selectRandomBelowEntityStyle(planID, videoID, itemID string) entityStyleVariant {
	h := sha256.Sum256([]byte("below\x00" + planID + "\x00" + videoID + "\x00" + itemID))
	idx := binary.LittleEndian.Uint64(h[:8]) % uint64(len(belowStyleIndices))
	return premiumEntityStyles[belowStyleIndices[idx]]
}

// ResolveEntityStyle maps a user or producer style selector to an entity
// variant. Supports "premium_random_v1", "testo_sotto", "below",
// "premium_below_random_v1", group selectors, and specific style IDs.
func ResolveEntityStyle(styleID, planID, videoID, itemID string) (entityStyleVariant, bool) {
	norm := normalizeStyleKey(styleID)
	var style entityStyleVariant
	var ok bool

	switch norm {
	case "premium_random_v1", "random", "":
		style = selectRandomEntityStyle(planID, videoID, itemID)
		ok = true
	case "testo_sotto", "below", "premium_below_random_v1", "caption_below":
		style = selectRandomBelowEntityStyle(planID, videoID, itemID)
		ok = true
	case "badge", "badge_random":
		h := sha256.Sum256([]byte("badge\x00" + planID + "\x00" + videoID + "\x00" + itemID))
		idx := binary.LittleEndian.Uint64(h[:8]) % uint64(len(badgeStyleIndices))
		style = premiumEntityStyles[badgeStyleIndices[idx]]
		ok = true
	case "camera", "camera_random":
		h := sha256.Sum256([]byte("camera\x00" + planID + "\x00" + videoID + "\x00" + itemID))
		idx := binary.LittleEndian.Uint64(h[:8]) % uint64(len(cameraStyleIndices))
		style = premiumEntityStyles[cameraStyleIndices[idx]]
		ok = true
	case "side", "side_random":
		h := sha256.Sum256([]byte("side\x00" + planID + "\x00" + videoID + "\x00" + itemID))
		idx := binary.LittleEndian.Uint64(h[:8]) % uint64(len(sideStyleIndices))
		style = premiumEntityStyles[sideStyleIndices[idx]]
		ok = true
	case "typewriter", "typewriter_random":
		h := sha256.Sum256([]byte("typewriter\x00" + planID + "\x00" + videoID + "\x00" + itemID))
		idx := binary.LittleEndian.Uint64(h[:8]) % uint64(len(typewriterStyleIndices))
		style = premiumEntityStyles[typewriterStyleIndices[idx]]
		ok = true
	default:
		style, ok = LookupEntityStyle(styleID)
	}

	if ok {
		applyBadgeRuntimeRandomization(&style, styleID, planID, videoID, itemID)
	}
	return style, ok
}
