package dateanim

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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

// PlanBytes returns a copy of the concrete plan, useful to callers that hand
// the plan to a renderer without holding the Materialized value.
func (m Materialized) PlanBytes() []byte {
	return bytes.Clone(m.Plan)
}
