// Package webphrase builds the RenderingGen semantic plan for the web
// paragraph long/short phrase lane.
//
// ChrononTemplate owns the native WebPageHighlightPack and ShortPhrasePack. This
// package is the RenderingGen-side producer for the same editorial grammar using
// primitives the worker already lowers: a colour page background, text runs for
// the masthead/body, a native shape marker and an IMPORTANT_PHRASE text layer.
// The semantic document is compiled by internal/overlay exactly like a job plan,
// so selecting this lane never introduces a second renderer.
package webphrase

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Width and Height are the delivery canvas the user asked for.
	Width  = 1920
	Height = 1080
	FPS    = 30
	// MaxFrames is the ten-second ceiling at FPS.
	MaxFrames   = 10 * FPS
	LongFrames  = 8 * FPS
	ShortFrames = 7 * FPS

	contentWidth = 1680.0
	marginX      = 120.0

	// The runtime `inter` family resolves to assets/fonts/Inter.ttf in this
	// checkout, which is absent; DejaVu Sans is the bundled regular face that
	// actually materializes. Bold display runs use the bundled Inter-Bold.
	regularFamily = "dejavu_sans"
	boldFamily    = "inter_bold"
)

// Scene is one web paragraph phrase card. Mode selects the long article page
// or the short impact plate; both use the same page grammar.
type Scene struct {
	ID       string
	Mode     string
	Theme    string
	SiteName string
	Nav      string
	Kicker   string
	Headline string
	Byline   string
	Context  []string
	Phrase   string
	// MotionID is the web-category motion applied to the phrase layer. Empty
	// keeps the historical long/short default.
	MotionID string
	Frames   int
}

type palette struct {
	paper     string
	ink       string
	phraseInk string
	muted     string
	accent    string
	marker    string
	rule      string
}

// DefaultLong is the long-phrase editorial page. The phrase is the only loud
// element; the surrounding article copy stays quiet.
func DefaultLong() Scene {
	return Scene{
		ID:       "web_phrase_long_1080p",
		Mode:     "long",
		Theme:    "light",
		SiteName: "CHRONON NEWS",
		Nav:      "NBA   FOOTBALL   BUSINESS   CULTURE",
		Kicker:   "NBA TRADE DESK",
		Headline: "Sources: Kuminga camp pushing for Warriors exit",
		Byline:   "ANTHONY SLATER · JAN 15, 2026 · SAN FRANCISCO",
		Context: []string{
			"The fifth-year forward demanded a trade away from the Warriors,",
			"league sources said, ending weeks of tension over his rotation spot.",
			"Golden State has weighed staged return plans, including minutes charts.",
		},
		Phrase: "The February deadline does not just reshape one roster. It redraws the entire Western Conference.",
		Frames: LongFrames,
	}
}

// DefaultShort is the short impact plate. The sentence is deliberately short
// enough to read in a single glance and still keep a quiet context line.
func DefaultShort() Scene {
	return Scene{
		ID:       "web_phrase_short_1080p",
		Mode:     "short",
		Theme:    "light",
		SiteName: "CHRONON NEWS",
		Nav:      "NBA   FOOTBALL   BUSINESS   CULTURE",
		Kicker:   "THE TAKEAWAY",
		Headline: "One number explains the deadline",
		Byline:   "CHRONON NEWS · ANALYSIS",
		Context: []string{
			"A single phrase can carry the whole story.",
			"The page keeps the context quiet and the phrase loud.",
		},
		Phrase: "ELEVEN ATHLETES",
		Frames: ShortFrames,
	}
}

