package overlay

import "testing"

func TestSubtitleStyleResolverAcceptsBothShadowOffsetContracts(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"axis fields": {"font_size_px": 54.0, "color": "#FFFFFF", "shadow": map[string]any{
			"color": "#000000", "opacity": 0.7, "blur_px": 10.0, "offset_x": 2.0, "offset_y": 5.0,
		}},
		"legacy vector": {"font_size_px": 54.0, "color": "#FFFFFF", "shadow": map[string]any{
			"color": "#000000", "opacity": 0.7, "blur_px": 10.0, "offset": []any{2.0, 5.0},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			style, err := parseStyleBlock(raw)
			if err != nil {
				t.Fatalf("parseStyleBlock: %v", err)
			}
			got, err := subtitleLayerStyle(style, "fonts/Poppins-Bold.ttf")
			if err != nil {
				t.Fatalf("subtitleLayerStyle: %v", err)
			}
			if got.Shadow == nil || len(got.Shadow.Offset) != 2 || got.Shadow.Offset[0] != 2 || got.Shadow.Offset[1] != 5 {
				t.Fatalf("shadow = %+v, want offset [2 5]", got.Shadow)
			}
		})
	}
}
