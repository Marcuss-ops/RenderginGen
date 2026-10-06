package overlay

import (
	"math"
	"strings"
	"unicode"
)

type typewriterCursorStyle struct {
	shape  string
	width  float64
	height float64
	radius float64
	fill   []float64
}

// The cursor family is keyed by the same typewriter motion IDs used by the
// runtime phrase selector. Geometry stays deliberately bold enough to read at
// 1080p while the restrained accent colors keep it in the phrase style.
func typewriterCursorForMotion(id string, fontSize float64) (typewriterCursorStyle, bool) {
	styles := map[string]typewriterCursorStyle{
		"typewriter_clean":                            {shape: "rounded_rect", width: .105, height: .72, radius: .05, fill: []float64{.96, .98, 1, 1}},
		"typewriter_glitch":                           {shape: "rect", width: .17, height: .68, fill: []float64{1, .48, .58, 1}},
		"typewriter_neon":                             {shape: "rounded_rect", width: .11, height: .76, radius: .055, fill: []float64{.36, .84, 1, 1}},
		"typewriter_pop":                              {shape: "ellipse", width: .25, height: .25, fill: []float64{1, .77, .37, 1}},
		"typewriter_tracking":                         {shape: "rounded_rect", width: .58, height: .16, radius: .08, fill: []float64{.96, .98, 1, 1}},
		"typewriter_lift":                             {shape: "ellipse", width: .23, height: .23, fill: []float64{.53, .9, .78, 1}},
		"typewriter_slide_in":                         {shape: "rounded_rect", width: .19, height: .72, radius: .07, fill: []float64{.96, .98, 1, 1}},
		"typewriter_scale_up":                         {shape: "ellipse", width: .27, height: .27, fill: []float64{.5, .83, 1, 1}},
		"typewriter_blur_focus":                       {shape: "rounded_rect", width: .13, height: .8, radius: .065, fill: []float64{.72, .68, 1, 1}},
		"typewriter_soft_lift":                        {shape: "rounded_rect", width: .16, height: .7, radius: .08, fill: []float64{.7, .94, .82, 1}},
		"typewriter_modern_01_monospace_block_cursor": {shape: "rect", width: .18, height: .78, fill: []float64{1, .25, .3, 1}},
		"typewriter_modern_02_kinetic_scramble":       {shape: "rounded_rect", width: .12, height: .72, radius: .05, fill: []float64{.3, .95, .72, 1}},
		"typewriter_modern_03_soft_opacity_ramp":      {shape: "rounded_rect", width: .09, height: .72, radius: .04, fill: []float64{.96, .98, 1, 1}},
		"typewriter_modern_04_character_bounce":       {shape: "ellipse", width: .22, height: .22, fill: []float64{1, .76, .38, 1}},
		"typewriter_modern_05_backspace_correction":   {shape: "rect", width: .12, height: .72, fill: []float64{.95, .96, 1, 1}},
		"typewriter_modern_06_glow_beam_sweep":        {shape: "rounded_rect", width: .1, height: .82, radius: .05, fill: []float64{1, .28, .32, 1}},
		"typewriter_modern_07_word_snap":              {shape: "rect", width: .11, height: .72, fill: []float64{.96, .98, 1, 1}},
		"typewriter_modern_08_mechanical_y_shift":     {shape: "rounded_rect", width: .12, height: .75, radius: .05, fill: []float64{.47, .84, 1, 1}},
		"typewriter_modern_09_highlighter_expansion":  {shape: "rect", width: .15, height: .7, fill: []float64{1, .72, .28, 1}},
		"typewriter_modern_10_weight_ramp":            {shape: "rounded_rect", width: .1, height: .72, radius: .05, fill: []float64{.92, .95, 1, 1}},
		"typewriter_modern_11_dynamic_auto_wrap":      {shape: "rect", width: .12, height: .75, fill: []float64{.95, .96, 1, 1}},
		"typewriter_modern_12_glitch_pop":             {shape: "rect", width: .18, height: .72, fill: []float64{1, .3, .4, 1}},
		"typewriter_modern_13_elastic_leading_cursor": {shape: "rounded_rect", width: .1, height: .74, radius: .05, fill: []float64{.4, .92, .8, 1}},
		"typewriter_modern_14_focal_blur_dissolve":    {shape: "rounded_rect", width: .1, height: .78, radius: .05, fill: []float64{.72, .69, 1, 1}},
		"typewriter_modern_15_paper_punch_stencil":    {shape: "rect", width: .14, height: .72, fill: []float64{.96, .98, 1, 1}},
	}
	style, ok := styles[id]
	if !ok {
		return typewriterCursorStyle{}, false
	}
	style.width *= fontSize
	style.height *= fontSize
	style.radius *= fontSize
	return style, true
}

