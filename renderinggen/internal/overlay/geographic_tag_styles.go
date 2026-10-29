package overlay

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// geographicTagStyleCatalog is shared with the tag showcase renderer. It is
// the single source for named map-label presets and their visual defaults.
//
//go:embed geographic_tag_styles.v1.json
var geographicTagStyleCatalog []byte

var geographicTagStylePresets = func() map[string]SemanticMapTextStyle {
	var catalog struct {
		Styles []geographicTagStyleEntry `json:"styles"`
	}
	if err := json.Unmarshal(geographicTagStyleCatalog, &catalog); err != nil {
		panic(fmt.Sprintf("invalid embedded geographic tag style catalog: %v", err))
	}
	presets := make(map[string]SemanticMapTextStyle, len(catalog.Styles))
	for _, entry := range catalog.Styles {
		if entry.Name == "" {
			panic("geographic tag style catalog contains an empty name")
		}
		if _, exists := presets[entry.Name]; exists {
			panic(fmt.Sprintf("duplicate geographic tag style preset %q", entry.Name))
		}
		presets[entry.Name] = entry.SemanticMapTextStyle
	}
	return presets
}()

type geographicTagStyleEntry struct {
	Name string `json:"name"`
	SemanticMapTextStyle
}

func geographicTagStylePreset(name string) (SemanticMapTextStyle, bool) {
	preset, ok := geographicTagStylePresets[name]
	return preset, ok
}

func resolveGeographicTagStyle(style *SemanticMapTextStyle) *SemanticMapTextStyle {
	if style == nil {
		return nil
	}
	resolved := *style
	if style.Preset != "" {
		if preset, ok := geographicTagStylePreset(style.Preset); ok {
			resolved = preset
			resolved.Preset = style.Preset
		}
	}
	if style.FontFamily != "" {
		resolved.FontFamily = style.FontFamily
	}
	if style.FontSizePX != nil {
		resolved.FontSizePX = style.FontSizePX
	}
	if style.Fill != "" {
		resolved.Fill = style.Fill
	}
	if style.Stroke != nil {
		resolved.Stroke = style.Stroke
	}
	if style.Shadow != nil {
		resolved.Shadow = style.Shadow
	}
	if style.Glow != nil {
		resolved.Glow = style.Glow
	}
	if style.Background != nil {
		resolved.Background = style.Background
	}
	return &resolved
}
