package overlay

// typography_ssot.go is the single source of truth for text typography
// (font family, stroke, shadow, glow) across every text role.
//
// Contract: every text role resolves typography through the same payload
// style keys, font resolver, and role defaults; motion recipes and entity
// composition sampling may not mutate an explicit payload choice.
//
// PRECEDENCE (highest wins):
//  1. PipelineGen's run-level style is projected into caption params before
//     sealing the plan; then explicit effective item/child params and nested
//     style keys. style overrides matching params keys.
//  2. Legacy caption_font_family only if no canonical font_family was selected;
//     then OfficialFontPathForLanguage (Poppins-Bold latin, DejaVuSans Cyrillic).
//  3. Role defaults (text_role.go) for stroke/shadow/glow only when the
//     payload did not choose those controls. Map pins retain label_style.
// The schema contract, PipelineGen supported-family list, queue asset registry,
// and runtime resolver each have parity tests; their ownership is documented
// at their respective producer/consumer boundary.
//
// RULES:
//   - Motion branches do not change font family.
//   - Entity styles provide layout/motion/fallback-color defaults, never an
//     explicit typeface or runtime text-style override.
//   - Every image caption is a separate text layer linked to its owning image;
//     do not create a second caption layer in the producer or consumer.
