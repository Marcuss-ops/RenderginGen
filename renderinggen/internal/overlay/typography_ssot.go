package overlay

// typography_ssot.go is the single source of truth for text typography
// (font family, stroke, shadow, glow) across every text role.
//
// PROBLEM (fixed here): entity captions rendered Inter while phrases
// rendered Poppins-Bold, typewriter motions forced Bricolage-Grotesque,
// and entity CaptionColor was overwritten per render by badge sampling —
// so the same payload produced a different typeface / treatment per
// animation and per render.
//
// PRECEDENCE (highest wins):
//  1. Payload explicit choice:
//     params/style font_family, glow_size, stroke_size, font_size_px,
//     shadow_* — plus caption_font_family / caption_color for captions,
//     plus pin label_style for map labels.
//  2. Language fallback: OfficialFontPathForLanguage (Poppins-Bold latin,
//     DejaVuSans cyrillic).
//  3. Role treatment (text_role.go): phrase vs caption vs map defaults for
//     stroke/shadow/glow ONLY when the payload did not choose them.
//
// RULES:
//   - No motion branch may change the font (the old typewriter Bricolage
//     force-override is removed).
//   - Entity styles provide layout/motion/color defaults but never silently
//     retype or recolor a payload choice (see compileEntityCard guards).
//   - DefaultEntityCaptionFontFamily matches the phrase default (poppins)
//     so captions without an explicit choice render the same face as phrases.
const (
	// TypographyDefaultFontFamily is the house face when the payload names
	// none. It must stay a key of runtimeFontsByFamily.
	TypographyDefaultFontFamily = "poppins"
)
