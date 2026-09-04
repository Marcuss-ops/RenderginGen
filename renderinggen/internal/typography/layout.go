package typography

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

func typographyColor(v []float64) string {
	if len(v) != 4 {
		return "#FFFFFFFF"
	}
	return fmt.Sprintf("#%02X%02X%02X%02X", int(v[0]*255), int(v[1]*255), int(v[2]*255), int(v[3]*255))
}

var interBoldWidths = map[rune]float64{
	' ': 0.28, '.': 0.28, ',': 0.28, ':': 0.28, ';': 0.28, '!': 0.32, '?': 0.52,
	'-': 0.35, '_': 0.55, '/': 0.35, '\'': 0.25, '"': 0.40,
	'a': 0.58, 'b': 0.62, 'c': 0.56, 'd': 0.62, 'e': 0.58, 'f': 0.36, 'g': 0.62,
	'h': 0.60, 'i': 0.28, 'j': 0.28, 'k': 0.56, 'l': 0.28, 'm': 0.90, 'n': 0.60,
	'o': 0.62, 'p': 0.62, 'q': 0.62, 'r': 0.38, 's': 0.52, 't': 0.36, 'u': 0.60,
	'v': 0.54, 'w': 0.80, 'x': 0.54, 'y': 0.54, 'z': 0.52,
	'A': 0.70, 'B': 0.68, 'C': 0.72, 'D': 0.74, 'E': 0.62, 'F': 0.58, 'G': 0.76,
	'H': 0.74, 'I': 0.30, 'J': 0.48, 'K': 0.66, 'L': 0.56, 'M': 0.88, 'N': 0.74,
	'O': 0.78, 'P': 0.66, 'Q': 0.78, 'R': 0.68, 'S': 0.64, 'T': 0.60, 'U': 0.72,
	'V': 0.66, 'W': 0.96, 'X': 0.66, 'Y': 0.64, 'Z': 0.62,
}

func CharWidth(r rune, fontSize float64) float64 {
	w, ok := interBoldWidths[r]
	if !ok {
		w = 0.60
	}
	return w * fontSize
}

func MeasureText(text string, fontSize float64, tracking float64) float64 {
	total := 0.0
	for _, r := range text {
		total += CharWidth(r, fontSize) + tracking
	}
	return total
}

func typographyAnimation(name string, duration int64) *overlay.LayerAnimation {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "static" || name == "none" {
		return nil
	}
	enter := duration
	if enter > 8 {
		enter = 8
	}
	if enter < 1 {
		enter = 1
	}
	track := overlay.AnimationTrack{Property: "opacity", Easing: "out_cubic", Keyframes: []overlay.AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: enter, Value: 1.0}}}
	switch name {
	case "slide_in", "reveal_from_bottom", "slide_up":
		track.Property = "position"
		track.Keyframes[0].Value = []float64{0, 40}
		track.Keyframes[1].Value = []float64{0, 0}
	case "scale_drop", "scale_in", "soft_pop":
		track.Property = "scale"
		track.Keyframes[0].Value = 0.85
	}
	return &overlay.LayerAnimation{Tracks: []overlay.AnimationTrack{track}}
}

func BuildWordLayers(prefix, text, font string, fontSize float64, color []float64, centerY float64, staggerFrames, totalDuration int64, animPreset ...string) []overlay.Layer {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	spaceWidth := CharWidth(' ', fontSize)
	wordWidths := make([]float64, len(words))
	totalWidth := 0.0
	for i, w := range words {
		wordWidths[i] = MeasureText(w, fontSize, 0)
		totalWidth += wordWidths[i]
		if i > 0 {
			totalWidth += spaceWidth
		}
	}
	currentX := -totalWidth / 2.0
	animation := "static"
	if len(animPreset) > 0 {
		animation = animPreset[0]
	}
	layers := make([]overlay.Layer, 0, len(words))
	for i, w := range words {
		wWidth := wordWidths[i]
		startFrame := int64(i) * staggerFrames
		duration := totalDuration - startFrame
		if duration <= 0 {
			duration = 1
		}
		layers = append(layers, overlay.Layer{ID: prefix + "_word_" + string(rune('0'+i)), Type: "text", Text: w, Style: &overlay.LayerStyle{Font: font, FontSize: fontSize, Fill: typographyColor(color)}, Position: []float64{currentX + wWidth/2.0, centerY}, StartFrame: startFrame, DurationFrames: duration, Opacity: 1.0, Animation: typographyAnimation(animation, duration)})
		currentX += wWidth + spaceWidth
	}
	return layers
}

func BuildGlyphLayers(prefix, text, font string, fontSize float64, color []float64, centerY float64, staggerFrames, totalDuration int64, animPreset ...string) []overlay.Layer {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	charWidths := make([]float64, len(runes))
	totalWidth := 0.0
	for i, r := range runes {
		charWidths[i] = CharWidth(r, fontSize)
		totalWidth += charWidths[i]
	}
	currentX := -totalWidth / 2.0
	animation := "static"
	if len(animPreset) > 0 {
		animation = animPreset[0]
	}
	layers := make([]overlay.Layer, 0, len(runes))
	for i, r := range runes {
		cWidth := charWidths[i]
		if r != ' ' {
			startFrame := int64(i) * staggerFrames
			duration := totalDuration - startFrame
			if duration <= 0 {
				duration = 1
			}
			layers = append(layers, overlay.Layer{ID: prefix + "_char_" + string(r) + "_" + string(rune('a'+i%26)), Type: "text", Text: string(r), Style: &overlay.LayerStyle{Font: font, FontSize: fontSize, Fill: typographyColor(color)}, Position: []float64{currentX + cWidth/2.0, centerY}, StartFrame: startFrame, DurationFrames: duration, Opacity: 1.0, Animation: typographyAnimation(animation, duration)})
		}
		currentX += cWidth
	}
	_ = utf8.RuneCountInString(text)
	return layers
}
