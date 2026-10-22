// semantic_text_accents.go owns the phrase/brush visual-accent lowering: the
// recipe components a brush motion emits beside its text layer, and the text
// block anchor they are laid out against. It is part of the semantic lowering
// chain driven by semantic_compile.go, which dispatches to it from the text
// lane.
package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// Brush motions target text as well as images. Their recipe components must be
// emitted beside the phrase layer; the ordinary text motion lowering only
// consumes tracks and would otherwise silently omit the authored brush stroke.
func compileTextVisualAccentLayers(ri resolvedItem, src *semanticPlan, text Layer) ([]Layer, error) {
	definition, err := resolveMotionDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, err
	}
	if definition != nil && definition.Category == "documentary_highlighting_v1" {
		return compileDocumentaryPhraseMotion(ri, src, text, definition.ID)
	}
	// SSOT typography (typography_ssot.go): no motion branch may change the
	// font. The payload's font_family (or the preset/language default) owns
	// the face for every animation. ModernTypewriterDefaultFontFamily stays
	// as the documented constant for callers that explicitly opt into it via
	// params.font_family, but it is never forced here.
	_ = ModernTypewriterDefaultFontFamily
	var layers []Layer
	if definition == nil || definition.Category != "brush_v1" {
		layers = []Layer{text}
	} else {
		if !motionDefinitionAdmitsTarget(definition, TargetText) {
			return nil, fmt.Errorf("overlay: brush motion %q does not target text", definition.ID)
		}
		// Brush paths for phrases use the visible text block as their local
		// coordinate system. Phrase text layers span the full canvas so long
		// copy can wrap; using that entire box for a circle or underline makes
		// short phrases look lost and long phrases run edge-to-edge.
		textAnchor := brushTextAnchor(text, src)
		textDefinition := *definition
		if definition.ImageRecipe != nil {
			recipe := *definition.ImageRecipe
			recipe.Components = append([]motion.ImageMotionComponent(nil), definition.ImageRecipe.Components...)
			for i := range recipe.Components {
				switch recipe.Components[i].PathKind {
				case PathKindLine:
					if definition.ID != "brush_marker_highlight" {
						recipe.Components[i].PathKind = PathKindUnderline
					}
				case PathKindDoubleLine:
					recipe.Components[i].PathKind = PathKindUnderlineDouble
				case PathKindWave:
					recipe.Components[i].PathKind = PathKindUnderlineWave
				}
			}
			textDefinition.ImageRecipe = &recipe
		}
		layers, err = compileVisualAccentsComponents(src, textAnchor, textDefinition)
		if err != nil {
			return nil, err
		}
		for i := range layers {
			if layers[i].ID == textAnchor.ID {
				layers[i] = text
				break
			}
		}
	}
	return layers, nil
}

func brushTextAnchor(text Layer, src *semanticPlan) Layer {
	anchor := text
	fontSize := 72.0
	if text.Style != nil && text.Style.FontSize > 0 {
		fontSize = text.Style.FontSize
	}
	maxUnits := 0.0
	lineCount := 0
	for _, line := range strings.Split(text.Text, "\n") {
		lineCount++
		units := 0.0
		for _, r := range line {
			switch {
			case r == ' ':
				units += 0.31
			case strings.ContainsRune("ilI.,:;!'|", r):
				units += 0.30
			case strings.ContainsRune("MW@%&", r):
				units += 0.83
			case r >= '0' && r <= '9':
				units += 0.56
			default:
				units += 0.55
			}
		}
		if units > maxUnits {
			maxUnits = units
		}
	}
	width := maxUnits*fontSize + fontSize*1.05
	minWidth := fontSize * 3
	maxWidth := float64(src.Width - 96)
	if width < minWidth {
		width = minWidth
	}
	if width > maxWidth {
		width = maxWidth
	}
	height := math.Max(fontSize*1.5, float64(lineCount)*fontSize*1.3+16)
	maxHeight := float64(src.Height - 96)
	if height > maxHeight {
		height = maxHeight
	}
	anchor.Size = []float64{width, height}
	return anchor
}
