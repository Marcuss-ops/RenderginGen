package dateanim

import (
	"fmt"
	"sort"
	"strings"
)

// materialize_year.go owns the rolling-year and year-morph animation: the
// keyframes, the per-prefix tracks and the rebuild passes that turn a year
// range into one animated morph instead of N static layers.

func rebuildYearMorph(plan map[string]any, dates []string, duration int) error {
	layers, _ := plan["layers"].([]any)
	kept := make([]any, 0, len(layers))
	var base map[string]any
	for _, raw := range layers {
		layer, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("dateanim: year morph has a malformed layer")
		}
		id, _ := layer["id"].(string)
		if id == "bg" {
			kept = append(kept, layer)
			continue
		}
		if base == nil && strings.HasPrefix(id, "d") {
			base = layer
			continue
		}
		if strings.HasPrefix(id, "d") {
			continue
		}
		kept = append(kept, layer)
	}
	if base == nil {
		return fmt.Errorf("dateanim: year morph has no digit template")
	}
	style, ok := base["style"].(map[string]any)
	if !ok {
		return fmt.Errorf("dateanim: year morph digit has no style")
	}
	style = cloneMap(style)
	xs := []float64{780, 900, 1020, 1140}
	for digit := 0; digit < 4; digit++ {
		for slot := 0; slot < len(dates); slot++ {
			layer := map[string]any{
				"id":              fmt.Sprintf("d%d_%d", digit+1, slot),
				"type":            "text",
				"text":            string(dates[slot][digit]),
				"size":            []any{float64(180), float64(300)},
				"position":        []any{xs[digit], float64(470)},
				"start_frame":     float64(0),
				"duration_frames": float64(duration),
				"style":           cloneMap(style),
				"animation":       map[string]any{"tracks": []any{opacityTrack(slot, len(dates), duration)}},
			}
			kept = append(kept, layer)
		}
	}
	plan["layers"] = kept
	return nil
}

func opacityTrack(slot, total, duration int) map[string]any {
	return map[string]any{
		"property":  "opacity",
		"keyframes": opacityKeyframes(slot, total, duration),
		"easing":    "in_out_cubic",
	}
}

func opacityKeyframes(slot, total, duration int) []any {
	enter := timelineSlots[slot].enter
	if slot == 0 {
		if total == 1 {
			return kfs(0, 1, duration-1, 1)
		}
		return kfs(0, 1, 48, 1, 60, 0, duration-1, 0)
	}
	if slot == total-1 {
		return kfs(0, 0, enter-12, 0, enter, 1, duration-1, 1)
	}
	hold := timelineSlots[slot].hold
	return kfs(0, 0, enter-12, 0, enter, 1, hold, 1, hold+12, 0, duration-1, 0)
}

func kfs(values ...int) []any {
	out := make([]any, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		out = append(out, map[string]any{"frame": float64(values[i]), "value": float64(values[i+1])})
	}
	return out
}

