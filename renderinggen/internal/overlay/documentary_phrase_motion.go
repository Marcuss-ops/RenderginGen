package overlay

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Documentary Highlighting motions are phrase-relative. Text byte ranges are
// exact; their marker shapes are laid out against the same wrapped lines as
// the text layer and use a lightweight Playfair advance model for geometry.
// The renderer owns glyph shaping; these shapes only provide the colored ink
// plate and are padded slightly so antialiased glyph edges remain covered.
type documentaryTarget struct {
	start  int
	end    int
	line   int
	prefix string
	value  string
}

var documentaryNeedles = map[string][][]string{
	"doc_01_yellow_marker_sweep":       {{"condanna"}, {"decisione"}},
	"doc_03_documentary_quote_marker":  {{"contestare gli accertamenti"}, {"consulenze tecniche"}},
	"doc_04_archival_source_highlight": {{"atti ufficiali depositati"}, {"atti ufficiali"}},
	"doc_05_selective_phrase_tracking": {{"valutazioni tecniche contrapposte"}, {"tecniche contrapposte"}},
	"doc_06_red_keyword_punch":         {{"responsabilità"}, {"responsabilita"}},
	"doc_08_mixed_font_emphasis":       {{"sentenze definitive"}, {"indagini ancora aperte"}},
	"doc_09_cinematic_underline_draw":  {{"ulteriori integrazioni"}, {"archiviazione"}},
	"doc_10_selective_contrast_focus":  {{"metodi e ai suoi limiti"}, {"limiti"}},
	"doc_11_headline_cinematic_zoom":   {{"presunzione di innocenza"}, {"sentenza definitiva"}},
	"doc_13_analog_scanner_highlight":  {{"fatti e interpretazioni"}, {"interpretazioni"}},
	"doc_14_dual_highlight_sequence":   {{"fatti"}, {"interpretazioni"}},
	"doc_15_documentary_source_focus":  {{"fonti verificabili"}, {"fonti"}},
}

// These public phrase_highlight IDs are catalog aliases for the established
// documentary recipes. Resolve them before applying targeting, color, and
// marker behavior so both vocabularies produce the same render semantics.
var documentaryPhraseAliases = map[string]string{
	"phrase_highlight_soft_marker":    "doc_01_yellow_marker_sweep",
	"phrase_highlight_archive_yellow": "doc_04_archival_source_highlight",
	"phrase_highlight_evidence_red":   "doc_06_red_keyword_punch",
	"phrase_highlight_phrase_focus":   "doc_10_selective_contrast_focus",
	"phrase_highlight_document_scan":  "doc_13_analog_scanner_highlight",
}

func compileDocumentaryPhraseMotion(ri resolvedItem, src *semanticPlan, text Layer, id string) ([]Layer, error) {
	if text.Text == "" || text.Style == nil || len(text.Position) != 2 {
		return nil, fmt.Errorf("overlay: documentary motion %q requires resolved phrase text geometry", id)
	}
	semanticID := id
	if canonical, ok := documentaryPhraseAliases[id]; ok {
		semanticID = canonical
	}
	if _, ok := documentaryMotionIDs[semanticID]; !ok {
		return nil, fmt.Errorf("overlay: unsupported documentary phrase motion %q", id)
	}
	targets := documentaryTargets(text.Text, semanticID)
	// Keep phrase color owned by the selected visual style. In particular, a
	// highlight motion must never fade the underlying copy to grey: the marker
	// is the emphasis and the type remains solid black (or the caller's color).
	var markers []Layer
	var emphasis []Layer
	text.Style.Fill = "#000000"
	for index, target := range targets {
		if target.start < 0 || target.end > len(text.Text) || target.start >= target.end {
			continue
		}
		spanID := fmt.Sprintf("%s-target-%d", id, index+1)
		span := map[string]any{"start": target.start, "end": target.end, "semantic_id": spanID}
		color := documentaryTextColor(semanticID)
		if semanticID == "doc_08_mixed_font_emphasis" && lightSolidBackground(src.Background) {
			color = backgroundHex(src.Background.Color)
		}
		if color != "" {
			span["style"] = map[string]any{"color": color}
		}
		if semanticID == "doc_08_mixed_font_emphasis" {
			style, _ := span["style"].(map[string]any)
			if style == nil {
				style = map[string]any{}
				span["style"] = style
			}
			style["font_size_mult"] = 1.04
		}
		text.TextSpans = append(text.TextSpans, span)
		if documentaryNeedsMarker(semanticID) {
			markers = append(markers, documentaryMarker(text, src, semanticID, target, index)...)
		}
		// doc_08 is catalog-pinned (documentary_highlighting_runtime_test):
		// the alternate Inter-Bold layer is the emphasis, one per target.
		if semanticID == "doc_08_mixed_font_emphasis" && lightSolidBackground(src.Background) {
			emphasis = append(emphasis, documentaryMixedFontLayer(text, src, target, index))
		}
	}
	return append(append(markers, text), emphasis...), nil
}

