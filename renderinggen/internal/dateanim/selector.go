package dateanim

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrNoDates is returned when a caller asks for a date animation without any
// date. A single date is valid and uses the catalog's documented default.
var ErrNoDates = errors.New("dateanim: at least one date is required")

// dateYearPattern finds the four-digit years an editorial text carries. The
// word boundaries keep video resolutions such as 1920x1080 out of the result.
var dateYearPattern = regexp.MustCompile(`\b(?:18|19|20|21)\d{2}\b`)

// DatesFromText extracts the years from editorial copy in first-seen order.
// Repeated mentions collapse to one date, so a sentence that names 2026 twice
// still describes a three-date sequence rather than a four-date one. This is
// deliberately conservative: the date animations are year-based, while more
// precise forms such as 5 March 2026 still contribute their year.
func DatesFromText(text string) []string {
	matches := dateYearPattern.FindAllString(text, -1)
	seen := make(map[string]bool, len(matches))
	dates := make([]string, 0, len(matches))
	for _, date := range matches {
		if seen[date] {
			continue
		}
		seen[date] = true
		dates = append(dates, date)
	}
	return dates
}

// SelectFromText is the editorial entry point: it extracts the date sequence
// from a text and applies the same stable selector as Select. A text with no
// year is rejected with ErrNoDates instead of silently using a sample date.
func SelectFromText(planID, videoID, groupID, text string) (Style, error) {
	dates := DatesFromText(text)
	if len(dates) == 0 {
		return Style{}, ErrNoDates
	}
	return Select(planID, videoID, groupID, dates)
}

// Select chooses the complete animation for a date sequence.
//
// One date is not a sequence, so it uses Selection.SingleDateStyle. Two or
// more dates use a stable random choice over all 13 styles. Stability is part
// of the contract: the same plan/video/group and the same dates always select
// the same style, so a retry can never silently re-roll an approved cut. The
// date count and values participate in the seed, so two different sequences
// in one plan can choose different styles.
func Select(planID, videoID, groupID string, dates []string) (Style, error) {
	if len(dates) == 0 {
		return Style{}, ErrNoDates
	}
	normalized := make([]string, len(dates))
	for i, date := range dates {
		date = strings.TrimSpace(date)
		if date == "" {
			return Style{}, fmt.Errorf("dateanim: date %d is empty", i)
		}
		normalized[i] = date
	}
	catalog, err := Load()
	if err != nil {
		return Style{}, err
	}
	if len(normalized) < catalog.Selection.MinimumDates {
		style, ok := StyleByID(catalog.Selection.SingleDateStyle)
		if !ok {
			return Style{}, fmt.Errorf("dateanim: default style %q is missing", catalog.Selection.SingleDateStyle)
		}
		return style, nil
	}
	styles := append([]Style(nil), catalog.Styles...)
	sort.Slice(styles, func(i, j int) bool { return styles[i].ID < styles[j].ID })
	seed := strings.Join([]string{
		catalog.Selection.Salt,
		planID,
		videoID,
		groupID,
		fmt.Sprintf("%d", len(normalized)),
		strings.Join(normalized, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	index := binary.BigEndian.Uint64(sum[:8]) % uint64(len(styles))
	return styles[index], nil
}