// rebuildRollingYear rebuilds the odometer plan around the actual dates. The
// century wheel only has two positions, so a sequence that crosses more than
// one century boundary (or goes backwards) is rejected instead of showing the
// wrong prefix.
func rebuildRollingYear(plan map[string]any, dates []string, duration int) error {
	layers, _ := plan["layers"].([]any)
	var bg, coverTop, coverBottom, rail, underline, suffixTemplate, axisTemplate map[string]any
	for _, raw := range layers {
		layer, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("dateanim: rolling year has a malformed layer")
		}
		switch layer["id"] {
		case "bg":
			bg = layer
		case "slot_cover_top":
			coverTop = layer
		case "slot_cover_bottom":
			coverBottom = layer
		case "rail":
			rail = layer
		case "active_underline":
			underline = layer
		case "suffix_0":
			suffixTemplate = layer
		case "axis_0":
			axisTemplate = layer
		}
	}
	if bg == nil || coverTop == nil || coverBottom == nil || rail == nil || underline == nil ||
		suffixTemplate == nil || axisTemplate == nil {
		return fmt.Errorf("dateanim: rolling year plan is missing a required layer")
	}
	fontStyle, ok := suffixTemplate["style"].(map[string]any)
	if !ok {
		return fmt.Errorf("dateanim: rolling year suffix has no style")
	}
	axisStyle, ok := axisTemplate["style"].(map[string]any)
	if !ok {
		return fmt.Errorf("dateanim: rolling year axis has no style")
	}
	prefixTexts := uniquePrefixes(dates)
	if len(prefixTexts) > 2 {
		return fmt.Errorf("%w: at most two centuries, got %v", ErrRollingYearRange, prefixTexts)
	}
	changeAt := -1
	for i := 1; i < len(dates); i++ {
		if dates[i][:2] != dates[i-1][:2] {
			if changeAt >= 0 {
				return fmt.Errorf("%w: century changes more than once (%v)", ErrRollingYearRange, dates)
			}
			changeAt = i
		}
	}
	rollStarts := []int{0, 60, 140, 220}
	prefixRoll := -1
	if len(prefixTexts) == 2 {
		prefixRoll = rollStarts[changeAt]
	}
	prefixLayers := []any{
		textLayer("prefix_0", prefixTexts[0], 850, 470, 600, 300, fontStyle, duration,
			prefixTrack(prefixRoll, duration)),
	}
	if len(prefixTexts) == 2 {
		prefixLayers = append(prefixLayers,
			textLayer("prefix_1", prefixTexts[1], 850, 850, 600, 300, fontStyle, duration,
				prefixTrack(prefixRoll, duration)))
	}
	var suffixLayers []any
	for i, date := range dates {
		suffixLayers = append(suffixLayers, textLayer(
			fmt.Sprintf("suffix_%d", i), date[2:], 1070, float64(470+i*380), 600, 300,
			fontStyle, duration, []any{rollingTrack("position_y", len(dates), duration, 0, -380, rollStarts)},
		))
	}
	underlineTrack := rollingTrack("position_x", len(dates), duration, 0, 280, rollStarts)
	underline = cloneMap(underline)
	underline["duration_frames"] = float64(duration)
	underline["start_frame"] = float64(0)
	underline["animation"] = map[string]any{"tracks": []any{underlineTrack}}
	var axisLayers []any
	railXs := []float64{540, 820, 1100, 1380}
	for i, date := range dates {
		axisLayers = append(axisLayers, textLayer(
			fmt.Sprintf("axis_%d", i), date, railXs[i], 886, 240, 40, axisStyle, duration, nil,
		))
	}
	bg = cloneMap(bg)
	bg["duration_frames"] = float64(duration)
	coverTop = cloneMap(coverTop)
	coverTop["duration_frames"] = float64(duration)
	coverBottom = cloneMap(coverBottom)
	coverBottom["duration_frames"] = float64(duration)
	rail = cloneMap(rail)
	rail["duration_frames"] = float64(duration)

	rebuilt := []any{bg}
	rebuilt = append(rebuilt, prefixLayers...)
	rebuilt = append(rebuilt, suffixLayers...)
	rebuilt = append(rebuilt, coverTop, coverBottom, rail)
	rebuilt = append(rebuilt, axisLayers...)
	rebuilt = append(rebuilt, underline)
	plan["layers"] = rebuilt
	return nil
}

func uniquePrefixes(dates []string) []string {
	seen := make(map[string]bool, len(dates))
	var out []string
	for _, date := range dates {
		prefix := date[:2]
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	sort.Strings(out)
	return out
}

func prefixTrack(start, duration int) []any {
	if start < 0 {
		return nil
	}
	return []any{map[string]any{
		"property": "position_y",
		"keyframes": kfs(
			0, 0,
			start, 0,
			start+28, -380,
			duration-1, -380,
		),
		"easing": "in_out_cubic",
	}}
}

func rollingTrack(property string, total, duration, base, step int, starts []int) map[string]any {
	frames := []int{0}
	values := []int{base}
	for slot := 1; slot < total; slot++ {
		frames = append(frames, starts[slot], starts[slot]+28)
		values = append(values, base+(slot-1)*step, base+slot*step)
	}
	frames = append(frames, duration-1)
	values = append(values, base+(total-1)*step)
	pairs := make([]int, 0, len(frames)*2)
	for i := range frames {
		pairs = append(pairs, frames[i], values[i])
	}
	return map[string]any{
		"property":  property,
		"keyframes": kfs(pairs...),
		"easing":    "in_out_cubic",
	}
}
