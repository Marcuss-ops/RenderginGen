// subtitle_burn.go owns the ASS subtitle lowering shared by the semantic
// contract entry points: extracting the plan's subtitle policy, resolving its
// typed style and burning cue layers into a concrete plan. Geometry and
// typography come only from the plan — the worker never invents either.
package overlay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SubtitleAsset returns the content hash and burn policy declared by a
// semantic overlay plan. It is used after materialization, when the worker can
// safely read the verified subtitle bytes and lower them into Chronon layers.
func SubtitleAsset(raw []byte) (hash string, burn bool, ok bool, err error) {
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return "", false, false, fmt.Errorf("overlay: decode subtitle contract: %w", err)
	}
	if src.Subtitles == nil || len(src.Subtitles.AssetRefs) == 0 {
		return "", false, false, nil
	}
	ref := src.Subtitles.AssetRefs[0]
	return strings.ToLower(ref.SHA256), strings.EqualFold(strings.TrimSpace(src.Subtitles.Mode), "burn"), true, nil
}

// SubtitleStyleAsset resolves the plan's typed subtitle style into the
// concrete Chronon style + safe-area box used by BurnASSIntoPlan. The plan is
// the single style owner; resolution goes through the canonical visual style
// resolver (parseStyleBlock + subtitleLayerStyle), so a burn-mode plan
// without a fully declared style is rejected fail-closed — the compiler
// never substitutes its own typography.
func SubtitleStyleAsset(raw []byte) (*LayerStyle, SubtitleStyleBox, error) {
	var doc struct {
		Canvas struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"canvas"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: decode subtitle style contract: %w", err)
	}
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: decode subtitle style contract: %w", err)
	}
	if src.Subtitles == nil || src.Subtitles.Style == nil {
		return nil, SubtitleStyleBox{}, nil
	}
	block, err := parseStyleBlock(src.Subtitles.Style)
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	style, err := subtitleLayerStyle(block, "")
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	if block.Position == "" {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: subtitle style must declare position (placement is owned by PipelineGen)")
	}
	width, height := src.Width, src.Height
	if width <= 0 || height <= 0 {
		width, height = doc.Canvas.Width, doc.Canvas.Height
	}
	if width <= 0 || height <= 0 {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: subtitle burn requires a canvas")
	}
	position, size, err := subtitleCueGeometry(block, width, height, 1)
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	box := SubtitleStyleBox{
		Width:  int(size[0]),
		Height: int(size[1]),
		X:      int(position[0] + float64(width)/2 - size[0]/2),
		Y:      int(position[1] + float64(height)/2 - size[1]/2),
	}
	return style, box, nil
}

// BurnASSIntoPlan lowers an ASS subtitle track into ordinary Chronon text
// layers. Chronon then rasterizes each cue once, uploads the texture to GPU,
// and composites it before NVENC; no post-render ffmpeg subtitle pass is
// needed. The input plan must already be concrete render-plan.v2, the
// fontPath must be a prepared workspace-relative font asset, and the style
// must be fully typed by the caller (PipelineGen's plan) — this function
// invents no typography or geometry.
//
// BurnASSIntoPlanTyped is the typed-plan variant used by the processor: it
// mutates the in-memory plan instead of re-encoding JSON.
func BurnASSIntoPlan(planBytes, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) ([]byte, int, error) {
	var plan Plan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return nil, 0, fmt.Errorf("overlay: decode concrete plan for subtitles: %w", err)
	}
	count, err := appendSubtitleLayers(&plan, assBytes, fontPath, style, box)
	if err != nil {
		return nil, 0, err
	}
	out, err := plan.Marshal()
	if err != nil {
		return nil, 0, fmt.Errorf("overlay: encode subtitle layers: %w", err)
	}
	return out, count, nil
}

// BurnASSIntoPlanTyped mutates the typed plan in place with the lowered
// subtitle cue layers and returns the number of cue layers present afterwards.
// The processor path uses this to avoid a JSON round-trip; the plan is
// marshaled exactly once at the Chronon boundary.
func BurnASSIntoPlanTyped(plan *Plan, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) (int, error) {
	return appendSubtitleLayers(plan, assBytes, fontPath, style, box)
}

