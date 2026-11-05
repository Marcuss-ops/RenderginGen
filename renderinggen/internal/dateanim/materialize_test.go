package dateanim

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMaterializeEveryStyleUsesTheRealDates(t *testing.T) {
	dates := []string{"2007", "2013", "2017", "2024"}
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range catalog.Styles {
		t.Run(style.ID, func(t *testing.T) {
			materialized, err := MaterializeStyle(style, dates)
			if err != nil {
				t.Fatalf("materialize: %v", err)
			}
			if !reflect.DeepEqual(materialized.Dates, dates) {
				t.Fatalf("dates = %v, want %v", materialized.Dates, dates)
			}
			plan := decodePlan(t, materialized.Plan)
			canvas := plan["canvas"].(map[string]any)
			if got := int(asFloat(canvas["duration_frames"])); got != planDuration(style.ID, 4) {
				t.Fatalf("duration = %d, want %d", got, planDuration(style.ID, 4))
			}
			raw := string(materialized.Plan)
			for _, sample := range []string{"1948", "1969", "1998", "2026"} {
				if strings.Contains(raw, `"`+sample+`"`) {
					t.Fatalf("plan still contains sample date %q", sample)
				}
			}
			switch style.ID {
			case "02_year_morph", "05_calendar_flip", "07_split_date", "12_rolling_year":
				// These styles decompose the year into digits, prefix/suffix or
				// odometer wheels by design; their date text is checked above.
			default:
				if !materializedHasAnyText(plan, dates) {
					t.Fatalf("plan contains no real date text: %s", raw)
				}
			}
		})
	}
}

func TestMaterializeTrimsTheTimelineToTheDateCount(t *testing.T) {
	style, ok := StyleByID("01_timeline_progress")
	if !ok {
		t.Fatal("style missing")
	}
	cases := []struct {
		dates    []string
		duration int
		keep     []string
		drop     []string
	}{
		{[]string{"2007", "2024"}, 120, []string{"date_1", "axis_1"}, []string{"date_2", "date_3", "axis_2", "axis_3"}},
		{[]string{"2007", "2013", "2024"}, 192, []string{"date_2", "axis_2"}, []string{"date_3", "axis_3"}},
		{[]string{"2007", "2013", "2017", "2024"}, 240, []string{"date_3", "axis_3"}, nil},
	}
	for _, tc := range cases {
		materialized, err := MaterializeStyle(style, tc.dates)
		if err != nil {
			t.Fatalf("%v: %v", tc.dates, err)
		}
		plan := decodePlan(t, materialized.Plan)
		if got := int(asFloat(plan["canvas"].(map[string]any)["duration_frames"])); got != tc.duration {
			t.Fatalf("%d dates: duration = %d, want %d", len(tc.dates), got, tc.duration)
		}
		ids := layerIDs(plan)
		for _, id := range tc.keep {
			if !ids[id] {
				t.Fatalf("%d dates: missing layer %q", len(tc.dates), id)
			}
		}
		for _, id := range tc.drop {
			if ids[id] {
				t.Fatalf("%d dates: unused layer %q survived", len(tc.dates), id)
			}
		}
	}
}

func TestMaterializeRollingYearRollsTheCenturyOnlyWhenNeeded(t *testing.T) {
	style, ok := StyleByID("12_rolling_year")
	if !ok {
		t.Fatal("style missing")
	}
	crossing, err := MaterializeStyle(style, []string{"1998", "2007", "2013", "2024"})
	if err != nil {
		t.Fatal(err)
	}
	plan := decodePlan(t, crossing.Plan)
	ids := layerIDs(plan)
	if !ids["prefix_0"] || !ids["prefix_1"] {
		t.Fatalf("century crossing must keep both prefix wheels: %v", ids)
	}
	if textOf(plan, "prefix_0") != "19" || textOf(plan, "prefix_1") != "20" {
		t.Fatalf("prefix texts = %q/%q", textOf(plan, "prefix_0"), textOf(plan, "prefix_1"))
	}
	if textOf(plan, "suffix_0") != "98" || textOf(plan, "suffix_3") != "24" {
		t.Fatalf("suffix texts = %q..%q", textOf(plan, "suffix_0"), textOf(plan, "suffix_3"))
	}
	if !hasKeyframe(plan, "prefix_0", "position_y", 88, -380) {
		t.Fatal("prefix wheel must roll when the century changes between 1998 and 2007")
	}
	if !hasKeyframe(plan, "suffix_0", "position_y", 88, -380) {
		t.Fatal("suffix wheels must roll upward so the next year enters the window")
	}

	sameCentury, err := MaterializeStyle(style, []string{"2007", "2013", "2017", "2024"})
	if err != nil {
		t.Fatal(err)
	}
	samePlan := decodePlan(t, sameCentury.Plan)
	if layerIDs(samePlan)["prefix_1"] {
		t.Fatal("a single century must not keep a second prefix wheel")
	}
	if textOf(samePlan, "prefix_0") != "20" {
		t.Fatalf("prefix = %q, want 20", textOf(samePlan, "prefix_0"))
	}
}

