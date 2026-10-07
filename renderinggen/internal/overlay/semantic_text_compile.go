package overlay

import (
	"fmt"
	"math"
	"strings"
)

// compileTextLayer lowers a text kind to a single text layer. Text is
// mandatory: PipelineGen owns the displayed text and RenderingGen never
// invents one (there is no entity_ref fallback).
func compileTextLayer(ri resolvedItem, src *semanticPlan, layerID string) (Layer, error) {
	rawText := ri.Item.Text
	if strings.TrimSpace(rawText) == "" {
		return Layer{}, fmt.Errorf("overlay: item %q requires text (PipelineGen owns the displayed text)", ri.Item.ID)
	}
	// Ogni 25 caratteri a capo correttamente: word-aware wrap that never cuts
	// a word. Applied at compile so Chronon's word wrap and the motion's
	// glyph stagger both operate on the finalized line breaks, and every line
	// remains horizontally Center-aligned by materialize_text.
	text, wrappedLines := wrapPhraseAt25(rawText)
	// Preserve the phrase kind's semantic flag for the motion floor even when
	// the raw kind was routed through the text branch.
	layer := Layer{ID: layerID, Type: "text", Text: text, StartFrame: ri.Start, DurationFrames: ri.End - ri.Start}
	if ri.Preset.ID != "" {
		applyPresetDefinition(&layer, ri.Preset)
		if layer.Style != nil {
			layer.Style.Font = OfficialFontPathForLanguage(src.Language)
		}
	}
	if err := applyTextRuntimeOverrides(&layer, ri.Params); err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
	}
	// Text placement is expressed as a layer top-left plus a local text box.
	// materialize_text uses the serialized box size, while the layer position
	// is applied exactly once by Chronon.
	if width, ok := numericValue(ri.Params["width"]); ok && width > 0 {
		layer.BoxWidth = int(width)
	}
	if height, ok := numericValue(ri.Params["height"]); ok && height > 0 {
		layer.BoxHeight = int(height)
	}
	if layer.BoxWidth <= 0 {
		layer.BoxWidth = src.Width
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxWidth > 0 && ri.Preset.Layout.BoxWidth < layer.BoxWidth {
			layer.BoxWidth = ri.Preset.Layout.BoxWidth
		}
	}
	if layer.BoxHeight <= 0 {
		layer.BoxHeight = DefaultTextBoxHeight
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxHeight > 0 {
			layer.BoxHeight = ri.Preset.Layout.BoxHeight
		}
	}
	// Date and metric cards may carry a runtime font size above the canonical
	// phrase_default size. Give that requested size enough vertical layout room;
	// otherwise shrink_only fitting silently reduces it back to the preset's
	// small minimum inside the preset's fixed 260px box.
	if ri.Kind == KindTimelineDate || ri.Kind == KindMetricStat || ri.Kind == KindNumber {
		if requestedSize, ok := numericValue(ri.Params["font_size_px"]); ok && requestedSize > 0 {
			minimumHeight := int(math.Ceil(requestedSize*1.7)) + 32
			if layer.BoxHeight < minimumHeight {
				layer.BoxHeight = minimumHeight
			}
			if layer.Style != nil {
				// These are short, high-priority callouts. Their requested size is
				// deliberate; shrink_only was silently restoring phrase_default's
				// 112px visual size even after the layout box grew.
				layer.Style.FitMode = "none"
				layer.Style.MinFontSize = 0
				layer.Style.MaxFontSize = 0
			}
		}
	}
	// Grow the text box when the 25-char wrap produced more lines than the
	// preset's 260px (≈3.3 lines) can hold — the renderer owns the final
	// line layout but the box must be tall enough to not clip centred lines.
	if wrappedLines > 1 {
		needed := wrappedLines*phraseLineHeight + 16 // glow/shadow padding
		if needed > layer.BoxHeight {
			if needed > src.Height {
				needed = src.Height
			}
			layer.BoxHeight = needed
		}
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}

	var textAnimation *LayerAnimation
	if ri.Item.MotionID != "" {
		target := semanticTextMotionTarget(ri.Item, ri.Kind)
		// Both halves of the selected motion travel, never one of them: the
		// layer tracks and the per-unit text animators are produced by the same
		// lowering pass (see lowerMotion). The preset's exit window is the
		// fallback for a motion that declares none. The wrapped text is used
		// as the motion context so the stagger aligns with the final lines.
		animation, err := animationForMotionTarget(ri.Item.MotionID, ri.Item.MotionParams, text, ri.End-ri.Start, ri.Preset.Motion.Exit, ri.Item.ID, target, ri.Kind == KindImportantPhrase)
		if err != nil {
			return Layer{}, err
		}
		textAnimation = animation
	} else if ri.Preset.ID != "" {
		// Official text presets lower their motion through the shared
		// animationForPreset path so word/glyph selectors (word_reveal,
		// character_cascade, ...) are transported as text animators instead of
		// being silently compiled away to an empty layer animation. Chronon
		// requires animation objects to carry tracks, so an animator-only
		// motion keeps the animation field absent and rides the layer's
		// text_animators contract.
		presetAnimation, err := animationForPreset(ri.Preset, text, ri.End-ri.Start, ri.Kind == KindImportantPhrase)
		if err != nil {
			return Layer{}, err
		}
		textAnimation = presetAnimation
	}
	if ri.Kind == KindImportantPhrase {
		textAnimation = withPhraseEntryExit(textAnimation, ri.End-ri.Start)
	}
	applyMotionRouting(&layer, textAnimation)
	if layer.Style != nil && layer.Position == nil {
		// position_x/position_y are absolute canvas coordinates of the text
		// centre — the same form the engine reads for text layers.
		posX, hasPosX := ri.Params["position_x"].(float64)
		posY, hasPosY := ri.Params["position_y"].(float64)
		if hasPosX && hasPosY {
			layer.Position = []float64{posX, posY}
		} else {
			layer.Position = resolveTextLayout(src.Width, src.Height)
			if hasPosX {
				layer.Position[0] = posX
			}
			if hasPosY {
				layer.Position[1] = posY
			}
		}
	}
	return layer, nil
}
