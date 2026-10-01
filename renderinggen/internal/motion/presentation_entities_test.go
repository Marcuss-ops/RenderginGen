package motion

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"
)

func TestMetricValueParsing(t *testing.T) {
	cases := []struct{ raw, prefix, suffix, want string }{
		{"42", "", "", "42"}, {"42.5", "", "", "85/2"}, {"-12", "", "", "-12"},
		{"+18.7", "+", "", "187/10"}, {"42%", "", "%", "42"}, {"$3.4B", "$", "B", "3400000000"},
		{"€12.5M", "€", "M", "12500000"}, {"1,250,000", "", "", "1250000"},
		{"120 km", "", "km", "120"}, {"9.8x", "", "x", "49/5"}, {"1.25M", "", "M", "1250000"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseMetricValue(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Prefix != tc.prefix || got.Suffix != tc.suffix || got.Value.RatString() != tc.want {
				t.Fatalf("got %+v value=%s want %s/%s/%s", got, got.Value.RatString(), tc.prefix, tc.want, tc.suffix)
			}
		})
	}
	for _, bad := range []string{"", "NaN", "$", "1,2,3", "1e9", "42%%", "--12", "1.", "1,234.56.7", "42 units too many"} {
		if _, err := ParseMetricValue(bad); err == nil {
			t.Errorf("accepted invalid metric %q", bad)
		}
	}
}

func TestMetricCounterStartsEndsMonotonicAndNoOvershoot(t *testing.T) {
	for _, pair := range [][2]string{{"0", "42"}, {"-12", "18"}, {"42", "-12"}} {
		start, target := mustRat(t, pair[0]), mustRat(t, pair[1])
		previous, increasing := mustRat(t, pair[0]), start.Cmp(target) <= 0
		for frame := 0; frame <= 100; frame++ {
			value, err := MetricCounterAt(start, target, frame, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if increasing && value.Cmp(previous) < 0 || !increasing && value.Cmp(previous) > 0 {
				t.Fatalf("counter is not monotonic at frame %d", frame)
			}
			lo, hi := start, target
			if lo.Cmp(hi) > 0 {
				lo, hi = hi, lo
			}
			if value.Cmp(lo) < 0 || value.Cmp(hi) > 0 {
				t.Fatalf("counter overshot at frame %d: %s", frame, value)
			}
			previous = value
		}
		first, _ := MetricCounterAt(start, target, 0, 0, 100)
		end, _ := MetricCounterAt(start, target, 100, 0, 100)
		if first.Cmp(start) != 0 || end.Cmp(target) != 0 {
			t.Fatalf("counter endpoints are not exact: %s -> %s", first, end)
		}
	}
	if _, err := MetricCounterAt(nil, mustRat(t, "1"), 1, 0, 2); err == nil {
		t.Fatal("nil start accepted")
	}
}

func mustRat(t *testing.T, value string) *big.Rat {
	t.Helper()
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatal(value)
	}
	return parsed
}

func TestDateEntityNormalization(t *testing.T) {
	cases := []struct {
		raw, display, granularity string
		year                      int
	}{
		{"2026", "2026", "year", 2026}, {"March 2026", "March 2026", "month", 2026},
		{"30 September 2026", "30 September 2026", "day", 2026}, {"Q4 2026", "Q4 2026", "quarter", 2026},
		{"2010-2020", "2010–2020", "range", 2010}, {"1999/2000", "1999–2000", "range", 1999},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseDateEntity(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Display != tc.display || got.Granularity != tc.granularity || got.Start.Year() != tc.year {
				t.Fatalf("got %+v", got)
			}
			if got.Start.Location() != time.UTC || got.End.Before(got.Start) {
				t.Fatalf("invalid interval %+v", got)
			}
		})
	}
	if _, err := ParseDateEntity("2025-2020"); err == nil {
		t.Fatal("reversed date range accepted")
	}
	if _, err := ParseDateEntity("not a date"); err == nil {
		t.Fatal("unknown date form accepted")
	}
}

