package dateanim

import (
	"encoding/json"
	"fmt"
	"strings"
)

// materialize_validate.go owns the post-materialization checks: the invariants
// every produced plan must satisfy before it leaves the package.

func validateMaterializedPlan(raw []byte, styleID string, dates []string, duration int) error {
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("dateanim: validate plan %q: %w", styleID, err)
	}
	canvas, _ := plan["canvas"].(map[string]any)
	if got := int(asFloat(canvas["duration_frames"])); got != duration {
		return fmt.Errorf("dateanim: plan %q duration = %d, want %d", styleID, got, duration)
	}
	layers, _ := plan["layers"].([]any)
	if len(layers) == 0 {
		return fmt.Errorf("dateanim: plan %q has no layers", styleID)
	}
	found := make(map[string]bool, len(dates))
	for _, rawLayer := range layers {
		layer, _ := rawLayer.(map[string]any)
		if strings.EqualFold(fmt.Sprint(layer["type"]), "text") {
			text, _ := layer["text"].(string)
			for _, date := range dates {
				if text == date {
					found[date] = true
				}
			}
		}
		if got := int(asFloat(layer["duration_frames"])); got != duration {
			return fmt.Errorf("dateanim: plan %q layer %v duration = %d, want %d", styleID, layer["id"], got, duration)
		}
		for _, rawTrack := range tracksOf(layer) {
			track, _ := rawTrack.(map[string]any)
			keyframes, _ := track["keyframes"].([]any)
			if len(keyframes) == 0 {
				return fmt.Errorf("dateanim: plan %q layer %v has an empty track", styleID, layer["id"])
			}
			for _, rawKF := range keyframes {
				kf, _ := rawKF.(map[string]any)
				if asFloat(kf["frame"]) >= float64(duration) {
					return fmt.Errorf("dateanim: plan %q layer %v has a keyframe after the clip", styleID, layer["id"])
				}
			}
		}
	}
	// The decomposed styles (year morph, calendar flip, split date, odometer)
	// intentionally never carry a full date string; the other styles must.
	switch styleID {
	case "02_year_morph", "05_calendar_flip", "07_split_date", "12_rolling_year":
	default:
		for _, date := range dates {
			if !found[date] {
				return fmt.Errorf("dateanim: plan %q does not contain date %q", styleID, date)
			}
		}
	}
	return nil
}
