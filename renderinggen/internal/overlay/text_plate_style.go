package overlay

// text_plate_style.go owns the runtime decode of the optional plate border,
// shadow and entrance pool. The payload validator in
// refactored/internal/kernel/script enforces the same bounds one layer earlier;
// this decoder is the renderer's trust boundary and fails closed, because a
// dropped key would silently ship an unstyled plate.

import "fmt"

const (
	plateShadowDefaultColor   = "#000000"
	plateShadowDefaultOpacity = 0.55
	plateShadowDefaultBlur    = 12.0
	plateShadowMaxBlur        = 64.0
	plateShadowMaxOffset      = 128.0
	plateStrokeMaxWidth       = 64.0
	plateMotionMaxPool        = 8
)

func decodeRuntimePlateStroke(raw any) (*LayerStroke, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("params.style.background.stroke must be an object")
	}
	color, ok := entry["color"].(string)
	if !ok || !isHexColor(color) {
		return nil, fmt.Errorf("params.style.background.stroke.color must be #RRGGBB")
	}
	width, ok := numericValue(entry["width"])
	if !ok || !finite(width) || width < 0 || width > plateStrokeMaxWidth {
		return nil, fmt.Errorf("params.style.background.stroke.width must be between 0 and %g pixels", plateStrokeMaxWidth)
	}
	return &LayerStroke{Color: color, Width: width}, nil
}

func decodeRuntimePlateShadow(raw any) (*LayerShadow, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("params.style.background.shadow must be an object")
	}
	shadow := &LayerShadow{
		Color:   plateShadowDefaultColor,
		Opacity: plateShadowDefaultOpacity,
		Blur:    plateShadowDefaultBlur,
		Offset:  []float64{0, 6},
	}
	if value, exists := entry["color"]; exists {
		color, ok := value.(string)
		if !ok || !isHexColor(color) {
			return nil, fmt.Errorf("params.style.background.shadow.color must be #RRGGBB")
		}
		shadow.Color = color
	}
	if value, exists := entry["opacity"]; exists {
		opacity, ok := numericValue(value)
		if !ok || !finite(opacity) || opacity < 0 || opacity > 1 {
			return nil, fmt.Errorf("params.style.background.shadow.opacity must be between 0 and 1")
		}
		shadow.Opacity = opacity
	}
	if value, exists := entry["blur"]; exists {
		blur, ok := numericValue(value)
		if !ok || !finite(blur) || blur < 0 || blur > plateShadowMaxBlur {
			return nil, fmt.Errorf("params.style.background.shadow.blur must be between 0 and %g pixels", plateShadowMaxBlur)
		}
		shadow.Blur = blur
	}
	if value, exists := entry["offset"]; exists {
		offset, err := plateShadowOffset(value)
		if err != nil {
			return nil, err
		}
		shadow.Offset = offset
	}
	return shadow, nil
}

func plateShadowOffset(value any) ([]float64, error) {
	raw, ok := value.([]any)
	if !ok {
		if typed, isTyped := value.([]float64); isTyped {
			raw = make([]any, len(typed))
			for i, n := range typed {
				raw[i] = n
			}
		} else {
			return nil, fmt.Errorf("params.style.background.shadow.offset requires [x,y]")
		}
	}
	if len(raw) != 2 {
		return nil, fmt.Errorf("params.style.background.shadow.offset requires [x,y]")
	}
	offset := make([]float64, 2)
	for i, item := range raw {
		number, ok := numericValue(item)
		if !ok || !finite(number) || number < -plateShadowMaxOffset || number > plateShadowMaxOffset {
			return nil, fmt.Errorf("params.style.background.shadow.offset values must be between -%g and %g pixels", plateShadowMaxOffset, plateShadowMaxOffset)
		}
		offset[i] = number
	}
	return offset, nil
}

func decodeRuntimePlateMotions(raw any) ([]string, error) {
	values, ok := raw.([]any)
	if !ok {
		if typed, isTyped := raw.([]string); isTyped {
			values = make([]any, len(typed))
			for i, motion := range typed {
				values[i] = motion
			}
		} else {
			return nil, fmt.Errorf("params.style.background.motions must be an array of motion ids")
		}
	}
	if len(values) == 0 || len(values) > plateMotionMaxPool {
		return nil, fmt.Errorf("params.style.background.motions accepts 1..%d entries", plateMotionMaxPool)
	}
	motions := make([]string, len(values))
	for i, value := range values {
		motion, ok := value.(string)
		if !ok || !isPlateMotion(motion) {
			return nil, fmt.Errorf("params.style.background.motions %v is unsupported (supported: %s)", value, plateMotionVocabulary())
		}
		motions[i] = motion
	}
	return motions, nil
}