// BuildPlan returns a renderinggen.overlay-plan.v1 document. It is pure: the
// same Scene always produces the same bytes.
func BuildPlan(scene Scene) ([]byte, error) {
	if err := normalize(&scene); err != nil {
		return nil, err
	}
	durationMS := int64(scene.Frames) * 1000 / FPS
	colors, err := paletteFor(scene.Theme)
	if err != nil {
		return nil, err
	}
	paper, err := rgba(colors.paper)
	if err != nil {
		return nil, err
	}
	marker, err := rgba(colors.marker)
	if err != nil {
		return nil, err
	}
	rule, err := rgba(colors.rule)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, 24)
	items = append(items,
		shapeItem("web_rule_top", marginX, 146, contentWidth, 2, rule, 0.55, 0, durationMS),
		textItem("web_masthead", scene.SiteName, Width/2, 72, contentWidth, 40, 22, boldFamily, colors.accent, 0, durationMS),
		textItem("web_nav", scene.Nav, Width/2, 112, contentWidth, 34, 17, regularFamily, colors.muted, 0, durationMS),
		shapeItem("web_rule_masthead", marginX, 150, contentWidth, 2, rule, 0.65, 0, durationMS),
		textItem("web_kicker", scene.Kicker, Width/2, 196, contentWidth, 36, 20, boldFamily, colors.accent, 0, durationMS),
		textItem("web_headline", scene.Headline, Width/2, 264, contentWidth, 76, 54, boldFamily, colors.ink, 0, durationMS),
		textItem("web_byline", scene.Byline, Width/2, 330, contentWidth, 32, 20, regularFamily, colors.muted, 0, durationMS),
		shapeItem("web_rule_headline", marginX, 366, contentWidth, 2, rule, 0.5, 0, durationMS),
	)

	if scene.Mode == "long" {
		items = append(items, longBody(scene, colors, marker, durationMS)...)
	} else {
		items = append(items, shortBody(scene, colors, marker, durationMS)...)
	}

	items = append(items,
		shapeItem("web_rule_footer", marginX, 990, contentWidth, 2, rule, 0.45, 0, durationMS),
		textItem("web_footer", fmt.Sprintf("%s · WEB %s PHRASE · RENDERINGGEN", scene.SiteName, strings.ToUpper(scene.Mode)),
			Width/2, 1024, contentWidth, 26, 15, regularFamily, colors.muted, 0, durationMS),
	)

	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        scene.ID,
		"video_id":       scene.ID,
		"width":          Width,
		"height":         Height,
		"fps_num":        FPS,
		"fps_den":        1,
		"duration_ms":    durationMS,
		"background": map[string]any{
			"kind":  "color",
			"color": paper,
		},
		"items": items,
	}
	return json.MarshalIndent(plan, "", "  ")
}

func longBody(scene Scene, colors palette, marker []float64, duration int64) []map[string]any {
	items := make([]map[string]any, 0, 10)
	// Two context lines above the phrase, the rest below. The phrase owns the
	// visual centre so the page reads as one editorial unit.
	above := scene.Context
	if len(above) > 2 {
		above = above[:2]
	}
	below := scene.Context[2:]
	for index, line := range above {
		items = append(items, textItem(fmt.Sprintf("web_context_top_%d", index), line,
			Width/2, 436+float64(index)*48, 1680, 42, 30, regularFamily, colors.muted, 0, duration))
	}
	for index, line := range below {
		items = append(items, textItem(fmt.Sprintf("web_context_bottom_%d", index), line,
			Width/2, 812+float64(index)*48, 1680, 42, 30, regularFamily, colors.muted, 0, duration))
	}

	fontSize := 52.0
	lines := wrapPhrase(scene.Phrase, fontSize, 4)
	for fontSize > 38 && len(lines) > 4 {
		fontSize -= 4
		lines = wrapPhrase(scene.Phrase, fontSize, 4)
	}
	centerY := 640.0
	lineHeight := fontSize * 1.22
	mh := float64(len(lines))*lineHeight + 40
	mw := markerWidth(lines, fontSize, contentWidth-120)
	items = append(items,
		shapeItem("web_phrase_marker", Width/2-mw/2, centerY-mh/2, mw, mh, marker, 0.94, 400, duration),
		phraseItem("web_phrase_text", strings.Join(lines, "\n"), Width/2, centerY, mw, mh, fontSize,
			boldFamily, colors.phraseInk, scenePhraseMotion(scene), 0, duration),
	)
	return items
}