var documentaryMotionIDs = map[string]struct{}{
	"doc_01_yellow_marker_sweep": {}, "doc_02_multiline_stagger_highlight": {}, "doc_03_documentary_quote_marker": {},
	"doc_04_archival_source_highlight": {}, "doc_05_selective_phrase_tracking": {}, "doc_06_red_keyword_punch": {},
	"doc_07_word_impact_reveal": {}, "doc_08_mixed_font_emphasis": {}, "doc_09_cinematic_underline_draw": {},
	"doc_10_selective_contrast_focus": {}, "doc_11_headline_cinematic_zoom": {}, "doc_12_quote_line_by_line_reveal": {},
	"doc_13_analog_scanner_highlight": {}, "doc_14_dual_highlight_sequence": {}, "doc_15_documentary_source_focus": {},
}

func documentaryTargets(text, id string) []documentaryTarget {
	if id == "doc_02_multiline_stagger_highlight" || id == "doc_12_quote_line_by_line_reveal" {
		var out []documentaryTarget
		at := 0
		for lineIndex, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) != "" {
				out = append(out, documentaryTarget{start: at, end: at + len(line), line: lineIndex, value: line})
			}
			at += len(line) + 1
		}
		return out
	}

	if needles, ok := documentaryNeedles[id]; ok {
		for _, alternatives := range needles {
			for _, needle := range alternatives {
				at := strings.Index(strings.ToLower(text), strings.ToLower(needle))
				if at >= 0 {
					if id != "doc_14_dual_highlight_sequence" {
						return []documentaryTarget{targetAt(text, at, at+len(needle))}
					}
					first := targetAt(text, at, at+len(needle))
					secondAt := strings.LastIndex(strings.ToLower(text), "interpretazioni")
					if secondAt > at {
						return []documentaryTarget{first, targetAt(text, secondAt, secondAt+len("interpretazioni"))}
					}
					return []documentaryTarget{first}
				}
			}
		}
	}

	// If a motion's preferred wording is absent, emphasize the actual phrase as
	// a whole. Guessing the last token made the marker appear on arbitrary words
	// unrelated to the motion's intended emphasis.
	var out []documentaryTarget
	at := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, targetAt(text, at, at+len(line)))
		}
		at += len(line) + 1
	}
	return out
}

func targetAt(text string, start, end int) documentaryTarget {
	line := strings.Count(text[:start], "\n")
	lineStart := strings.LastIndex(text[:start], "\n") + 1
	return documentaryTarget{start: start, end: end, line: line, prefix: text[lineStart:start], value: text[start:end]}
}

func documentaryTextColor(id string) string {
	switch id {
	case "doc_06_red_keyword_punch":
		return "#FFFFFF"
	case "doc_10_selective_contrast_focus":
		return "#111827"
	case "doc_08_mixed_font_emphasis":
		return "#D97706"
	default:
		return ""
	}
}

