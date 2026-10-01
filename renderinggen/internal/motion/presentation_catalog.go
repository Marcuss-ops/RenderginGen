package motion

import (
	"encoding/json"
	"fmt"
	"sort"
)

type PresentationPreset struct {
	ID     string            `json:"id"`
	Tracks []TrackDefinition `json:"tracks"`
}
type PresentationFamily struct {
	ID                string               `json:"id"`
	SupportedTemplate string               `json:"supported_template"`
	SupportedContent  []string             `json:"supported_content"`
	DurationBounds    DurationBounds       `json:"duration_bounds"`
	RenderSafe        bool                 `json:"render_safe"`
	Seeded            bool                 `json:"seeded"`
	Presets           []PresentationPreset `json:"presets"`
}
type PresentationCatalog struct {
	Schema   string               `json:"schema"`
	Version  int                  `json:"version"`
	Families []PresentationFamily `json:"families"`
}

var expectedPresentationFamilies = map[string]int{"metric_v1": 10, "date_v1": 10, "entity_card_v1": 10}

func validateEntityPresentationCatalog(raw json.RawMessage, motions []MotionDefinition) error {
	if len(raw) == 0 {
		return fmt.Errorf("motion: canonical catalog is missing entity_presentation")
	}
	var catalog PresentationCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return fmt.Errorf("motion: decode entity_presentation: %w", err)
	}
	if catalog.Schema != "chronontemplate.entity-presentation.v1" || catalog.Version != 1 || len(catalog.Families) != 3 {
		return fmt.Errorf("motion: invalid entity_presentation schema/version/family count")
	}
	byID := make(map[string]MotionDefinition, len(motions))
	for _, d := range motions {
		byID[d.ID] = d
	}
	seenFamilies, seenPresets := map[string]bool{}, map[string]bool{}
	for _, family := range catalog.Families {
		count, known := expectedPresentationFamilies[family.ID]
		if !known || seenFamilies[family.ID] {
			return fmt.Errorf("motion: unknown or duplicate entity presentation family %q", family.ID)
		}
		seenFamilies[family.ID] = true
		if len(family.Presets) != count || family.SupportedTemplate == "" || len(family.SupportedContent) == 0 || !family.RenderSafe || family.DurationBounds.MinimumFrames < 1 || family.DurationBounds.MaximumFrames < family.DurationBounds.MinimumFrames {
			return fmt.Errorf("motion: entity presentation family %q has invalid metadata or preset count", family.ID)
		}
		for _, preset := range family.Presets {
			if preset.ID == "" || seenPresets[preset.ID] {
				return fmt.Errorf("motion: empty or duplicate entity presentation preset %q", preset.ID)
			}
			seenPresets[preset.ID] = true
			d, ok := byID[preset.ID]
			if !ok {
				return fmt.Errorf("motion: entity presentation preset %q missing from motions", preset.ID)
			}
			if d.Category != family.ID || len(d.Tracks) != len(preset.Tracks) {
				return fmt.Errorf("motion: entity presentation preset %q does not match canonical motion definition", preset.ID)
			}
			for i, track := range preset.Tracks {
				if err := validateTrackDefinition(preset.ID, "entity presentation track", track); err != nil {
					return err
				}
				actual := d.Tracks[i]
				if actual.Property != track.Property || len(actual.Keyframes) != len(track.Keyframes) {
					return fmt.Errorf("motion: entity presentation preset %q differs from emitted motion tracks", preset.ID)
				}
			}
		}
	}
	return nil
}

func validateEntityPresentationMotion(d MotionDefinition) error {
	_, ok := expectedPresentationFamilies[d.Category]
	if !ok {
		return nil
	}
	if d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames || d.RenderSafe == nil || !*d.RenderSafe || d.SupportedTemplate == "" || len(d.SupportedContent) == 0 || len(d.RequiredProperties) == 0 || len(d.Tracks) == 0 {
		return fmt.Errorf("motion %q: incomplete %s certification metadata", d.ID, d.Category)
	}
	if len(d.RequiredProperties) != len(d.Tracks) {
		return fmt.Errorf("motion %q: required_properties must list every presentation track", d.ID)
	}
	for i, track := range d.Tracks {
		if d.RequiredProperties[i] != track.Property {
			return fmt.Errorf("motion %q: required_properties differ from track order", d.ID)
		}
		if err := validateTrackDefinition(d.ID, "presentation track", track); err != nil {
			return err
		}
		last := track.Keyframes[len(track.Keyframes)-1]
		value, ok := motionNumericValue(last.Value)
		if !ok || !finiteMotionValue(value) {
			return fmt.Errorf("motion %q: %s has a non-finite final pose", d.ID, track.Property)
		}
		switch track.Property {
		case "opacity", "scale", "scale_x", "scale_y":
			if value != 1 {
				return fmt.Errorf("motion %q: %s must finish at 1", d.ID, track.Property)
			}
		case "position_x", "position_y", "position_z", "rotation_x", "rotation_y":
			if value != 0 {
				return fmt.Errorf("motion %q: %s must finish at neutral 0", d.ID, track.Property)
			}
		}
	}
	return nil
}

func (r *RegistryType) PresentationFamilyIDs() []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, 3)
	for _, f := range catalog.EntityPresentationFamilies() {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	return ids
}
func (r *RegistryType) PresentationMotionIDs(familyID string) []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	for _, f := range catalog.EntityPresentationFamilies() {
		if f.ID == familyID {
			ids := make([]string, 0, len(f.Presets))
			for _, p := range f.Presets {
				ids = append(ids, p.ID)
			}
			return ids
		}
	}
	return nil
}
func (c Catalog) EntityPresentationFamilies() []PresentationFamily {
	var parsed PresentationCatalog
	if err := json.Unmarshal(c.EntityPresentation, &parsed); err != nil {
		return nil
	}
	return append([]PresentationFamily(nil), parsed.Families...)
}
