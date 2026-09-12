package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// UnmarshalJSON is the wire boundary for one semantic item. PipelineGen owns
// entity selection/editorial data; RenderingGen only validates and lowers it.
// Keep wire-only identity/timing fields out of the render model while still
// requiring them where the contract says they are authoritative.
func (i *semanticItem) UnmarshalJSON(data []byte) error {
	type alias semanticItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var wire struct {
		EntityID   string `json:"entity_id"`
		DurationMS *int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*i = semanticItem(decoded)

	if wire.DurationMS != nil {
		if *wire.DurationMS <= 0 {
			return fmt.Errorf("overlay: item %q duration_ms must be positive", i.ID)
		}
		if want := i.EndMS - i.StartMS; *wire.DurationMS != want {
			return fmt.Errorf("overlay: item %q duration_ms %d does not match end_ms-start_ms %d", i.ID, *wire.DurationMS, want)
		}
	}

	spec := templateSpecFor(i.Template)
	kind, err := spec.resolveKind(i.Kind, i.ID)
	if err != nil {
		return err
	}
	if !isEntityKind(kind) {
		return nil
	}

	if strings.TrimSpace(wire.EntityID) == "" {
		return fmt.Errorf("overlay: entity item %q requires entity_id from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.Kind) == "" {
		return fmt.Errorf("overlay: entity item %q requires kind from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.PresetID) == "" {
		return fmt.Errorf("overlay: entity item %q requires preset_id from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.Text) == "" {
		return fmt.Errorf("overlay: entity item %q requires text from PipelineGen", i.ID)
	}
	if wire.DurationMS == nil {
		return fmt.Errorf("overlay: entity item %q requires duration_ms from PipelineGen", i.ID)
	}
	if len(i.Assets) > 0 && strings.TrimSpace(i.ImagePresetID) == "" {
		return fmt.Errorf("overlay: entity item %q with image requires image_preset_id from PipelineGen", i.ID)
	}
	return nil
}