func lightSolidBackground(background *semanticBackground) bool {
	if background == nil || background.Kind != "color" || len(background.Color) < 3 {
		return false
	}
	return background.Color[0] >= 0.92 && background.Color[1] >= 0.92 && background.Color[2] >= 0.92
}

func backgroundHex(color []float64) string {
	if len(color) < 3 {
		return "#FFFFFF"
	}
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(color[0]*255)), int(math.Round(color[1]*255)), int(math.Round(color[2]*255)))
}

func documentaryMixedFontLayer(text Layer, src *semanticPlan, target documentaryTarget, index int) Layer {
	lineStart := strings.LastIndex(text.Text[:target.start], "\n") + 1
	lineEnd := len(text.Text)
	if n := strings.Index(text.Text[target.end:], "\n"); n >= 0 {
		lineEnd = target.end + n
	}
	line := text.Text[lineStart:lineEnd]
	prefix := text.Text[lineStart:target.start]
	value := text.Text[target.start:target.end]
	fontSize := text.Style.FontSize
	if fontSize <= 0 {
		fontSize = 72
	}
	lineWidth := documentaryAdvance(line, fontSize)
	valueWidth := documentaryAdvance(value, fontSize)
	x := -lineWidth/2 + documentaryAdvance(prefix, fontSize) + valueWidth/2
	width := valueWidth + fontSize*0.35
	lineCount := strings.Count(text.Text, "\n") + 1
	y := text.Position[1] + (float64(target.line)-(float64(lineCount)-1)/2)*fontSize*1.3
	return Layer{
		ID: fmt.Sprintf("%s-mixed-font-%d", text.ID, index+1), Type: "text", Text: value,
		Size: []float64{width, fontSize * 1.5}, Position: []float64{text.Position[0] + x, y},
		StartFrame: text.StartFrame, DurationFrames: text.DurationFrames, ScreenSpace: true,
		Style: &LayerStyle{Font: "assets/fonts/Inter-Bold.ttf", FontSize: fontSize * 1.04, Fill: "#202020",
			FitMode: FitModeShrinkOnly, MinFontSize: fontSize * 1.04, MaxFontSize: fontSize * 1.04},
		Animation: &LayerAnimation{Tracks: []AnimationTrack{{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 8, Value: 1.0}}}}},
	}
}

func documentaryHighlightColor(id string) string {
	switch id {
	case "doc_06_red_keyword_punch":
		return "#DC2626"
	case "doc_15_documentary_source_focus":
		return "#06B6D4"
	case "doc_10_selective_contrast_focus":
		return "#38BDF8"
	case "doc_08_mixed_font_emphasis":
		return "#D97706"
	case "doc_13_analog_scanner_highlight":
		return "#FFE600"
	default:
		return "#FFE600"
	}
}

func documentaryNeedsMarker(id string) bool {
	switch id {
	case "doc_01_yellow_marker_sweep", "doc_02_multiline_stagger_highlight", "doc_03_documentary_quote_marker",
		"doc_04_archival_source_highlight", "doc_05_selective_phrase_tracking", "doc_06_red_keyword_punch", "doc_08_mixed_font_emphasis",
		"doc_09_cinematic_underline_draw", "doc_10_selective_contrast_focus", "doc_11_headline_cinematic_zoom",
		"doc_07_word_impact_reveal", "doc_12_quote_line_by_line_reveal", "doc_13_analog_scanner_highlight", "doc_14_dual_highlight_sequence",
		"doc_15_documentary_source_focus":
		return true
	default:
		return false
	}
}