// appendSubtitleLayers is the shared lowering core: validate inputs, parse
// the ASS cues, append one GPU text layer per cue and return the count.
func appendSubtitleLayers(plan *Plan, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) (int, error) {
	if plan == nil {
		return 0, fmt.Errorf("overlay: burn subtitles requires a plan")
	}
	if strings.TrimSpace(fontPath) == "" {
		return 0, fmt.Errorf("overlay: burn subtitles requires a prepared font")
	}
	if style == nil || style.FontSize == 0 {
		return 0, fmt.Errorf("overlay: burn subtitles requires a typed style with font_size (no compiler defaults)")
	}
	if box.Width <= 0 || box.Height <= 0 {
		return 0, fmt.Errorf("overlay: burn subtitles requires a positive subtitle box")
	}
	cues, err := parseASSCues(assBytes, plan.Canvas.FPSNum, plan.Canvas.FPSDen)
	if err != nil {
		return 0, err
	}
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			return 0, nil // idempotent: cues already lowered
		}
	}
	for i, cue := range cues {
		if cue.Text == "" || cue.EndFrame <= cue.StartFrame {
			continue
		}
		cueStyle := *style
		cueStyle.Font = fontPath
		plan.Layers = append(plan.Layers, Layer{
			ID: "subtitle_cue_" + strconv.Itoa(i), Type: "text", Text: cue.Text,
			Size: []float64{float64(box.Width), float64(box.Height)},
			// Chronon layer positions are offsets from the canvas centre and
			// address the layer centre. Convert the absolute safe-area box.
			Position: []float64{
				float64(box.X) + float64(box.Width)*0.5 - float64(plan.Canvas.Width)*0.5,
				float64(box.Y) + float64(box.Height)*0.5 - float64(plan.Canvas.Height)*0.5,
			},
			Style:      &cueStyle,
			StartFrame: cue.StartFrame, DurationFrames: cue.EndFrame - cue.StartFrame,
		})
	}
	count := 0
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			count++
		}
	}
	return count, nil
}

type assCue struct {
	StartFrame int64
	EndFrame   int64
	Text       string
}

func parseASSCues(raw []byte, fpsNum, fpsDen int) ([]assCue, error) {
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("overlay: invalid subtitle fps")
	}
	var cues []assCue
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(strings.ToLower(line), "dialogue:") {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(line[len("Dialogue:"):]), ",", 10)
		if len(fields) < 10 {
			continue
		}
		start, err := assTimeFrame(strings.TrimSpace(fields[1]), fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		end, err := assTimeFrame(strings.TrimSpace(fields[2]), fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(fields[9])
		text = assBraceTagRe.ReplaceAllString(text, "")
		text = strings.ReplaceAll(strings.ReplaceAll(text, `\N`, "\n"), `\n`, "\n")
		cues = append(cues, assCue{StartFrame: start, EndFrame: end, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("overlay: read subtitles: %w", err)
	}
	return cues, nil
}

// assBraceTagRe strips ASS override blocks like {\i1} from dialogue text.
var assBraceTagRe = regexp.MustCompile(`\{[^}]*\}`)

func assTimeFrame(raw string, fpsNum, fpsDen int) (int64, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("overlay: invalid ASS timestamp %q", raw)
	}
	seconds, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	secParts := strings.SplitN(parts[2], ".", 2)
	whole, err := strconv.Atoi(secParts[0])
	if err != nil {
		return 0, err
	}
	centis := 0
	if len(secParts) == 2 {
		centis, err = strconv.Atoi(secParts[1])
		if err != nil {
			return 0, err
		}
	}
	ms := int64(seconds*3600000 + minutes*60000 + whole*1000 + centis*10)
	return (ms*int64(fpsNum) + 1000*int64(fpsDen) - 1) / (1000 * int64(fpsDen)), nil
}
