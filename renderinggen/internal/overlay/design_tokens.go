// design_tokens.go owns the named geometry and motion tokens the overlay
// lowering uses when a value is IMPLIED rather than supplied.
//
// Why one file. The lowering path is deliberately a mechanical compiler: every
// style, geometry and motion decision comes from the semantic plan or from the
// official preset catalog. The handful of values that are instead *derived* from
// the canvas (a nominal text box, a safe-area fraction, a subtitle line height)
// were numeric literals inline at their use sites, spread across motion.go,
// visual_style_resolver.go and semantic_compile.go. That made two things
// impossible to review: seeing the complete set of values where RenderingGen
// stops being a pure executor, and telling a derived constant apart from an
// arbitrary one inside a function that mostly does arithmetic.
//
// The values are unchanged — these are declarations of what the code already
// did, not new defaults. The only rule this file adds is that a new implicit
// value has to be named here, where its justification is visible next to its
// neighbours.
package overlay

// Layer fit vocabulary. It is ONE closed set: every component that reads or
// validates a fit (background lowering, item lowering, the preset catalog)
// resolves against these tokens, so a producer cannot ask for a treatment that
// one site accepts and another silently rewrites.
const (
	// FitCover crops the source to fill the box.
	FitCover = "cover"
	// FitContain fits the whole source inside the box.
	FitContain = "contain"
	// FitStretch stretches the source to the box.
	FitStretch = "stretch"
	// FitNone leaves the source at its own size.
	FitNone = "none"
)

// fitVocabulary is the membership set of the tokens above.
var fitVocabulary = map[string]struct{}{
	FitCover: {}, FitContain: {}, FitStretch: {}, FitNone: {},
}

// supportedFits is the human-readable list used in compile errors, derived from
// the vocabulary so a new fit cannot be added without appearing in the message a
// producer reads when their value is rejected.
const supportedFits = "cover, contain, stretch, none"

// isSupportedFit reports whether fit is a token of the closed set.
func isSupportedFit(fit string) bool {
	_, ok := fitVocabulary[fit]
	return ok
}

// Nominal layer geometry. These are the dimensions a layer declares when neither
// the plan's typed block nor the official preset supplies one. They are
// nominal in the literal sense: they describe a box the renderer will size the
// text inside, not a measured layout.
const (
	// DefaultTextBoxWidth/Height are the nominal text-layer box (an
	// unbounded-width text layer would have no wrap point for the resolver).
	DefaultTextBoxWidth  = 320
	DefaultTextBoxHeight = 120

	// Watermark box: the declared width_px/height_px win; otherwise the box is
	// one sixth of the canvas wide with a fixed nominal height.
	WatermarkBoxWidthDivisor  = 6
	DefaultWatermarkBoxHeight = 80.0

	// Subtitle geometry: a fixed line height per cue, and a symmetric safe
	// margin subtracted from the canvas width to form the text box.
	SubtitleLineHeightPX = 70.0
	SubtitleSideMarginPX = 120.0
)

// Canvas-relative anchors, as fractions of the canvas dimension. They are the
// placement a preset asks for by NAME (lower_third, safe_area, bottom_center,
// top_center); the plan never carries the fraction itself.
const (
	// AnchorLowerThirdYFraction is where a lower-third anchor sits.
	AnchorLowerThirdYFraction = 0.76
	// AnchorSafeAreaFraction is the inset used by the safe_area anchor and the
	// horizontal margin of the lower-third anchor.
	AnchorSafeAreaFraction = 0.06
	// SubtitleBottomCenterYFraction is the baseline band of bottom_center cues.
	SubtitleBottomCenterYFraction = 0.80
	// SubtitleTopCenterYFraction is the top band of top_center cues.
	SubtitleTopCenterYFraction = 0.10
)

// MaxStaggerSweepFrames bounds the selector sweep a staggered preset emits: a
// reveal sweeps its glyphs over at most this many frames, so a long layer does
// not turn a stagger into a slow crawl. Preset definitions in the catalog
// declare their own enter durations against the same 72-frame budget.
const MaxStaggerSweepFrames = 72
