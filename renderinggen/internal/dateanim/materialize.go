package dateanim

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The plans are the ChrononTemplate golden plans, embedded so the selector and
// the renderer travel together. They contain the approved four-date timing and
// typography; Materialize rewrites only the date content and the visible slot
// count, never the visual vocabulary.
//
//go:embed plans/*.json
var embeddedPlans embed.FS

var (
	// ErrTooManyDates is returned when a text names more dates than the
	// approved four-slot animations can show without dropping one.
	ErrTooManyDates = errors.New("dateanim: the catalog styles support at most four dates")
	// ErrSingleDatePlan is returned by the materializer for one date: that
	// editorial case uses the ordinary timeline_date overlay, not a sequence.
	ErrSingleDatePlan = errors.New("dateanim: a date animation needs at least two dates")
	// ErrRollingYearRange is returned when the odometer style cannot represent
	// the century sequence. Materialize falls back to the timeline style rather
	// than letting a stable random choice fail the render.
	ErrRollingYearRange = errors.New("dateanim: rolling year cannot represent this century sequence")
)

const maxDates = 4

// Materialized is a selected style plus the concrete render plan for the real
// dates. Plan is the chronon.render-plan.v3 document; Dates keeps the extracted
// sequence so a caller can stamp it into a manifest without re-parsing.
type Materialized struct {
	Style Style           `json:"style"`
	Dates []string        `json:"dates"`
	Plan  json.RawMessage `json:"-"`
}

// MaterializeFromText is the editorial entry point for the renderable path: it
// extracts the years, picks the stable style, and rewrites that style's plan
// with those years.
func MaterializeFromText(planID, videoID, groupID, text string) (Materialized, error) {
	dates := DatesFromText(text)
	if len(dates) == 0 {
		return Materialized{}, ErrNoDates
	}
	return Materialize(planID, videoID, groupID, dates)
}

// Materialize selects the style for a date sequence and returns the concrete
// plan. The style choice is the same stable choice Select exposes; the plan is
// a copy of the approved template with the sample dates replaced by dates.
func Materialize(planID, videoID, groupID string, dates []string) (Materialized, error) {
	style, err := Select(planID, videoID, groupID, dates)
	if err != nil {
		return Materialized{}, err
	}
	materialized, err := MaterializeStyle(style, dates)
	if errors.Is(err, ErrRollingYearRange) {
		if fallback, ok := StyleByID("01_timeline_progress"); ok {
			return MaterializeStyle(fallback, dates)
		}
	}
	return materialized, err
}

// MaterializeStyle rewrites one style for the supplied dates. Two to four
// dates are supported. The plans are built around four timeline slots; unused
// slots are removed and the final visible date holds to the end of the clip.
func MaterializeStyle(style Style, dates []string) (Materialized, error) {
	normalized, err := normalizePlanDates(dates)
	if err != nil {
		return Materialized{}, err
	}
	if len(normalized) > maxDates {
		return Materialized{}, ErrTooManyDates
	}
	if len(normalized) < 2 {
		return Materialized{}, ErrSingleDatePlan
	}
	raw, err := embeddedPlans.ReadFile("plans/" + style.ID + ".plan.json")
	if err != nil {
		return Materialized{}, fmt.Errorf("dateanim: no plan for style %q: %w", style.ID, err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return Materialized{}, fmt.Errorf("dateanim: decode plan %q: %w", style.ID, err)
	}
	duration := planDuration(style.ID, len(normalized))
	plan["job_id"] = planJobID(style.ID)
	if output, ok := plan["output"].(map[string]any); ok {
		output["path"] = style.ID + ".mp4"
		output["format"] = "mp4"
		output["codec"] = "h264"
	}
	canvas, _ := plan["canvas"].(map[string]any)
	if canvas == nil {
		return Materialized{}, fmt.Errorf("dateanim: plan %q has no canvas", style.ID)
	}
	canvas["duration_frames"] = float64(duration)
	canvas["fps_num"] = float64(style.FPS)
	canvas["fps_den"] = float64(1)

	switch style.ID {
	case "02_year_morph":
		if err := rebuildYearMorph(plan, normalized, duration); err != nil {
			return Materialized{}, err
		}
	case "12_rolling_year":
		if err := rebuildRollingYear(plan, normalized, duration); err != nil {
			return Materialized{}, err
		}
	default:
		if err := rewriteTimelinePlan(plan, style.ID, normalized, duration); err != nil {
			return Materialized{}, err
		}
	}
	if err := finishPlanLayers(plan, style.ID, normalized, duration); err != nil {
		return Materialized{}, err
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return Materialized{}, fmt.Errorf("dateanim: encode plan %q: %w", style.ID, err)
	}
	encoded = append(encoded, '\n')
	if err := validateMaterializedPlan(encoded, style.ID, normalized, duration); err != nil {
		return Materialized{}, err
	}
	return Materialized{Style: style, Dates: normalized, Plan: encoded}, nil
}

func normalizePlanDates(dates []string) ([]string, error) {
	if len(dates) == 0 {
		return nil, ErrNoDates
	}
	out := make([]string, len(dates))
	seen := make(map[string]struct{}, len(dates))
	for i, date := range dates {
		date = strings.TrimSpace(date)
		if len(date) != 4 {
			return nil, fmt.Errorf("dateanim: date %q is not a four-digit year", date)
		}
		for _, r := range date {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("dateanim: date %q is not a four-digit year", date)
			}
		}
		if _, ok := seen[date]; ok {
			return nil, fmt.Errorf("dateanim: duplicate date %q", date)
		}
		seen[date] = struct{}{}
		out[i] = date
	}
	return out, nil
}

func planDuration(styleID string, dates int) int {
	if styleID == "12_rolling_year" {
		switch dates {
		case 2:
			return 140
		case 3:
			return 220
		default:
			return 320
		}
	}
	switch dates {
	case 2:
		return 120
	case 3:
		return 192
	default:
		return 240
	}
}

func planJobID(styleID string) string {
	return "date_animation_" + styleID
}

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

func textLayer(id, text string, x, y, w, h float64, style map[string]any, duration int, tracks []any) map[string]any {
	layer := map[string]any{
		"id":              id,
		"type":            "text",
		"text":            text,
		"size":            []any{w, h},
		"position":        []any{x, y},
		"start_frame":     float64(0),
		"duration_frames": float64(duration),
		"style":           cloneMap(style),
	}
	if len(tracks) > 0 {
		layer["animation"] = map[string]any{"tracks": tracks}
	}
	return layer
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	}
	return 0
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

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

// PlanBytes returns a copy of the concrete plan, useful to callers that hand
// the plan to a renderer without holding the Materialized value.
func (m Materialized) PlanBytes() []byte {
	return bytes.Clone(m.Plan)
}