func shortBody(scene Scene, colors palette, marker []float64, duration int64) []map[string]any {
	items := make([]map[string]any, 0, 8)
	fontSize := 104.0
	lines := wrapPhrase(scene.Phrase, fontSize, 2)
	for fontSize > 72 && len(lines) > 2 {
		fontSize -= 6
		lines = wrapPhrase(scene.Phrase, fontSize, 2)
	}
	centerY := 600.0
	lineHeight := fontSize * 1.18
	mh := float64(len(lines))*lineHeight + 44
	mw := markerWidth(lines, fontSize, 1500)
	items = append(items,
		shapeItem("web_phrase_marker", Width/2-mw/2, centerY-mh/2, mw, mh, marker, 0.96, 350, duration),
		phraseItem("web_phrase_text", strings.Join(lines, "\n"), Width/2, centerY, mw, mh, fontSize,
			boldFamily, colors.phraseInk, scenePhraseMotion(scene), 0, duration),
	)
	for index, line := range scene.Context {
		items = append(items, textItem(fmt.Sprintf("web_context_%d", index), line,
			Width/2, 832+float64(index)*48, 1560, 40, 28, regularFamily, colors.muted, 0, duration))
	}
	return items
}

func scenePhraseMotion(scene Scene) string {
	if strings.TrimSpace(scene.MotionID) != "" {
		return strings.TrimSpace(scene.MotionID)
	}
	if scene.Mode == "long" {
		return "phrase_apple_clean_01_blur_soft_reveal"
	}
	return "typewriter_impact_push_short"
}

func textItem(id, text string, centerX, centerY, width, height, fontSize float64, family, fill string, startMS, endMS int64) map[string]any {
	return map[string]any{
		"id":        id,
		"preset_id": "static_text_smoke",
		"text":      text,
		"start_ms":  startMS,
		"end_ms":    endMS,
		"params": map[string]any{
			"font_family":  family,
			"font_size_px": fontSize,
			"fill":         fill,
			"width":        width,
			"height":       height,
			"position_x":   centerX,
			"position_y":   centerY,
			// The phrase role seeds a keyline for text over footage. On a
			// paper page it thickens every body run, so this producer opts out.
			"stroke_size": 0.0,
			"glow_size":   0.0,
		},
	}
}

func phraseItem(id, text string, centerX, centerY, width, height, fontSize float64, family, fill, motion string, startMS, endMS int64) map[string]any {
	return map[string]any{
		"id":          id,
		"kind":        "important_phrase",
		"template_id": "IMPORTANT_PHRASE",
		"preset_id":   "phrase_default",
		"motion_id":   motion,
		"motion_params": map[string]any{
			"enter_frames": 30,
		},
		"text":     text,
		"start_ms": startMS,
		"end_ms":   endMS,
		"params": map[string]any{
			"font_family":  family,
			"font_size_px": fontSize,
			"fill":         fill,
			"width":        width,
			"height":       height,
			"position_x":   centerX,
			"position_y":   centerY,
		},
	}
}

func shapeItem(id string, x, y, width, height float64, fill []float64, opacity float64, startMS, endMS int64) map[string]any {
	return map[string]any{
		"id":          id,
		"kind":        "shape",
		"template_id": "SHAPE",
		"start_ms":    startMS,
		"end_ms":      endMS,
		"params": map[string]any{
			"shape":  "rect",
			"width":  width,
			"height": height,
			// resolveShape forwards params.position to the Chronon layer, and
			// the renderer reads a non-screen-space shape position as the box
			// CENTRE. The helper accepts the authored top-left and converts it
			// once; passing the top-left here clipped the marker to one half of
			// the canvas (pinned by the pixel audit).
			"position": []float64{x + width/2, y + height/2},
			"fill":     fill,
			"opacity":  opacity,
		},
	}
}

