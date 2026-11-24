package overlay

import (
	"strings"
	"testing"
)

func gradientTupleStopsForTest() []any {
	return []any{
		[]any{"#000000", 0.0},
		[]any{"#FFFFFF", 1.0},
	}
}

func TestParseGradientDefaultsAndBothStopDialects(t *testing.T) {
	tuple, err := parseGradient(map[string]any{"stops": gradientTupleStopsForTest()}, "tuple")
	if err != nil {
		t.Fatalf("tuple stops: %v", err)
	}
	if tuple.Type != "linear" {
		t.Fatalf("default type = %q, want linear", tuple.Type)
	}
	if len(tuple.Start) != 2 || tuple.Start[0] != 0 || tuple.Start[1] != 0 {
		t.Fatalf("default start = %v, want [0 0]", tuple.Start)
	}
	if len(tuple.End) != 2 || tuple.End[0] != 0 || tuple.End[1] != 1 {
		t.Fatalf("default end = %v, want [0 1]", tuple.End)
	}
	if len(tuple.ColorStops) != 2 || len(tuple.Stops) != 2 {
		t.Fatalf("tuple stops lowered to %d color stops and %d raw stops", len(tuple.ColorStops), len(tuple.Stops))
	}
	if tuple.Stops[0][0] != "#000000" || tuple.Stops[1][0] != "#FFFFFF" {
		t.Fatalf("raw stops lost their authored colors: %v", tuple.Stops)
	}

	object, err := parseGradient(map[string]any{
		"type": "radial",
		"color_stops": []any{
			map[string]any{"color": "#000000", "position": 0.0},
			map[string]any{"color": "#FFFFFF", "position": 1.0},
		},
		"opacity_stops": []any{
			map[string]any{"position": 0.0, "opacity": 0.25},
			map[string]any{"position": 1.0, "opacity": 0.75},
		},
	}, "object")
	if err != nil {
		t.Fatalf("object stops: %v", err)
	}
	if object.Type != "radial" {
		t.Fatalf("type = %q, want radial", object.Type)
	}
	if len(object.ColorStops) != 2 || len(object.Stops) != 2 || len(object.OpacityStops) != 2 {
		t.Fatalf("object stops lowered to %+v", object)
	}
	if object.ColorStops[0].Position != tuple.ColorStops[0].Position ||
		object.ColorStops[1].Position != tuple.ColorStops[1].Position {
		t.Fatalf("the two dialects disagree on positions: tuple=%v object=%v", tuple.ColorStops, object.ColorStops)
	}
	if object.OpacityStops[0].Opacity != 0.25 || object.OpacityStops[1].Opacity != 0.75 {
		t.Fatalf("opacity stops mismatch: %+v", object.OpacityStops)
	}
}

func TestParseGradientRejectsInvalidInput(t *testing.T) {
	validStops := gradientTupleStopsForTest()
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"not_an_object", "not-a-gradient", "gradient must be an object"},
		{"unknown_property", map[string]any{"stops": validStops, "shadow": true}, `gradient has unsupported property "shadow"`},
		{"unsupported_type", map[string]any{"type": "sweep", "stops": validStops}, "unsupported gradient type sweep"},
		{"both_stop_dialects", map[string]any{"stops": validStops, "color_stops": validStops}, "cannot declare both stops and color_stops"},
		{"bad_start", map[string]any{"stops": validStops, "start": []any{0.0}}, "gradient start must contain two finite numbers"},
		{"bad_end", map[string]any{"stops": validStops, "end": []any{0.0, "x"}}, "gradient end must contain two finite numbers"},
		{"too_few_stops", map[string]any{"stops": []any{[]any{"#000000", 0.0}}}, "gradient stops must contain at least two entries"},
		{"malformed_stop", map[string]any{"stops": []any{[]any{"#000000"}, []any{"#FFFFFF", 1.0}}}, "gradient stop must be [color, position]"},
		{"bad_stop_color", map[string]any{"stops": []any{[]any{"nope", 0.0}, []any{"#FFFFFF", 1.0}}}, "gradient stop color invalid"},
		{"bad_stop_position", map[string]any{"stops": []any{[]any{"#000000", 0.0}, []any{"#FFFFFF", 2.0}}}, "gradient stop position 2 must be in [0, 1]"},
		{"too_few_color_stops", map[string]any{"color_stops": []any{map[string]any{"color": "#000000", "position": 0.0}}}, "gradient color_stops must contain at least two entries"},
		{"color_stop_not_object", map[string]any{"color_stops": []any{"#000000", map[string]any{"color": "#FFFFFF", "position": 1.0}}}, "color_stop must be an object"},
		{"color_stop_unknown_property", map[string]any{"color_stops": []any{map[string]any{"color": "#000000", "position": 0.0, "stop": 1}, map[string]any{"color": "#FFFFFF", "position": 1.0}}}, `color_stop has unsupported property "stop"`},
		{"color_stop_missing_position", map[string]any{"color_stops": []any{map[string]any{"color": "#000000"}, map[string]any{"color": "#FFFFFF", "position": 1.0}}}, "color_stop requires color and position"},
		{"too_few_opacity_stops", map[string]any{"stops": validStops, "opacity_stops": []any{map[string]any{"position": 0.0, "opacity": 1.0}}}, "gradient opacity_stops must contain at least two entries"},
		{"opacity_stop_not_object", map[string]any{"stops": validStops, "opacity_stops": []any{0.0, map[string]any{"position": 1.0, "opacity": 1.0}}}, "gradient opacity_stop must be an object"},
		{"opacity_stop_unknown_property", map[string]any{"stops": validStops, "opacity_stops": []any{map[string]any{"position": 0.0, "opacity": 1.0, "alpha": 1.0}, map[string]any{"position": 1.0, "opacity": 1.0}}}, `opacity_stop has unsupported property "alpha"`},
		{"opacity_stop_out_of_range", map[string]any{"stops": validStops, "opacity_stops": []any{map[string]any{"position": 0.0, "opacity": 2.0}, map[string]any{"position": 1.0, "opacity": 1.0}}}, "gradient opacity_stop values must be in [0, 1]"},
		{"no_stops", map[string]any{"type": "linear"}, "gradient requires at least two color stops"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseGradient(test.raw, "item")
			if err == nil {
				t.Fatalf("parseGradient(%v) returned no error", test.raw)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want substring %q", err, test.want)
			}
		})
	}
}