func documentaryMarker(text Layer, src *semanticPlan, id string, target documentaryTarget, index int) []Layer {
	fontSize := text.Style.FontSize
	if fontSize <= 0 {
		fontSize = 72
	}
	lineText := text.Text
	lineStart := strings.LastIndex(lineText[:target.start], "\n") + 1
	lineEnd := len(lineText)
	if n := strings.Index(lineText[target.end:], "\n"); n >= 0 {
		lineEnd = target.end + n
	}
	line := lineText[lineStart:lineEnd]
	prefix := lineText[lineStart:target.start]
	value := lineText[target.start:target.end]
	if id == "doc_02_multiline_stagger_highlight" || id == "doc_12_quote_line_by_line_reveal" {
		prefix, value = "", line
	}
	lineAdvance := documentaryAdvance(line, fontSize)
	prefixAdvance := documentaryAdvance(prefix, fontSize)
	valueAdvance := documentaryAdvance(value, fontSize)
	width := math.Max(fontSize*1.6, valueAdvance+fontSize*0.20)
	if id == "doc_02_multiline_stagger_highlight" || id == "doc_12_quote_line_by_line_reveal" {
		width = lineAdvance + fontSize*0.20
	}
	if width > float64(src.Width-64) {
		width = float64(src.Width - 64)
	}
	lineCount := strings.Count(text.Text, "\n") + 1
	// Match the text compiler's wrapped phrase layout. The previous 1.3 factor
	// placed successive plates farther apart than the rendered Playfair lines.
	lineHeight := fontSize * 1.12
	y := text.Position[1] + (float64(target.line)-(float64(lineCount)-1)/2)*lineHeight
	markerHeight := fontSize * 0.72
	markerColor := documentaryRGB(documentaryHighlightColor(id))
	if id == "doc_09_cinematic_underline_draw" {
		markerColor = []float64{0.16, 0.20, 0.25, 0.96}
		markerHeight = math.Max(3, fontSize*0.035)
		y += fontSize * 0.54
	}
	x := -lineAdvance/2 + prefixAdvance + valueAdvance/2
	if id == "doc_02_multiline_stagger_highlight" || id == "doc_12_quote_line_by_line_reveal" {
		x = 0
	}
	enter := int64(36)
	if id == "doc_02_multiline_stagger_highlight" || id == "doc_12_quote_line_by_line_reveal" {
		enter = 20 + int64(index)*18
	}
	if duration := text.DurationFrames; enter > duration/2 {
		enter = duration / 2
	}
	marker := Layer{
		ID: fmt.Sprintf("%s-marker-%d", text.ID, index+1), Type: "shape",
		Size: []float64{width, markerHeight}, Position: []float64{text.Position[0] + x, y},
		StartFrame: text.StartFrame, DurationFrames: text.DurationFrames, ScreenSpace: true,
		Opacity: floatPointer(0.88), Shape: &LayerShape{Type: "rounded_rect", Fill: markerColor, Radius: math.Min(fontSize*0.06, markerHeight/2)},
		Animation: &LayerAnimation{Tracks: []AnimationTrack{
			{Property: "scale_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.01}, {Frame: enter, Value: 1.0}}},
			{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -width * 0.495}, {Frame: enter, Value: 0.0}}},
		}},
	}
	return []Layer{marker}
}

func documentaryAdvance(text string, fontSize float64) float64 {
	units := 0.0
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			units += 0.22
		case strings.ContainsRune("ilI.,:;!'|", r):
			units += 0.20
		case strings.ContainsRune("MW@%&", r):
			units += 0.58
		case unicode.IsPunct(r):
			units += 0.23
		default:
			units += 0.34
		}
	}
	return units * fontSize
}

func documentaryRGB(hex string) []float64 {
	if len(hex) != 7 || hex[0] != '#' {
		return []float64{1, 0.9, 0, 0.88}
	}
	var r, g, b int
	_, _ = fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	return []float64{float64(r) / 255, float64(g) / 255, float64(b) / 255, 0.88}
}

func floatPointer(v float64) *float64 { return &v }
