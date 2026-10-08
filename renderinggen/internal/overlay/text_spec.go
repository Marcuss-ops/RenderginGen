package overlay

import (
	"fmt"
	"strings"
)

// resolvedTextSpec is the fully resolved input of the role-agnostic text compiler:
// every semantic decision (role, style, geometry, motion) has already been
// made by the role resolver, so compileResolvedText never inspects a template,
// preset family or item kind. This is the seam that keeps phrases, entity
// captions and map labels from growing back into per-kind text engines.
type resolvedTextSpec struct {
	Text      string
	BoxWidth  int
	BoxHeight int
	Position  []float64
	// Size preserves fractional role-resolved geometry where the layer's
	// renderer size is more precise than the integer text box dimensions.
	Size []float64
	// OmitBox keeps legacy role specs whose wire contract carries Size only.
	OmitBox bool
	Style   *LayerStyle
	// RoleOverrides carries role-specific caller effects resolved before
	// lowering; the text compiler applies them after the shared role defaults.
	RoleOverrides *LayerStyle
	PresetID      string
	PresetDef     PresetDefinition
	MotionID      string
	MotionParams  map[string]any
	// StyleParams are the item's runtime style controls (params.style /
	// top-level params). The resolver splits them from the motion controls so
	// the compiler lowers exactly one style path.
	StyleParams  map[string]any
	MotionTarget string
	StylePolicy  textRole
	EntryExit    bool
	// FitPolicy is the resolved no-shrink decision for metric/date font sizes.
	FitPolicy string
	// FontSize is the role's resolved base size: the caption resolver pins the
	// fitted size, preset-backed specs leave zero and inherit the style.
	FontSize     float64
	MinFontSize  float64
	MaxFontSize  float64
	OverrideFill string
	OverrideFont string
}

// preset returns the preset definition attached to the spec, if any.
func (s resolvedTextSpec) preset() PresetDefinition {
	if s.PresetID == "" {
		return PresetDefinition{}
	}
	return s.PresetDef
}

// resolveTextRole selects the dedicated resolver from the validated semantic
// kind and its registered motion target. Template metadata wins for date and
// metric presentation routes, whose wire kind may intentionally be `number`.
func resolveTextRole(ri resolvedItem) textRole {
	if ri.Kind == KindImportantPhrase {
		return textRoleImportantPhrase
	}
	if ri.Kind == KindLowerThird {
		return textRoleLowerThird
	}
	switch semanticTextMotionTarget(ri.Item, ri.Kind) {
	case "date":
		return textRoleDate
	case "metric":
		return textRoleMetric
	}
	if ri.Kind == KindTimelineDate {
		return textRoleDate
	}
	if ri.Kind == KindMetricStat || ri.Kind == KindNumber {
		return textRoleMetric
	}
	return textRolePhrase
}

// resolveTextSpec dispatches to the semantic-role resolvers. Every resolver
// returns the same fully resolved input for the one shared text lowering.
func resolveTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	switch resolveTextRole(ri) {
	case textRoleImportantPhrase:
		return resolveImportantPhraseTextSpec(ri, src)
	case textRoleMetric:
		return resolveMetricTextSpec(ri, src)
	case textRoleDate:
		return resolveDateTextSpec(ri, src)
	case textRoleLowerThird:
		return resolveLowerThirdTextSpec(ri, src)
	default:
		return resolvePhraseTextSpec(ri, src)
	}
}

func resolvePhraseTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	spec, lines, err := resolveTextSpecBase(ri, src)
	if err != nil {
		return resolvedTextSpec{}, err
	}
	spec.StylePolicy = textRolePhrase
	return resolveTextSpecGeometry(ri, src, spec, lines), nil
}

func resolveImportantPhraseTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	spec, lines, err := resolveTextSpecBase(ri, src)
	if err != nil {
		return resolvedTextSpec{}, err
	}
	spec.StylePolicy = textRoleImportantPhrase
	spec.EntryExit = true
	return resolveTextSpecGeometry(ri, src, spec, lines), nil
}

func resolveMetricTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	spec, lines, err := resolveTextSpecBase(ri, src)
	if err != nil {
		return resolvedTextSpec{}, err
	}
	spec.StylePolicy = textRoleMetric
	spec.FitPolicy = resolvedTextFitPolicy(ri, textRoleMetric)
	return resolveTextSpecGeometry(ri, src, spec, lines), nil
}

func resolveDateTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	spec, lines, err := resolveTextSpecBase(ri, src)
	if err != nil {
		return resolvedTextSpec{}, err
	}
	spec.StylePolicy = textRoleDate
	spec.FitPolicy = resolvedTextFitPolicy(ri, textRoleDate)
	return resolveTextSpecGeometry(ri, src, spec, lines), nil
}

func resolveLowerThirdTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	spec, lines, err := resolveTextSpecBase(ri, src)
	if err != nil {
		return resolvedTextSpec{}, err
	}
	spec.StylePolicy = textRoleLowerThird
	return resolveTextSpecGeometry(ri, src, spec, lines), nil
}

// resolveTextSpecBase handles role-neutral text validation, wrapping, motion
// inputs and preset typography. The role resolvers above then add their own
// semantics before the shared geometry resolver finishes the spec.
func resolveTextSpecBase(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, int, error) {
	if strings.TrimSpace(ri.Item.Text) == "" {
		return resolvedTextSpec{}, 0, fmt.Errorf("overlay: item %q requires text (PipelineGen owns the displayed text)", ri.Item.ID)
	}
	wrapped, lines := wrapPhraseAt25(ri.Item.Text)
	spec := resolvedTextSpec{
		Text:         wrapped,
		MotionID:     ri.Item.MotionID,
		MotionParams: ri.Item.MotionParams,
		StyleParams:  ri.Params,
		MotionTarget: semanticTextMotionTarget(ri.Item, ri.Kind),
	}
	// Preset style and family layout travel with the spec; the compiler never
	// re-derives them from a template lookup.
	if ri.Preset.ID != "" {
		style, err := resolvedTextPresetStyle(ri.Preset, src.Language)
		if err != nil {
			return resolvedTextSpec{}, 0, err
		}
		spec.Style = style
		spec.PresetID = ri.Preset.ID
		spec.PresetDef = ri.Preset
	}
	return spec, lines, nil
}

// resolveTextSpecGeometry resolves the common position and box after each role
// has attached its fit policy. Explicit runtime geometry wins over preset
// layout, then the canonical text canvas.
func resolveTextSpecGeometry(ri resolvedItem, src *semanticPlan, spec resolvedTextSpec, lines int) resolvedTextSpec {
	posX, hasPosX := spec.StyleParams["position_x"].(float64)
	posY, hasPosY := spec.StyleParams["position_y"].(float64)
	switch {
	case hasPosX && hasPosY:
		spec.Position = []float64{posX, posY}
	default:
		spec.Position = resolveTextLayout(src.Width, src.Height)
		if hasPosX {
			spec.Position[0] = posX
		}
		if hasPosY {
			spec.Position[1] = posY
		}
	}
	if width, ok := numericValue(spec.StyleParams["width"]); ok && width > 0 {
		spec.BoxWidth = int(width)
	}
	if height, ok := numericValue(spec.StyleParams["height"]); ok && height > 0 {
		spec.BoxHeight = int(height)
	}
	if spec.BoxWidth <= 0 {
		spec.BoxWidth = src.Width
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxWidth > 0 && ri.Preset.Layout.BoxWidth < spec.BoxWidth {
			spec.BoxWidth = ri.Preset.Layout.BoxWidth
		}
	}
	if spec.BoxHeight <= 0 {
		spec.BoxHeight = DefaultTextBoxHeight
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxHeight > 0 {
			spec.BoxHeight = ri.Preset.Layout.BoxHeight
		}
	}
	if spec.FitPolicy == "none" {
		if requestedSize, ok := numericValue(spec.StyleParams["font_size_px"]); ok && requestedSize > 0 {
			minimumHeight := int(requestedSize*1.7) + 32
			if spec.BoxHeight < minimumHeight {
				spec.BoxHeight = minimumHeight
			}
		}
	}
	if lines > 1 {
		needed := lines*phraseLineHeight + 16 // glow/shadow padding
		if needed > spec.BoxHeight {
			if needed > src.Height {
				needed = src.Height
			}
			spec.BoxHeight = needed
		}
	}
	return spec
}

