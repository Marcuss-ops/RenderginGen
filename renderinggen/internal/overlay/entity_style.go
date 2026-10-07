package overlay

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// typewriterCaptionPrefix identifies the typewriter caption class: the only
// caption-motion alias class the entity registry needs to tag separately.
const typewriterCaptionPrefix = "typewriter"

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

// entityStyleTags derives every tag of a variant from its composition fields:
// no parallel index classification exists. A tag is either an inherent part of
// the composition (layout side, badge, camera controller) or an alias class of
// the caption motion (typewriter). Producers can query the registry by tag
// instead of by hard-coded index groups.
func entityStyleTags(style entityStyleVariant) []string {
	var tags []string
	seen := map[string]struct{}{}
	add := func(tag string) {
		tag = normalizeStyleKey(tag)
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	const (
		tagBadge      = "badge"
		tagTypewriter = "typewriter"
	)
	switch style.CaptionLayout {
	case "below":
		add("below")
	case "left", "right":
		add("side")
	}
	switch style.ImageSide {
	case "center":
		add("below")
	case "left", "right":
		add("side")
	}
	if style.CameraMotionID != "" {
		add("camera")
	}
	if style.IsBadge {
		add(tagBadge)
	}
	if strings.HasPrefix(style.CaptionMotionID, typewriterCaptionPrefix) {
		// Typewriter recipes are their own composition class in the catalog:
		// tag the caption-motion family the style actually uses.
		add(tagTypewriter)
	}
	return tags
}

// styleHash is the deterministic sampling key shared by the registry sampler
// and the badge runtime randomization, so both derive from the same salted
// identity inputs and no third randomizer can drift apart.
func styleHash(domain, planID, videoID, itemID string, mod uint64) uint64 {
	h := sha256.Sum256([]byte(domain + "\x00" + planID + "\x00" + videoID + "\x00" + itemID))
	return binary.LittleEndian.Uint64(h[:8]) % mod
}

// entityStyleQuery is a tag query over the registry: every requested tag must
// be present on the candidate. Empty queries match everything.
type entityStyleQuery []string

// entityStyleRegistry is the queryable catalog over premiumEntityStyles. It is
// built once from the definitions; tags are derived, never stored, so no
// parallel classification of the same array can drift out of sync.
var entityStyleRegistry = newEntityStyleRegistry()

type entityStyleRegistryType struct {
	byTag map[string][]int
	all   []int
}

func newEntityStyleRegistry() *entityStyleRegistryType {
	reg := &entityStyleRegistryType{byTag: map[string][]int{}}
	for i := range premiumEntityStyles {
		reg.all = append(reg.all, i)
		for _, tag := range entityStyleTags(premiumEntityStyles[i]) {
			reg.byTag[tag] = append(reg.byTag[tag], i)
		}
	}
	return reg
}

// query returns the indices whose tags satisfy every requested tag.
func (reg *entityStyleRegistryType) query(tags []string) []int {
	if len(tags) == 0 {
		return append([]int(nil), reg.all...)
	}
	// Seed from the smallest indexed tag set, then intersect the rest. The
	// registry index is authoritative; do not rescan/rederive every style tag
	// for each sampler request.
	var candidates []int
	hasTag := false
	for _, tag := range tags {
		tag = normalizeStyleKey(tag)
		if tag == "" {
			continue
		}
		hasTag = true
		indices, exists := reg.byTag[tag]
		if !exists {
			return nil
		}
		if candidates == nil || len(indices) < len(candidates) {
			candidates = indices
		}
	}
	if !hasTag {
		return append([]int(nil), reg.all...)
	}
	result := make([]int, 0, len(candidates))
	for _, idx := range candidates {
		styleTags := entityStyleTags(premiumEntityStyles[idx])
		match := true
		for _, want := range tags {
			want = normalizeStyleKey(want)
			if want == "" {
				continue
			}
			found := false
			for _, have := range styleTags {
				if have == want {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			result = append(result, idx)
		}
	}
	return result
}

var entityStyleAliases = map[string]string{
	"center_top":         "01_entity_pitch_lift_text_below",
	"vertical_editorial": "02_entity_yaw_flip_text_below",
	"centered_signature": "05_entity_depth_float_text_below",
}

func entityStyleByID(id string) (entityStyleVariant, bool) {
	for _, style := range premiumEntityStyles {
		if style.ID == id {
			return style, true
		}
	}
	return entityStyleVariant{}, false
}

// sample deterministically picks one candidate for the identity tuple.
func (reg *entityStyleRegistryType) sample(indices []int, domain, planID, videoID, itemID string) entityStyleVariant {
	if len(indices) == 0 {
		indices = reg.all
	}
	return premiumEntityStyles[indices[styleHash(domain, planID, videoID, itemID, uint64(len(indices)))]]
}

func normalizeStyleKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// applyBadgeRuntimeRandomization enforces the runtime yellow/red contrast rule:
// badges are always randomized between Yellow (#FFDE00, dark lettering #0A0B0E)
// and Red (#EB1E2D, white lettering #FFFFFF). The sampler shares styleHash.
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
	if styleHash("badge_color", planID, videoID, itemID, 2) == 0 {
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
	// Backward-compatibility aliases are catalog entries by stable ID, not
	// positional references into the 25-style array.
	if id, ok := entityStyleAliases[norm]; ok {
		return entityStyleByID(id)
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

// entityTagSelectors maps the formerly hard-coded group selectors to tag
// queries. Adding a composition class now means tagging it: no new switch case,
// no new index group. Empty query ("random") covers the whole registry.
var entityTagSelectors = map[string]entityStyleQuery{
	"":                        {},
	"premium_random_v1":       {},
	"random":                  {},
	"testo_sotto":             {"below"},
	"below":                   {"below"},
	"premium_below_random_v1": {"below"},
	"caption_below":           {"below"},
	"badge":                   {"badge"},
	"badge_random":            {"badge"},
	"camera":                  {"camera"},
	"camera_random":           {"camera"},
	"side":                    {"side"},
	"side_random":             {"side"},
	"typewriter":              {"typewriter"},
	"typewriter_random":       {"typewriter"},
}

// ResolveEntityStyle maps a user or producer style selector to an entity
// variant. Selector words "premium_random_v1", "testo_sotto", "badge",
// "camera", "side", "typewriter" (and their _random aliases) resolve as tag
// queries over the registry; everything else is a lookup by ID, name, slug,
// reference number, or motion id.
func ResolveEntityStyle(styleID, planID, videoID, itemID string) (entityStyleVariant, bool) {
	if query, isSelector := entityTagSelectors[normalizeStyleKey(styleID)]; isSelector {
		style := entityStyleRegistry.sample(entityStyleRegistry.query(query), styleDomain(query), planID, videoID, itemID)
		applyBadgeRuntimeRandomization(&style, styleID, planID, videoID, itemID)
		return style, true
	}
	style, ok := LookupEntityStyle(styleID)
	if ok {
		applyBadgeRuntimeRandomization(&style, styleID, planID, videoID, itemID)
	}
	return style, ok
}

// styleDomain is the sampling salt shared by all tag selectors: the selector
// class, not the raw query, so "badge" and "badge_random" sample identically.
// The empty query keeps the historic unprefixed salt of the all-styles sample.
func styleDomain(tags entityStyleQuery) string {
	if len(tags) != 0 {
		return strings.Join(tags, "+")
	}
	return ""
}
