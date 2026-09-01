package overlay

import (
	"fmt"
	"strings"
)

// FastEntityOverlay defines the high-speed overlay contract for entities and phrases.
// Supported types: "text" | "image"
// Supported animations: "fade" | "slide" | "scale" | "static"
type FastEntityOverlay struct {
	Type       string    `json:"type"`                // "text" | "image"
	StartFrame int64     `json:"start_frame"`         // inclusive start frame
	EndFrame   int64     `json:"end_frame"`           // exclusive end frame
	Position   string    `json:"position,omitempty"`  // anchor / preset position ("lower_third", "center", "safe_area", "image_left", "image_right")
	Size       float64   `json:"size,omitempty"`      // font size for text, or box size for image
	Opacity    float64   `json:"opacity,omitempty"`   // alpha [0.0, 1.0] (default 1.0)
	Animation  string    `json:"animation,omitempty"` // "fade" | "slide" | "scale" | "static"
	Asset      string    `json:"asset,omitempty"`     // image file path
	Text       string    `json:"text,omitempty"`      // phrase string
	Font       string    `json:"font,omitempty"`      // font path / font family
	Color      []float64 `json:"color,omitempty"`     // RGBA [r, g, b, a] in [0, 1]
	Translate  []float64 `json:"translate,omitempty"` // [dx, dy] offset
	Scale      float64   `json:"scale,omitempty"`     // scale multiplier (default 1.0)
}

// Default video contract parameters matching refactored pipeline standards.
const (
	DefaultFPSNum = 24
	DefaultFPSDen = 1
	DefaultWidth  = 1920
	DefaultHeight = 1080
)

// BuildPlanFromEntityOverlays constructs a concrete chronon.render-plan.v1 from a background video
// and a sequence of fast entity overlays. It emits only generic layer data and
// tracks; animation names are retained solely as debug metadata.
func genericAnimation(name string, duration int64) *LayerAnimation {
	enter := int(duration)
	if enter > 12 {
		enter = 12
	}
	if enter < 1 {
		enter = 1
	}
	track := AnimationTrack{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0}, {Frame: int64(enter), Value: 1}}}
	switch name {
	case "slide", "slide_in", "slide_left", "slide_up":
		track.Property = "position_y"
		track.Keyframes[0].Value, track.Keyframes[1].Value = 40, 0
	case "scale", "scale_in", "scale_drop", "pop":
		track.Property = "scale"
		track.Keyframes[0].Value = 0.85
	}
	return &LayerAnimation{Unit: "layer", EnterDurationFrames: enter, Tracks: []AnimationTrack{track}}
}

func BuildPlanFromEntityOverlays(
	jobID string,
	width, height int,
	fpsNum, fpsDen int,
	totalDurationFrames int64,
	bgVideoPath string,
	overlays []FastEntityOverlay,
) (*Plan, error) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	if fpsNum <= 0 {
		fpsNum = DefaultFPSNum
	}
	if fpsDen <= 0 {
		fpsDen = DefaultFPSDen
	}
	if totalDurationFrames <= 0 {
		return nil, fmt.Errorf("entity_contract: total duration must be positive")
	}

	plan := &Plan{
		Schema:  "chronon.render-plan",
		Version: 1,
		JobID:   jobID,
		Canvas: Canvas{
			Width:          width,
			Height:         height,
			FPSNum:         fpsNum,
			FPSDen:         fpsDen,
			DurationFrames: totalDurationFrames,
		},
		Output: Output{
			Path:   "result.mp4",
			Format: "mp4",
			Codec:  "h264",
		},
	}

	// Add background video layer
	if bgVideoPath != "" {
		plan.Layers = append(plan.Layers, Layer{
			ID:             "bg_video",
			Type:           "video",
			Source:         bgVideoPath,
			BoxWidth:       width,
			BoxHeight:      height,
			Fit:            "cover",
			StartFrame:     0,
			DurationFrames: totalDurationFrames,
			Opacity:        1.0,
		})
	}

	// Add entity overlays
	for i, ov := range overlays {
		layerID := fmt.Sprintf("entity_%d_%s", i+1, strings.ToLower(ov.Type))
		duration := ov.EndFrame - ov.StartFrame
		if duration <= 0 {
			duration = 1
		}

		opacity := ov.Opacity
		if opacity <= 0.0 {
			opacity = 1.0
		}

		animationName := strings.ToLower(strings.TrimSpace(ov.Animation))
		if animationName == "" || animationName == "none" {
			animationName = "static"
		}
		var layerAnim *LayerAnimation
		if animationName != "static" {
			layerAnim = genericAnimation(animationName, duration)
		}

		switch strings.ToLower(strings.TrimSpace(ov.Type)) {
		case "image":
			if ov.Asset == "" {
				return nil, fmt.Errorf("entity_contract: overlay %d (image) requires asset path", i)
			}
			boxSize := 260
			if ov.Size > 0 {
				boxSize = int(ov.Size)
			}
			pos := ov.Position
			if pos == "" {
				pos = "center"
			}

			// Entity overlays expose only generic absolute geometry. Editorial
			// anchor resolution belongs to the common RenderingGen compiler.
			posX := float64(width-boxSize) / 2.0
			posY := float64(height-boxSize) / 2.0
			switch strings.ToLower(pos) {
			case "image_left", "left":
				posX = 0
			case "image_right", "right":
				posX = float64(width - boxSize)
			}
			if len(ov.Translate) == 2 {
				posX += ov.Translate[0]
				posY += ov.Translate[1]
			}
			if ov.Scale > 0 {
				boxSize = int(float64(boxSize) * ov.Scale)
			}

			imgLayer := Layer{
				ID:             layerID,
				Type:           "image",
				Asset:          ov.Asset,
				BoxWidth:       boxSize,
				BoxHeight:      boxSize,
				Fit:            "contain",
				Position:       []float64{posX, posY},
				StartFrame:     ov.StartFrame,
				DurationFrames: duration,
				Opacity:        opacity,
				Animation:      layerAnim,
			}
			plan.Layers = append(plan.Layers, imgLayer)

		case "text":
			if ov.Text == "" {
				return nil, fmt.Errorf("entity_contract: overlay %d (text) requires text content", i)
			}
			fontSize := 58.0
			if ov.Size > 0 {
				fontSize = ov.Size
			}
			font := ov.Font
			if font == "" {
				font = "assets/fonts/Poppins-Bold.ttf"
			}
			color := ov.Color
			if len(color) != 4 {
				color = []float64{1.0, 1.0, 1.0, 1.0}
			}

			txtLayer := Layer{
				ID:             layerID,
				Type:           "text",
				Text:           ov.Text,
				Font:           font,
				FontSize:       fontSize,
				Color:          color,
				StartFrame:     ov.StartFrame,
				DurationFrames: duration,
				Opacity:        opacity,
				Animation:      layerAnim,
			}
			if len(ov.Translate) == 2 {
				txtLayer.Position = []float64{ov.Translate[0], ov.Translate[1]}
			}
			plan.Layers = append(plan.Layers, txtLayer)

		default:
			return nil, fmt.Errorf("entity_contract: unsupported overlay type %q", ov.Type)
		}
	}

	return plan, nil
}
