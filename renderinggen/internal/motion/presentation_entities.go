package motion

import (
	"fmt"
	"math/big"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MetricValue preserves the editorial spelling while exposing an exact numeric
// value and separate prefix, suffix, and unit for alignment/layout decisions.
type MetricValue struct {
	Raw, Prefix, Suffix string
	Value               *big.Rat
}

var metricPattern = regexp.MustCompile(`^([+$€£]?)(-?(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?)(?:\s*(%|[A-Za-z]{1,8}))?$`)

func ParseMetricValue(raw string) (MetricValue, error) {
	input := strings.TrimSpace(raw)
	match := metricPattern.FindStringSubmatch(input)
	if match == nil {
		return MetricValue{}, fmt.Errorf("metric: invalid value %q", raw)
	}
	numeric := strings.ReplaceAll(match[2], ",", "")
	value, ok := new(big.Rat).SetString(numeric)
	if !ok {
		return MetricValue{}, fmt.Errorf("metric: invalid numeric value %q", raw)
	}
	switch strings.ToUpper(match[3]) {
	case "K":
		value.Mul(value, big.NewRat(1_000, 1))
	case "M":
		value.Mul(value, big.NewRat(1_000_000, 1))
	case "B":
		value.Mul(value, big.NewRat(1_000_000_000, 1))
	}
	return MetricValue{Raw: input, Prefix: match[1], Value: value, Suffix: match[3]}, nil
}

func MetricCounterAt(start, target *big.Rat, frame, first, last int) (*big.Rat, error) {
	if start == nil || target == nil || first < 0 || last <= first || frame < first || frame > last {
		return nil, fmt.Errorf("metric: invalid counter range")
	}
	if frame == last {
		return new(big.Rat).Set(target), nil
	}
	if frame == first {
		return new(big.Rat).Set(start), nil
	}
	delta := new(big.Rat).Sub(target, start)
	delta.Mul(delta, big.NewRat(int64(frame-first), int64(last-first)))
	return new(big.Rat).Add(start, delta), nil
}

// DateEntity supports the six common editorial forms without changing raw
// copy. Start/End are UTC dates; range is inclusive and a single date has End=Start.
type DateEntity struct {
	Raw, Display, Granularity string
	Start, End                time.Time
}

var yearRangePattern = regexp.MustCompile(`^(\d{4})\s*[-–—/]\s*(\d{4})$`)
var quarterPattern = regexp.MustCompile(`^Q([1-4])\s+(\d{4})$`)

func ParseDateEntity(raw string) (DateEntity, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return DateEntity{}, fmt.Errorf("date: empty input")
	}
	if m := yearRangePattern.FindStringSubmatch(value); m != nil {
		startYear, _ := strconv.Atoi(m[1])
		endYear, _ := strconv.Atoi(m[2])
		if startYear > endYear {
			return DateEntity{}, fmt.Errorf("date: reversed range %q", raw)
		}
		return DateEntity{Raw: value, Display: fmt.Sprintf("%d–%d", startYear, endYear), Granularity: "range", Start: utcDate(startYear, 1, 1), End: utcDate(endYear, 12, 31)}, nil
	}
	if m := quarterPattern.FindStringSubmatch(value); m != nil {
		q, _ := strconv.Atoi(m[1])
		year, _ := strconv.Atoi(m[2])
		month := time.Month((q-1)*3 + 1)
		return DateEntity{Raw: value, Display: fmt.Sprintf("Q%d %d", q, year), Granularity: "quarter", Start: utcDate(year, int(month), 1), End: utcDate(year, int(month)+2, daysInMonth(year, month+2))}, nil
	}
	for _, layout := range []struct{ in, out, granularity string }{
		{"2006", "2006", "year"}, {"January 2006", "January 2006", "month"},
		{"2 January 2006", "2 January 2006", "day"}, {"02 January 2006", "2 January 2006", "day"},
		{"2006-01-02", "2 January 2006", "day"},
	} {
		if parsed, err := time.Parse(layout.in, value); err == nil {
			start, end := parsed.UTC(), parsed.UTC()
			if layout.granularity == "year" {
				start = utcDate(parsed.Year(), 1, 1)
				end = utcDate(parsed.Year(), 12, 31)
			}
			if layout.granularity == "month" {
				start = utcDate(parsed.Year(), int(parsed.Month()), 1)
				end = utcDate(parsed.Year(), int(parsed.Month()), daysInMonth(parsed.Year(), parsed.Month()))
			}
			return DateEntity{Raw: value, Display: parsed.Format(layout.out), Granularity: layout.granularity, Start: start, End: end}, nil
		}
	}
	return DateEntity{}, fmt.Errorf("date: unsupported or invalid form %q", raw)
}

func utcDate(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// EntityCardInput keeps identity fields immutable from asset attachment onward.
type EntityCardInput struct{ Name, ImagePath string }

func NewEntityCard(name, imagePath string) (EntityCardInput, error) {
	if strings.TrimSpace(name) == "" {
		return EntityCardInput{}, fmt.Errorf("entity card: name is required")
	}
	if strings.TrimSpace(imagePath) == "" || filepath.IsAbs(imagePath) || strings.Contains(filepath.ToSlash(imagePath), "../") {
		return EntityCardInput{}, fmt.Errorf("entity card: image path must be a safe asset-relative path")
	}
	return EntityCardInput{Name: name, ImagePath: imagePath}, nil
}

type EntityCardPlacement struct {
	Entity                          EntityCardInput
	CenterX, CenterY, Width, Height float64
}

// EntityFocusOrder returns the placement indices in deterministic reading
// order — top-to-bottom, left-to-right — so narration focus can walk a grouped
// scene (A → B → …) without a second layout pass. It never mutates geometry:
// focusing B leaves A exactly where the group put it, and the caption of every
// unfocused card stays visible.
func EntityFocusOrder(placements []EntityCardPlacement) []int {
	order := make([]int, len(placements))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := placements[order[i]], placements[order[j]]
		if a.CenterY == b.CenterY {
			return a.CenterX < b.CenterX
		}
		return a.CenterY < b.CenterY
	})
	return order
}

func LayoutEntityGroup(entities []EntityCardInput, canvasWidth, canvasHeight, gutter float64) ([]EntityCardPlacement, error) {
	if len(entities) < 1 || len(entities) > 5 || canvasWidth <= 0 || canvasHeight <= 0 || gutter < 0 {
		return nil, fmt.Errorf("entity group: invalid count or canvas")
	}
	columns := len(entities)
	if columns > 2 {
		columns = 2
	}
	rows := (len(entities) + columns - 1) / columns
	cellW := (canvasWidth - gutter*float64(columns-1)) / float64(columns)
	cellH := canvasHeight / float64(rows)
	cardW := cellW * 0.88
	cardH := cellH * 0.78
	out := make([]EntityCardPlacement, len(entities))
	for i, entity := range entities {
		row, col := i/columns, i%columns
		out[i] = EntityCardPlacement{Entity: entity, CenterX: cellW*(float64(col)+0.5) + gutter*float64(col), CenterY: cellH * (float64(row) + 0.5), Width: cardW, Height: cardH}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CenterY == out[j].CenterY {
			return out[i].CenterX < out[j].CenterX
		}
		return out[i].CenterY < out[j].CenterY
	})
	return out, nil
}
