// phrase_wrap.go owns the phrase wrapping policy shared by phrase text,
// entity captions and layout measurement: the column budget, the line height
// and the hard-break-preserving wrapper.
package overlay

import (
	"strings"
	"unicode/utf8"
)

const phraseWrapCols = 25
const phraseLineHeight = 132

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