// compileTypewriterCursor emits a real overlay shape and drives its path from
// the text selector's own sampled keyframes. This makes every motion's caret
// share the exact timing curve used to reveal its glyphs.
func compileTypewriterCursor(ri resolvedItem, src *semanticPlan, text Layer) (Layer, bool) {
	fontSize := 112.0
	if text.Style != nil && text.Style.FontSize > 0 {
		fontSize = text.Style.FontSize
	}
	style, ok := typewriterCursorForMotion(ri.Item.MotionID, fontSize)
	if !ok || len(text.TextAnimators) == 0 || len(text.Position) < 2 {
		return Layer{}, false
	}
	isTimelineDate := canonicalTemplateID(ri.Item.Template) == "TIMELINE_DATE_CARD"
	if isTimelineDate {
		// Date overlays use a restrained documentary caret regardless of the
		// selected typewriter motion. Avoid the dots and underline shapes that
		// read as decorative punctuation rather than a typing cursor.
		style = typewriterCursorStyle{
			shape: "rounded_rect", width: .045 * fontSize, height: .58 * fontSize,
			radius: .022 * fontSize, fill: []float64{.48, .78, .82, 1},
		}
	}
	var sweep *AnimationTrack
	for _, animator := range text.TextAnimators {
		for i := range animator.Selectors {
			if animator.Selectors[i].Start != nil && len(animator.Selectors[i].Start.Keyframes) > 1 {
				sweep = animator.Selectors[i].Start
				break
			}
		}
		if sweep != nil {
			break
		}
	}
	if sweep == nil {
		return Layer{}, false
	}
	lines := strings.Split(text.Text, "\n")
	lineWidths := make([]float64, len(lines))
	lineAdvances := make([][]float64, len(lines))
	totalGlyphs := 0
	for lineIndex, line := range lines {
		advance := 0.0
		lineAdvances[lineIndex] = make([]float64, 0, len([]rune(line))+1)
		lineAdvances[lineIndex] = append(lineAdvances[lineIndex], 0)
		for _, char := range line {
			advance += typewriterGlyphAdvance(char, fontSize)
			lineAdvances[lineIndex] = append(lineAdvances[lineIndex], advance)
			if !unicode.IsSpace(char) {
				totalGlyphs++
			}
		}
		lineWidths[lineIndex] = advance
	}
	if totalGlyphs == 0 {
		return Layer{}, false
	}
	lineHeight := fontSize * 1.18
	blockHeight := float64(len(lines)-1) * lineHeight
	positionsX := make([]AnimationKeyframe, 0, len(sweep.Keyframes))
	positionsY := make([]AnimationKeyframe, 0, len(sweep.Keyframes))
	for _, key := range sweep.Keyframes {
		progress, ok := cursorNumericValue(key.Value)
		if !ok {
			return Layer{}, false
		}
		clampedProgress := math.Max(0, math.Min(100, progress))
		visible := int(math.Round(clampedProgress / 100 * float64(totalGlyphs)))
		remaining := visible
		lineIndex := 0
		charIndex := 0
		for lineIndex < len(lines) {
			glyphs := 0
			for _, char := range lines[lineIndex] {
				if !unicode.IsSpace(char) {
					glyphs++
				}
			}
			if remaining <= glyphs || lineIndex == len(lines)-1 {
				break
			}
			remaining -= glyphs
			lineIndex++
		}
		for runeIndex, char := range []rune(lines[lineIndex]) {
			if !unicode.IsSpace(char) {
				if remaining == 0 {
					break
				}
				remaining--
			}
			charIndex = runeIndex + 1
		}
		if visible >= totalGlyphs {
			charIndex = len([]rune(lines[lineIndex]))
		}
		localX := lineAdvances[lineIndex][charIndex] - lineWidths[lineIndex]*.5
		if visible >= totalGlyphs && clampedProgress >= 100 {
			localX += fontSize * .32
		}
		localY := float64(lineIndex)*lineHeight - blockHeight*.5
		positionsX = append(positionsX, AnimationKeyframe{Frame: key.Frame, Value: localX + sampleLayerOffset(text.Animation, "position_x", key.Frame)})
		positionsY = append(positionsY, AnimationKeyframe{Frame: key.Frame, Value: localY + sampleLayerOffset(text.Animation, "position_y", key.Frame)})
	}
	tracks := []AnimationTrack{{Property: "position_x", Keyframes: positionsX, Easing: "linear"}, {Property: "position_y", Keyframes: positionsY, Easing: "linear"}}
	// Copy layer-level transforms so the caret follows the phrase's entrance
	// choreography as well as its selector timing.
	if text.Animation != nil {
		for _, track := range text.Animation.Tracks {
			if track.Property == "scale_x" || track.Property == "scale_y" || track.Property == "scale" {
				copyTrack := track
				copyTrack.Keyframes = append([]AnimationKeyframe(nil), track.Keyframes...)
				tracks = append(tracks, copyTrack)
			}
		}
	}
	if isTimelineDate {
		// Let the caret blink briefly after the last glyph, then disappear while
		// the date remains on screen.
		last := sweep.Keyframes[len(sweep.Keyframes)-1].Frame
		tracks = append(tracks, AnimationTrack{Property: "opacity", Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 1},
			{Frame: last, Value: 1},
			{Frame: last + 8, Value: 0},
			{Frame: last + 14, Value: 1},
			{Frame: last + 22, Value: 0},
		}, Easing: "linear"})
	}
	return Layer{
		ID: text.ID + "__typewriter_cursor", Type: "shape",
		Size: []float64{style.width, style.height}, Position: []float64{text.Position[0], text.Position[1]},
		StartFrame: text.StartFrame, DurationFrames: text.DurationFrames,
		Shape:     &LayerShape{Type: style.shape, Radius: style.radius, Fill: style.fill},
		Animation: &LayerAnimation{Tracks: tracks},
	}, true
}

