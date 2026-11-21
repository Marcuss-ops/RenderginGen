package dateanim

import (
	"fmt"
	"strconv"
	"strings"
)

// materialize_timeline.go owns the slot rewrite: where each date layer sits on
// the plan timeline, the per-layer retiming, the stack marker and the layer
// finishing pass. Materialize itself (and the entry points) stay in
// materialize.go.

// timelineSlots is the approved four-slot schedule shared by the 24 fps
// styles: slot i enters at enter[i] and holds until hold[i]. Keeping the same
// schedule means a two-date clip is literally the first two acts of the
// approved four-date animation, not a new animation.
var timelineSlots = []struct {
	enter int
	hold  int
}{
	{0, 48},
	{60, 120},
	{132, 192},
	{204, 240},
}

func rewriteTimelinePlan(plan map[string]any, styleID string, dates []string, duration int) error {
	layers, ok := plan["layers"].([]any)
	if !ok {
		return fmt.Errorf("dateanim: plan %q has no layers", styleID)
	}
	out := make([]any, 0, len(layers))
	for _, raw := range layers {
		layer, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("dateanim: plan %q has a malformed layer", styleID)
		}
		id, _ := layer["id"].(string)
		keep, text := rewriteTimelineLayer(styleID, id, dates)
		if !keep {
			continue
		}
		if text != "" {
			layer["text"] = text
		}
		if id == "stack_marker" && (len(dates) == 2 || len(dates) == 3) {
			adjustStackMarker(layer, len(dates), duration)
		}
		out = append(out, layer)
	}
	plan["layers"] = out
	return nil
}

// rewriteTimelineLayer reports whether a layer belongs to a visible slot and,
// for text layers, the replacement text. Unknown layers are always kept.
func rewriteTimelineLayer(styleID, id string, dates []string) (bool, string) {
	if i, ok := indexedLayer(id, "date_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "axis_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "v_axis_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "focus_date_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "stack_row_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "stack_active_"); ok {
		return i < len(dates), dateAt(dates, i)
	}
	if i, ok := indexedLayer(id, "prev_"); ok {
		if i >= len(dates) || i == 0 {
			return false, ""
		}
		return true, dateAt(dates, i-1)
	}
	if i, ok := indexedLayer(id, "next_"); ok {
		if i >= len(dates)-1 {
			return false, ""
		}
		return true, dateAt(dates, i+1)
	}
	if i, ok := indexedLayer(id, "split_p_"); ok {
		if i >= len(dates) {
			return false, ""
		}
		return true, dates[i][:2]
	}
	if i, ok := indexedLayer(id, "split_s_"); ok {
		if i >= len(dates) {
			return false, ""
		}
		return true, dates[i][2:]
	}
	if i, j, ok := calendarDigitLayer(id); ok {
		if i >= len(dates) {
			return false, ""
		}
		return true, string(dates[i][j])
	}
	if strings.HasPrefix(id, "d") && styleID == "02_year_morph" {
		// Rebuilt from scratch; the sample digit layers must not survive.
		return false, ""
	}
	return true, ""
}

func indexedLayer(id, prefix string) (int, bool) {
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil {
		return 0, false
	}
	return index, true
}

func calendarDigitLayer(id string) (int, int, bool) {
	parts := strings.Split(id, "_")
	if len(parts) != 3 || parts[0] != "cal" {
		return 0, 0, false
	}
	slot, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	digit, err := strconv.Atoi(parts[2])
	if err != nil || digit < 0 || digit > 3 {
		return 0, 0, false
	}
	return slot, digit, true
}

func dateAt(dates []string, i int) string {
	if i < 0 || i >= len(dates) {
		return ""
	}
	return dates[i]
}

func adjustStackMarker(layer map[string]any, dates, duration int) {
	var targetFrame, targetValue float64
	switch dates {
	case 2:
		targetFrame, targetValue = 132, 120
	case 3:
		targetFrame, targetValue = 192, 240
	default:
		return
	}
	for _, rawTrack := range tracksOf(layer) {
		track, ok := rawTrack.(map[string]any)
		if !ok || track["property"] != "position_y" {
			continue
		}
		keyframes, _ := track["keyframes"].([]any)
		for _, raw := range keyframes {
			kf, ok := raw.(map[string]any)
			if !ok || asFloat(kf["frame"]) != targetFrame {
				continue
			}
			kf["frame"] = float64(duration - 1)
			kf["value"] = targetValue
		}
	}
}

func finishPlanLayers(plan map[string]any, styleID string, dates []string, duration int) error {
	layers, ok := plan["layers"].([]any)
	if !ok {
		return fmt.Errorf("dateanim: plan %q has no layers after rewrite", styleID)
	}
	for _, raw := range layers {
		layer, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("dateanim: plan %q has a malformed layer", styleID)
		}
		layer["start_frame"] = float64(0)
		layer["duration_frames"] = float64(duration)
		filterLayerTracks(layer, duration)
	}
	return nil
}

func filterLayerTracks(layer map[string]any, duration int) {
	tracks := tracksOf(layer)
	if len(tracks) == 0 {
		return
	}
	for _, raw := range tracks {
		track, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		keyframes, _ := track["keyframes"].([]any)
		kept := make([]any, 0, len(keyframes))
		for _, rawKF := range keyframes {
			kf, ok := rawKF.(map[string]any)
			if !ok || asFloat(kf["frame"]) >= float64(duration) {
				continue
			}
			kept = append(kept, kf)
		}
		if len(kept) == 0 && len(keyframes) > 0 {
			kf, ok := keyframes[0].(map[string]any)
			if ok {
				kf["frame"] = float64(0)
				kept = append(kept, kf)
			}
		}
		track["keyframes"] = kept
	}
}

func tracksOf(layer map[string]any) []any {
	animation, ok := layer["animation"].(map[string]any)
	if !ok {
		return nil
	}
	tracks, _ := animation["tracks"].([]any)
	return tracks
}
