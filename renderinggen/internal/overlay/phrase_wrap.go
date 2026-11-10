// phrase_wrap.go owns the phrase wrapping policy shared by phrase text,
// entity captions and layout measurement: the column budget, the line height
// and the hard-break-preserving wrapper.
package overlay

import (
	"strings"
	"unicode/utf8"
)

const phraseWrapCols = 25

func wrapPhraseAt25(text string) (string, int) {
	if utf8.RuneCountInString(strings.TrimSpace(text)) <= phraseWrapCols {
		return strings.TrimSpace(text), 1
	}
	// Respect existing hard breaks: each paragraph is wrapped independently
	// so an authored manuale \n is never collapsed.
	paragraphs := strings.Split(text, "\n")
	var out []string
	for _, para := range paragraphs {
		trimmed := strings.TrimSpace(para)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(trimmed)
		if len(words) == 0 {
			continue
		}
		var cur strings.Builder
		curLen := 0
		for idx, word := range words {
			wLen := utf8.RuneCountInString(word)
			if curLen == 0 {
				cur.WriteString(word)
				curLen = wLen
			} else if curLen+1+wLen <= phraseWrapCols {
				cur.WriteString(" ")
				cur.WriteString(word)
				curLen += 1 + wLen
			} else {
				out = append(out, cur.String())
				cur.Reset()
				cur.WriteString(word)
				curLen = wLen
			}
			if idx == len(words)-1 {
				out = append(out, cur.String())
			}
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(text), 1
	}
	lineCount := len(out)
	// Drop a leading/trailing empty produced by borderline splits.
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
		lineCount--
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
		lineCount--
	}
	if lineCount <= 0 {
		return strings.TrimSpace(text), 1
	}
	return strings.Join(out, "\n"), lineCount
}

// wrapPhraseByPixelBudget wraps proportional text against a renderer-space
// width estimate. It uses conservative per-rune advances (including wider
// capitals, narrow punctuation and full-width CJK glyphs) because font files
// are resolved by the native renderer, not loaded by this semantic compiler.
func wrapPhraseByPixelBudget(text string, maxWidth, fontSize float64) (string, int) {
	text = strings.TrimSpace(text)
	if maxWidth <= 0 || fontSize <= 0 {
		return wrapPhraseAt25(text)
	}
	paragraphs := strings.Split(text, "\n")
	var lines []string
	for _, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := ""
		lineWidth := 0.0
		for _, word := range words {
			wordWidth := estimatedTextWidth(word, fontSize)
			spaceWidth := estimatedTextWidth(" ", fontSize)
			if line != "" && lineWidth+spaceWidth+wordWidth > maxWidth {
				lines = append(lines, line)
				line, lineWidth = "", 0
			}
			if line != "" {
				line += " "
				lineWidth += spaceWidth
			}
			// Keep an unbreakable word visible by splitting it at rune
			// boundaries when it alone is wider than the available box.
			for _, r := range word {
				advance := runeAdvance(r) * fontSize
				if line != "" && lineWidth+advance > maxWidth {
					lines = append(lines, line)
					line, lineWidth = "", 0
				}
				line += string(r)
				lineWidth += advance
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return text, 1
	}
	return strings.Join(lines, "\n"), len(lines)
}

func estimatedTextWidth(text string, fontSize float64) float64 {
	width := 0.0
	for _, r := range text {
		width += runeAdvance(r) * fontSize
	}
	return width
}

func runeAdvance(r rune) float64 {
	if r == ' ' || r == '\t' {
		return 0.30
	}
	if r >= 0x300 && r <= 0x36f {
		return 0
	}
	if r >= 0x2e80 && r <= 0x9fff {
		return 1.0
	}
	switch r {
	case 'i', 'I', 'l', '!', '.', ',', ':', ';', '\'', '`', '|':
		return 0.30
	case 'm', 'w', 'M', 'W', '@', '%':
		return 0.82
	case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'J', 'K', 'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'X', 'Y', 'Z':
		return 0.66
	default:
		return 0.54
	}
}