func TestMaterializeStackMarkerEndsOnTheLastVisibleRow(t *testing.T) {
	style, ok := StyleByID("06_date_stack")
	if !ok {
		t.Fatal("style missing")
	}
	cases := []struct {
		dates []string
		value float64
	}{
		{[]string{"1998", "2007"}, 120},
		{[]string{"1998", "2007", "2013"}, 240},
	}
	for _, tc := range cases {
		materialized, err := MaterializeStyle(style, tc.dates)
		if err != nil {
			t.Fatal(err)
		}
		plan := decodePlan(t, materialized.Plan)
		duration := planDuration(style.ID, len(tc.dates))
		if !hasKeyframe(plan, "stack_marker", "position_y", duration-1, tc.value) {
			t.Fatalf("%d dates: marker does not land on the last visible row", len(tc.dates))
		}
	}
}

func TestMaterializeFallsBackWhenRollingYearCannotRepresentCenturies(t *testing.T) {
	dates := []string{"1899", "1948", "2007", "2024"}
	rolling, ok := StyleByID("12_rolling_year")
	if !ok {
		t.Fatal("style 12 missing")
	}
	if _, err := MaterializeStyle(rolling, dates); !errors.Is(err, ErrRollingYearRange) {
		t.Fatalf("style 12 error = %v, want ErrRollingYearRange", err)
	}
	for i := 0; i < 200; i++ {
		planID := fmt.Sprintf("fallback-%d", i)
		selected, err := Select(planID, "video", "dates", dates)
		if err != nil {
			t.Fatal(err)
		}
		if selected.ID != "12_rolling_year" {
			continue
		}
		materialized, err := Materialize(planID, "video", "dates", dates)
		if err != nil {
			t.Fatal(err)
		}
		if materialized.Style.ID != "01_timeline_progress" {
			t.Fatalf("style = %q, want 01 fallback", materialized.Style.ID)
		}
		plan := decodePlan(t, materialized.Plan)
		if !materializedHasAnyText(plan, dates) {
			t.Fatal("fallback plan lost the real dates")
		}
		return
	}
	t.Fatal("no seed selected style 12 for the fallback test")
}

func TestMaterializeFromTextUsesTheExtractedDates(t *testing.T) {
	text := "Nel 2007 parte; nel 2013 cresce; nel 2017 cambia; nel 2024 si chiude."
	materialized, err := MaterializeFromText("plan-a", "video-a", "dates", text)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2007", "2013", "2017", "2024"}
	if !reflect.DeepEqual(materialized.Dates, want) {
		t.Fatalf("dates = %v, want %v", materialized.Dates, want)
	}
	if _, ok := StyleByID(materialized.Style.ID); !ok {
		t.Fatalf("selected style %q is outside the catalog", materialized.Style.ID)
	}
	if !json.Valid(materialized.Plan) {
		t.Fatal("materialized plan is not valid JSON")
	}
}

func TestMaterializeRejectsUnsupportedSequences(t *testing.T) {
	style, _ := StyleByID("01_timeline_progress")
	if _, err := MaterializeStyle(style, []string{"2007"}); err != ErrSingleDatePlan {
		t.Fatalf("single date error = %v, want ErrSingleDatePlan", err)
	}
	if _, err := MaterializeStyle(style, []string{"2007", "2013", "2017", "2024", "2026"}); err != ErrTooManyDates {
		t.Fatalf("five dates error = %v, want ErrTooManyDates", err)
	}
	if _, err := MaterializeStyle(style, []string{"2007", "2007"}); err == nil {
		t.Fatal("duplicate dates must fail")
	}
	if _, err := MaterializeStyle(style, []string{"2007", "13"}); err == nil {
		t.Fatal("non-four-digit dates must fail")
	}
}

func decodePlan(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return plan
}

func layerIDs(plan map[string]any) map[string]bool {
	ids := make(map[string]bool)
	layers, _ := plan["layers"].([]any)
	for _, raw := range layers {
		layer, _ := raw.(map[string]any)
		id, _ := layer["id"].(string)
		ids[id] = true
	}
	return ids
}

func textOf(plan map[string]any, id string) string {
	layers, _ := plan["layers"].([]any)
	for _, raw := range layers {
		layer, _ := raw.(map[string]any)
		if layer["id"] == id {
			text, _ := layer["text"].(string)
			return text
		}
	}
	return ""
}

func materializedHasAnyText(plan map[string]any, dates []string) bool {
	layers, _ := plan["layers"].([]any)
	for _, raw := range layers {
		layer, _ := raw.(map[string]any)
		text, _ := layer["text"].(string)
		for _, date := range dates {
			if text == date {
				return true
			}
		}
	}
	return false
}

func hasKeyframe(plan map[string]any, layerID, property string, frame int, value float64) bool {
	layers, _ := plan["layers"].([]any)
	for _, raw := range layers {
		layer, _ := raw.(map[string]any)
		if layer["id"] != layerID {
			continue
		}
		for _, rawTrack := range tracksOf(layer) {
			track, _ := rawTrack.(map[string]any)
			if track["property"] != property {
				continue
			}
			keyframes, _ := track["keyframes"].([]any)
			for _, rawKF := range keyframes {
				kf, _ := rawKF.(map[string]any)
				if int(asFloat(kf["frame"])) == frame && asFloat(kf["value"]) == value {
					return true
				}
			}
		}
	}
	return false
}