// resolvedTextFitPolicy resolves data-style no-shrink behavior before text
// compilation. The compiler receives only the decision, not semantic item kind.
func resolvedTextFitPolicy(ri resolvedItem, role textRole) string {
	if role != textRoleDate && role != textRoleMetric {
		return ""
	}
	if requestedSize, ok := numericValue(ri.Params["font_size_px"]); ok && requestedSize > 0 {
		return "none"
	}
	return ""
}

// compileResolvedText lowers a resolved text spec into one text layer. The
// function is deliberately semantic-agnostic: it must not branch on item
// kind, template or preset family, because every one of those decisions is
// the resolver's job. Adding a text role must never grow this function.
func compileResolvedText(layerID string, start, end int64, spec resolvedTextSpec) (Layer, error) {
	if strings.TrimSpace(spec.Text) == "" {
		return Layer{}, fmt.Errorf("overlay: text spec %q requires text (PipelineGen owns the displayed text)", layerID)
	}
	layer := Layer{ID: layerID, Type: "text", Text: spec.Text, StartFrame: start, DurationFrames: end - start}
	if spec.Style != nil {
		seeded := *spec.Style
		layer.Style = &seeded
	}
	if err := applyTextRuntimeOverrides(&layer, spec.StyleParams); err != nil {
		return Layer{}, fmt.Errorf("overlay: text spec %q: %w", layerID, err)
	}
	if layer.Style == nil {
		layer.Style = &LayerStyle{}
	}
	// The role owns the base treatment. The spec's explicit choices (runtime
	// override fill/font) always win over the role default.
	applyTextRoleBaseStyle(spec.StylePolicy, layer.Style, resolvedTextFontSize(spec), spec.OverrideFill)
	if spec.MinFontSize > 0 {
		layer.Style.MinFontSize = spec.MinFontSize
	}
	if spec.MaxFontSize > 0 {
		layer.Style.MaxFontSize = spec.MaxFontSize
	}
	applyResolvedTextRoleOverrides(layer.Style, spec.RoleOverrides)
	applyResolvedTextFont(&layer, spec)
	if !spec.OmitBox {
		layer.BoxWidth = spec.BoxWidth
		layer.BoxHeight = spec.BoxHeight
	}
	layer.Size = append([]float64(nil), spec.Size...)
	if len(layer.Size) != 2 {
		layer.Size = []float64{float64(spec.BoxWidth), float64(spec.BoxHeight)}
	}
	layer.Position = append([]float64(nil), spec.Position...)
	applyResolvedTextFitPolicy(&layer, spec)
	var animation *LayerAnimation
	if spec.MotionID != "" {
		// The preset's exit window is the fallback for a motion that declares
		// none; the wrapped text is the motion context so the stagger aligns
		// with the final lines. Motion-target admission stays where the plan
		// contract owns it (resolver/caller): this lowering lowers.
		resolved, err := animationForMotionTarget(spec.MotionID, spec.MotionParams, spec.Text, layer.DurationFrames, spec.PresetDef.Motion.Exit, layerID, spec.MotionTarget, spec.EntryExit)
		if err != nil {
			return Layer{}, err
		}
		animation = resolved
	} else if spec.PresetID != "" {
		// Official text presets lower their motion through the shared
		// animationForPreset path so word/glyph selectors are transported as
		// text animators instead of being silently compiled away.
		presetAnimation, err := animationForPreset(spec.preset(), spec.Text, layer.DurationFrames, spec.EntryExit)
		if err != nil {
			return Layer{}, err
		}
		animation = presetAnimation
	}
	if spec.EntryExit {
		animation = withPhraseEntryExit(animation, layer.DurationFrames)
	}
	applyMotionRouting(&layer, animation)
	return layer, nil
}

func applyResolvedTextRoleOverrides(style, overrides *LayerStyle) {
	if style == nil || overrides == nil {
		return
	}
	if overrides.Stroke != nil {
		style.Stroke = cloneLayerStyleStroke(overrides.Stroke)
	}
	if overrides.Shadow != nil {
		style.Shadow = cloneLayerStyleShadow(overrides.Shadow)
	}
	if overrides.Glow != nil {
		style.Glow = cloneLayerStyleGlow(overrides.Glow)
	}
	if overrides.Background != nil {
		style.Background = cloneLayerStyleBackground(overrides.Background)
	}
}