func sampleLayerOffset(animation *LayerAnimation, property string, frame int64) float64 {
	if animation == nil {
		return 0
	}
	for _, track := range animation.Tracks {
		if track.Property != property || len(track.Keyframes) == 0 {
			continue
		}
		if value, ok := cursorNumericValue(track.Keyframes[0].Value); ok && frame <= track.Keyframes[0].Frame {
			return value
		}
		for i := 1; i < len(track.Keyframes); i++ {
			left, right := track.Keyframes[i-1], track.Keyframes[i]
			if frame > right.Frame {
				continue
			}
			leftValue, leftOK := cursorNumericValue(left.Value)
			rightValue, rightOK := cursorNumericValue(right.Value)
			if !leftOK || !rightOK {
				return 0
			}
			if right.Frame <= left.Frame {
				return rightValue
			}
			ratio := float64(frame-left.Frame) / float64(right.Frame-left.Frame)
			return leftValue + (rightValue-leftValue)*ratio
		}
		if value, ok := cursorNumericValue(track.Keyframes[len(track.Keyframes)-1].Value); ok {
			return value
		}
	}
	return 0
}

func typewriterGlyphAdvance(char rune, fontSize float64) float64 {
	// Bricolage Grotesque metric ratios (including the word-space advance),
	// matching the modern Short Phrases face used by the date previews.
	var em float64
	switch {
	case unicode.IsSpace(char):
		em = .28
	case strings.ContainsRune("ilI|!.,:;'`", char):
		em = .32
	case strings.ContainsRune("mwMW@%&", char):
		em = .88
	case unicode.IsPunct(char):
		em = .43
	default:
		em = .63
	}
	return em * fontSize
}

func cursorNumericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}
