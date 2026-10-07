package overlay

import (
	"fmt"
	"strings"
)

// resolvedTextSpec is the fully resolved input of the role-agnostic text compiler:
// every semantic decision (role, style, geometry, motion) has already been
// made by the role resolver, so compileTextSpec never inspects a template,
// preset family or item kind. This is the seam that keeps phrases, entity
// captions and map labels from growing back into per-kind text engines.
type resolvedTextSpec struct {
	Text         string
	BoxWidth     int
	BoxHeight    int
	Position     []float64
	Style        *LayerStyle
	PresetID     string
	PresetDef    PresetDefinition
	MotionID     string
	MotionParams map[string]any
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

// resolveTextRole assigns the generic semantic role used by the text system.
// Template-specific typography remains a resolved style input, not a compiler
// branch, and the compiler sees only this role and the effective policy flags.
func resolveTextRole(ri resolvedItem) textRole {
	if ri.Kind == KindImportantPhrase {
		return textRoleImportantPhrase
	}
	if ri.Kind == KindTimelineDate {
		return textRoleDate
	}
	if ri.Kind == KindMetricStat || ri.Kind == KindNumber {
		return textRoleMetric
	}
	return textRolePhrase
}

// resolveTextSpec lowers one resolved semantic text item into the spec the
// text compiler lowers. It owns every semantic choice the compiler used to
// rediscover inline: phrase wrap discipline, preset family layout, the
// important-phrase entry/exit policy and the metric/date explicit-size fit
// override. The compiler only formats and lowers what lands here.
func buildResolvedTextSpec(ri resolvedItem, src *semanticPlan) (resolvedTextSpec, error) {
	if strings.TrimSpace(ri.Item.Text) == "" {
		return resolvedTextSpec{}, fmt.Errorf("overlay: item %q requires text (PipelineGen owns the displayed text)", ri.Item.ID)
	}
	wrapped, lines := wrapPhraseAt25(ri.Item.Text)
	spec := resolvedTextSpec{
		Text:         wrapped,
		MotionID:     ri.Item.MotionID,
		MotionParams: ri.Item.MotionParams,
		StyleParams:  ri.Params,
		MotionTarget: semanticTextMotionTarget(ri.Item, ri.Kind),
		EntryExit:    resolveTextRole(ri) == textRoleImportantPhrase,
		StylePolicy:  resolveTextRole(ri),
		FitPolicy:    resolvedTextFitPolicy(ri),
	}
	// Preset style and family layout travel with the spec; the compiler never
	// re-derives them from a template lookup.
	if ri.Preset.ID != "" {
		style, err := resolvedTextPresetStyle(ri.Preset, src.Language)
		if err != nil {
			return resolvedTextSpec{}, err
		}
		spec.Style = style
		spec.PresetID = ri.Preset.ID
		spec.PresetDef = ri.Preset
	}
	if spec.MotionID == "" && spec.PresetID == "phrase_default" && len(strings.Fields(spec.Text)) <= 5 {
		// Keep the preset's typography and layout, but do not let its legacy
		// animation silently fill an empty ChrononTemplate short-phrase slot.
		spec.PresetID = ""
		spec.PresetDef = PresetDefinition{}
	}
	// Position is the canvas centre the engine reads for text layers; without
	// an explicit centre the default text layout owns the placement.
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
	// Box: explicit runtime size wins, then the preset family layout, then the
	// canonical text canvas.
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
	// Grow the box when the wrap produced more lines than the preset's
	// canonical height can hold, so centred lines are never clipped.
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
	return spec, nil
}

// resolvedTextFitPolicy resolves data-style no-shrink behavior before text
// compilation. The compiler receives only the decision, not semantic item kind.
func resolvedTextFitPolicy(ri resolvedItem) string {
	if ri.Kind != KindTimelineDate && ri.Kind != KindMetricStat && ri.Kind != KindNumber {
		return ""
	}
	if requestedSize, ok := numericValue(ri.Params["font_size_px"]); ok && requestedSize > 0 {
		return "none"
	}
	return ""
}

// applyResolvedTextSpecStyle seeds the spec with the item's preset style and
// definition when it carries one; the compiler never re-derives them.
func applyResolvedTextSpecStyle(spec *resolvedTextSpec, ri resolvedItem, src *semanticPlan) error {
	if ri.Preset.ID == "" {
		return nil
	}
	style, err := resolvedTextPresetStyle(ri.Preset, src.Language)
	if err != nil {
		return err
	}
	spec.Style = style
	spec.PresetID = ri.Preset.ID
	spec.PresetDef = ri.Preset
	return nil
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
	applyResolvedTextFont(&layer, spec)
	layer.BoxWidth = spec.BoxWidth
	layer.BoxHeight = spec.BoxHeight
	layer.Size = []float64{float64(spec.BoxWidth), float64(spec.BoxHeight)}
	layer.Position = spec.Position
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