func normalize(scene *Scene) error {
	scene.Mode = strings.ToLower(strings.TrimSpace(scene.Mode))
	scene.Theme = strings.ToLower(strings.TrimSpace(scene.Theme))
	if scene.Mode != "long" && scene.Mode != "short" {
		return fmt.Errorf("webphrase: mode %q must be long or short", scene.Mode)
	}
	if scene.Theme == "" {
		scene.Theme = "light"
	}
	if scene.Theme != "light" && scene.Theme != "dark" {
		return fmt.Errorf("webphrase: theme %q must be light or dark", scene.Theme)
	}
	if strings.TrimSpace(scene.ID) == "" {
		scene.ID = "web_phrase_" + scene.Mode
	}
	if scene.Frames == 0 {
		if scene.Mode == "long" {
			scene.Frames = LongFrames
		} else {
			scene.Frames = ShortFrames
		}
	}
	if scene.Frames < FPS || scene.Frames > MaxFrames {
		return fmt.Errorf("webphrase: %d frames is outside the 1..%d frame (ten-second) window", scene.Frames, MaxFrames)
	}
	if strings.TrimSpace(scene.Phrase) == "" {
		return fmt.Errorf("webphrase: phrase is required")
	}
	if strings.TrimSpace(scene.Headline) == "" {
		return fmt.Errorf("webphrase: headline is required")
	}
	if runeLen(scene.Phrase) > 260 {
		return fmt.Errorf("webphrase: phrase is %d characters; the web page layout accepts at most 260", runeLen(scene.Phrase))
	}
	if scene.Mode == "short" && len(scene.Context) > 2 {
		return fmt.Errorf("webphrase: short mode accepts at most 2 context lines")
	}
	if scene.SiteName == "" {
		scene.SiteName = "CHRONON NEWS"
	}
	if scene.Nav == "" {
		scene.Nav = "NBA   FOOTBALL   BUSINESS   CULTURE"
	}
	return nil
}

func paletteFor(theme string) (palette, error) {
	switch theme {
	case "light":
		return palette{
			paper:     "#F4F2EC",
			ink:       "#141414",
			phraseInk: "#141414",
			muted:     "#6D7379",
			accent:    "#B7410E",
			marker:    "#F0F500",
			rule:      "#C9C6BD",
		}, nil
	case "dark":
		return palette{
			paper:     "#0B0E13",
			ink:       "#F5F2E8",
			phraseInk: "#141414",
			muted:     "#A6ADB5",
			accent:    "#FB923C",
			marker:    "#F0F500",
			rule:      "#2A3038",
		}, nil
	default:
		return palette{}, fmt.Errorf("webphrase: unknown theme %q", theme)
	}
}

func rgba(hex string) ([]float64, error) {
	if len(hex) != 7 || hex[0] != '#' {
		return nil, fmt.Errorf("webphrase: invalid colour %q", hex)
	}
	value, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return nil, fmt.Errorf("webphrase: invalid colour %q: %w", hex, err)
	}
	return []float64{
		float64((value>>16)&0xFF) / 255,
		float64((value>>8)&0xFF) / 255,
		float64(value&0xFF) / 255,
		1,
	}, nil
}

func wrapPhrase(text string, fontSize float64, maxLines int) []string {
	// A large display font fits fewer characters per line. The factor is the
	// same conservative average the native web recipes use.
	maxRunes := int((contentWidth - 120) / (fontSize * 0.55))
	if maxRunes < 12 {
		maxRunes = 12
	}
	words := strings.Fields(strings.ReplaceAll(text, "\n", " "))
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if runeLen(candidate) <= maxRunes || current == "" {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func markerWidth(lines []string, fontSize, max float64) float64 {
	longest := 0
	for _, line := range lines {
		if n := runeLen(line); n > longest {
			longest = n
		}
	}
	width := float64(longest) * fontSize * 0.58
	width += 96
	if width < 520 {
		width = 520
	}
	if width > max {
		width = max
	}
	return width
}

func runeLen(value string) int {
	return len([]rune(value))
}