func TestDateTimelineSpacingModesAreSortedAndMonotonic(t *testing.T) {
	entities := make([]DateEntity, 0, 3)
	for _, raw := range []string{"2020", "1990", "2000"} {
		entity, err := ParseDateEntity(raw)
		if err != nil {
			t.Fatal(err)
		}
		entities = append(entities, entity)
	}
	for _, mode := range []string{"equal", "proportional"} {
		positions, err := LayoutDateTimeline(entities, 100, 900, mode)
		if err != nil {
			t.Fatal(err)
		}
		if len(positions) != 3 || positions[0].Date.Display != "1990" || positions[1].Date.Display != "2000" || positions[2].Date.Display != "2020" {
			t.Fatalf("%s order = %+v", mode, positions)
		}
		if !(positions[0].X < positions[1].X && positions[1].X < positions[2].X) {
			t.Fatalf("%s positions are not strictly monotonic: %+v", mode, positions)
		}
		if positions[0].X != 100 || positions[2].X != 900 {
			t.Fatalf("%s endpoints = %v, %v", mode, positions[0].X, positions[2].X)
		}
	}
	equal, _ := LayoutDateTimeline(entities, 100, 900, "equal")
	proportional, _ := LayoutDateTimeline(entities, 100, 900, "proportional")
	if equal[1].X == proportional[1].X {
		t.Fatalf("unequal 10y/20y gaps should differ by spacing mode: equal=%v proportional=%v", equal[1].X, proportional[1].X)
	}
	if _, err := LayoutDateTimeline(entities, 900, 100, "equal"); err == nil {
		t.Fatal("reversed x interval accepted")
	}
	if _, err := LayoutDateTimeline(entities, 0, 10, "random"); err == nil {
		t.Fatal("unknown spacing mode accepted")
	}
	if _, err := LayoutDateTimeline(nil, 0, 10, "equal"); err == nil {
		t.Fatal("empty timeline accepted")
	}
	if _, err := LayoutDateTimeline(entities, math.NaN(), 10, "equal"); err == nil {
		t.Fatal("non-finite timeline bounds accepted")
	}
	invalid := []DateEntity{{Raw: "invalid", Display: "invalid"}}
	if _, err := LayoutDateTimeline(invalid, 0, 10, "equal"); err == nil {
		t.Fatal("unnormalized timeline date accepted")
	}
}

func TestEntityCardPreservesUnicodeAndImagePathAndRejectsTraversal(t *testing.T) {
	for _, name := range []string{"José Mourinho", "François Hollande", "李小龍", "محمد علي", "Alexander Jonathan Montgomery Williams"} {
		card, err := NewEntityCard(name, "assets/people/person.png")
		if err != nil {
			t.Fatal(err)
		}
		if card.Name != name || card.ImagePath != "assets/people/person.png" {
			t.Fatalf("identity changed: %+v", card)
		}
	}
	for _, path := range []string{"", "../private.png", "/etc/passwd", "assets/../../secret"} {
		if _, err := NewEntityCard("Ada", path); err == nil {
			t.Errorf("unsafe image path accepted: %q", path)
		}
	}
}

func TestTwoEntitiesGroupIntoOneSafeNonOverlappingScene(t *testing.T) {
	entities := []EntityCardInput{{Name: "PERSON A", ImagePath: "a.png"}, {Name: "PERSON B", ImagePath: "b.png"}}
	placements, err := LayoutEntityGroup(entities, 1920, 1080, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements", len(placements))
	}
	for _, p := range placements {
		if p.CenterX-p.Width/2 < 0 || p.CenterX+p.Width/2 > 1920 || p.CenterY-p.Height/2 < 0 || p.CenterY+p.Height/2 > 1080 {
			t.Errorf("out of safe area: %+v", p)
		}
	}
	if math.Abs(placements[0].CenterX-placements[1].CenterX) < placements[0].Width {
		t.Fatalf("cards overlap: %+v", placements)
	}
	again, err := LayoutEntityGroup(entities, 1920, 1080, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(placements, again) {
		t.Fatal("layout is nondeterministic")
	}
	for _, invalid := range [][]EntityCardInput{nil, make([]EntityCardInput, 6)} {
		if _, err := LayoutEntityGroup(invalid, 1920, 1080, 32); err == nil {
			t.Errorf("invalid entity count %d accepted", len(invalid))
		}
	}
}
