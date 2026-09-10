// cue_burn.go lowers the word-aligned cue JSON (produced by PipelineGen's
// align_words_cues.py bridge) into ordinary Chronon text layers. It mirrors
// subtitle_burn.go: each cue becomes one GPU text layer composited before
// NVENC — no post-render ffmpeg pass — but with word-exact start/end from
// the whisper alignment instead of ASS dialogue times.
//
// The contract is fail-closed and caller-owned, like every other lowering:
// the cue document must carry the schema marker, the style must be fully
// typed by the plan (the worker invents no typography or geometry), and the
// lowering is idempotent (already-lowered cue layers short-circuit to zero).
package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CueSchemaV1 identifies the word-alignment cue document.
const CueSchemaV1 = "renderinggen.cues.v1"

// CueWord is one word-exact timestamp inside a cue.
type CueWord struct {
	StartMS     int64   `json:"start_ms"`
	EndMS       int64   `json:"end_ms"`
	Text        string  `json:"text"`
	Probability float64 `json:"probability,omitempty"`
}

// Cue is one timed overlay unit: either a single word (word-exact mode) or a
// grouped phrase whose Words slice preserves per-word timing for animators.
type Cue struct {
	StartMS     int64     `json:"start_ms"`
	EndMS       int64     `json:"end_ms"`
	Text        string    `json:"text"`
	Probability float64   `json:"probability,omitempty"`
	Words       []CueWord `json:"words,omitempty"`
}

// CueDocument is the canonical alignment document emitted by the bridge.
type CueDocument struct {
	SchemaVersion string `json:"schema_version"`
	Language      string `json:"language"`
	Cues          []Cue  `json:"cues"`
}

// ParseCueDocument decodes and validates the alignment document. It rejects
// documents with the wrong schema marker, no cues, or any cue with inverted
// timing or empty text — a silently mangled alignment would burn wrong
// timings into the render, so it fails loudly instead.
func ParseCueDocument(raw []byte) ([]Cue, error) {
	var doc CueDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("overlay: decode cue document: %w", err)
	}
	if doc.SchemaVersion != CueSchemaV1 {
		return nil, fmt.Errorf("overlay: unsupported cue schema %q (want %q)", doc.SchemaVersion, CueSchemaV1)
	}
	if len(doc.Cues) == 0 {
		return nil, fmt.Errorf("overlay: cue document carries no cues")
	}
	for i, c := range doc.Cues {
		if c.Text == "" {
			return nil, fmt.Errorf("overlay: cue %d carries no text", i)
		}
		if c.EndMS <= c.StartMS {
			return nil, fmt.Errorf("overlay: cue %d timing inverted or zero (%d..%d)", i, c.StartMS, c.EndMS)
		}
	}
	return doc.Cues, nil
}

// BurnCuesIntoPlanTyped appends one GPU text layer per cue to the typed plan
// and returns the number of cue layers present afterwards. fpsNum/fpsDen
// convert millisecond timings to the canvas frame grid (floor on start,
// ceil on end so a cue never disappears a frame early).
//
// Geometry follows the subtitle contract: the caller-provided safe-area box
// positions the cue; the plan's typed style supplies every visual property.
// The lowering is idempotent: plans already carrying cue layers are returned
// unchanged with their existing count.
func BurnCuesIntoPlanTyped(plan *Plan, cues []Cue, fontPath string, style *LayerStyle, box SubtitleStyleBox) (int, error) {
	if plan == nil {
		return 0, fmt.Errorf("overlay: burn cues requires a plan")
	}
	if len(cues) == 0 {
		return 0, fmt.Errorf("overlay: burn cues requires at least one cue")
	}
	if plan.Canvas.FPSNum <= 0 || plan.Canvas.FPSDen <= 0 {
		return 0, fmt.Errorf("overlay: invalid canvas fps for cue burn")
	}
	if strings.TrimSpace(fontPath) == "" {
		return 0, fmt.Errorf("overlay: burn cues requires a prepared font")
	}
	if style == nil || style.FontSize == 0 {
		return 0, fmt.Errorf("overlay: burn cues requires a typed style with font_size (no compiler defaults)")
	}
	if box.Width <= 0 || box.Height <= 0 {
		return 0, fmt.Errorf("overlay: burn cues requires a positive cue box")
	}
	// Idempotency guard (same contract as the ASS burn path).
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "cue_") {
			return countCueLayers(plan), nil
		}
	}
	fps := float64(plan.Canvas.FPSNum) / float64(plan.Canvas.FPSDen)
	for i, cue := range cues {
		startFrame := msToFrameFloor(cue.StartMS, fps)
		endFrame := msToFrameCeil(cue.EndMS, fps)
		if endFrame <= startFrame {
			continue
		}
		cueStyle := *style
		cueStyle.Font = fontPath
		plan.Layers = append(plan.Layers, Layer{
			ID:   "cue_" + strconv.Itoa(i),
			Type: "text",
			Text: cue.Text,
			Size: []float64{float64(box.Width), float64(box.Height)},
			// Chronon layer positions are offsets from the canvas centre and
			// address the layer centre — the same conversion as subtitles.
			Position: []float64{
				float64(box.X) + float64(box.Width)*0.5 - float64(plan.Canvas.Width)*0.5,
				float64(box.Y) + float64(box.Height)*0.5 - float64(plan.Canvas.Height)*0.5,
			},
			Style:          &cueStyle,
			StartFrame:     startFrame,
			DurationFrames: endFrame - startFrame,
		})
	}
	return countCueLayers(plan), nil
}

func countCueLayers(plan *Plan) int {
	count := 0
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "cue_") {
			count++
		}
	}
	return count
}

// msToFrameFloor converts milliseconds to the first frame whose timestamp is
// >= ms (a cue never appears before its word is spoken).
func msToFrameFloor(ms int64, fps float64) int64 {
	return int64(math.Floor(float64(ms) / 1000.0 * fps))
}

// msToFrameCeil converts milliseconds to the first frame whose timestamp is
// >= ms (a cue never disappears before its word ends).
func msToFrameCeil(ms int64, fps float64) int64 {
	return int64(math.Ceil(float64(ms) / 1000.0 * fps))
}