func cloneLayerStyleStroke(source *LayerStroke) *LayerStroke {
	if source == nil {
		return nil
	}
	return &LayerStroke{Color: source.Color, Width: source.Width}
}

func cloneLayerStyleShadow(source *LayerShadow) *LayerShadow {
	if source == nil {
		return nil
	}
	return &LayerShadow{Color: source.Color, Opacity: source.Opacity, Blur: source.Blur, Offset: append([]float64(nil), source.Offset...)}
}

func cloneLayerStyleGlow(source *LayerGlow) *LayerGlow {
	if source == nil {
		return nil
	}
	return &LayerGlow{Color: source.Color, Radius: source.Radius, Intensity: source.Intensity}
}

func cloneLayerStyleBackground(source *LayerBackground) *LayerBackground {
	if source == nil {
		return nil
	}
	var opacity *float64
	if source.Opacity != nil {
		value := *source.Opacity
		opacity = &value
	}
	return &LayerBackground{Color: source.Color, Opacity: opacity, Radius: source.Radius, Padding: append([]float64(nil), source.Padding...)}
}

// resolvedTextFontSize is the role style's base size: an explicitly resolved
// role size wins, then the preset-seeded style size.
func resolvedTextFontSize(spec resolvedTextSpec) float64 {
	if spec.FontSize > 0 {
		return spec.FontSize
	}
	if spec.Style != nil && spec.Style.FontSize > 0 {
		return spec.Style.FontSize
	}
	return 0
}

// applyResolvedTextFont resolves the layer font: an explicit spec font wins, then the
// style the preset seeded, then nothing (the preset already carries one).
func applyResolvedTextFont(layer *Layer, spec resolvedTextSpec) {
	if layer.Style == nil {
		return
	}
	if family := strings.TrimSpace(spec.OverrideFont); family != "" {
		fontPath, ok := runtimeFontPath(family)
		if !ok {
			// The resolver validated the family; reaching this branch means a
			// caller bypassed it, so fail loudly instead of guessing.
			fontPath = ""
		}
		layer.Style.Font = fontPath
	}
}

// applyResolvedTextFitPolicy applies the resolver's fit decision: "none" disables
// shrink-only fitting so an authored size survives verbatim.
func applyResolvedTextFitPolicy(layer *Layer, spec resolvedTextSpec) {
	if layer.Style == nil {
		return
	}
	switch spec.FitPolicy {
	case "none":
		layer.Style.FitMode = "none"
		layer.Style.MinFontSize = 0
		layer.Style.MaxFontSize = 0
	}
}

// resolvedTextPresetStyle builds the preset's LayerStyle with the language's
// official font applied. applyPresetDefinition keeps this same shape for the
// text family; extracting it lets the resolver hand the compiler a style
// without fabricating a whole Layer.
func resolvedTextPresetStyle(d PresetDefinition, language string) (*LayerStyle, error) {
	style := &LayerStyle{
		Font:        d.Style.FontFamily,
		FontSize:    d.Style.FontSize,
		Fill:        rgbaHex(d.Style.Fill),
		FitMode:     d.Style.TextFitMode,
		MinFontSize: d.Style.MinFontSize,
		MaxFontSize: d.Style.MaxFontSize,
	}
	// Runtime style overrides may have carried a workspace-relative alias;
	// the official language font is the resolver's canonical choice.
	style.Font = OfficialFontPathForLanguage(language)
	if d.Style.Stroke != nil {
		stroke := d.Style.Stroke
		style.Stroke = &LayerStroke{Color: stroke.Color, Width: stroke.Width}
	}
	if d.Style.Shadow != nil {
		s := d.Style.Shadow
		style.Shadow = &LayerShadow{Color: s.Color, Opacity: s.Opacity, Blur: s.Blur, Offset: append([]float64(nil), s.Offset...)}
	}
	if d.Style.Glow != nil {
		g := d.Style.Glow
		style.Glow = &LayerGlow{Radius: g.Radius, Intensity: g.Intensity, Color: g.Color}
	}
	return style, nil
}
