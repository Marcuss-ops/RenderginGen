package dateanim

import (
	"reflect"
	"testing"
)

func TestCatalogContainsTheThirteenCleanStyles(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Styles) != 13 {
		t.Fatalf("styles = %d, want 13", len(catalog.Styles))
	}
	if catalog.Labels {
		t.Fatal("labels must remain disabled")
	}
	for _, id := range []string{"14_mask_reveal", "15_archival_chronology"} {
		if _, ok := StyleByID(id); ok {
			t.Fatalf("removed style %q is still selectable", id)
		}
	}
	for _, style := range catalog.Styles {
		if style.FontSize <= 0 {
			t.Fatalf("style %q has no fixed font size", style.ID)
		}
	}
}

func TestSelectIsStableForTheSameIdentity(t *testing.T) {
	dates := []string{"1948", "1969", "1998", "2026"}
	first, err := Select("plan-a", "video-a", "dates", dates)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Select("plan-a", "video-a", "dates", dates)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry changed style: %q -> %q", first.ID, second.ID)
	}
}

func TestSelectUsesOneDefaultForASingleDate(t *testing.T) {
	style, err := Select("plan-a", "video-a", "dates", []string{"1948"})
	if err != nil {
		t.Fatal(err)
	}
	if style.ID != "01_timeline_progress" {
		t.Fatalf("single-date style = %q, want 01_timeline_progress", style.ID)
	}
}

func TestDatesFromTextExtractsAndDeduplicatesYears(t *testing.T) {
	text := "Nel 1948 nasce; nel 1948 si consolida, nel 1969 cambia; nel 1998 arriva il lancio; nel 2026 il presente. Export 1920x1080."
	got := DatesFromText(text)
	want := []string{"1948", "1969", "1998", "2026"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dates = %v, want %v", got, want)
	}
}

func TestSelectFromTextUsesTheThirteenStyleCatalog(t *testing.T) {
	text := "Nel 1948 nasce; nel 1969 cambia; nel 1998 arriva il lancio; nel 2026 il presente."
	style, err := SelectFromText("plan-a", "video-a", "dates", text)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := StyleByID(style.ID); !ok {
		t.Fatalf("selected style %q is not in the 13-style catalog", style.ID)
	}
	if style.Video == "" || style.Plan == "" || style.FontSize <= 0 {
		t.Fatalf("selected style is incomplete: %+v", style)
	}
}

func TestSelectFromTextSingleDateUsesTheDefault(t *testing.T) {
	style, err := SelectFromText("plan-a", "video-a", "dates", "Nel 2026 il presente.")
	if err != nil {
		t.Fatal(err)
	}
	if style.ID != "01_timeline_progress" {
		t.Fatalf("single-date style = %q, want 01_timeline_progress", style.ID)
	}
}

func TestSelectFromTextRejectsTextWithoutYears(t *testing.T) {
	if _, err := SelectFromText("plan-a", "video-a", "dates", "Nessuna data in questa frase."); err != ErrNoDates {
		t.Fatalf("missing dates error = %v, want ErrNoDates", err)
	}
}

func TestSelectRejectsEmptyDates(t *testing.T) {
	if _, err := Select("plan-a", "video-a", "dates", nil); err != ErrNoDates {
		t.Fatalf("empty dates error = %v, want ErrNoDates", err)
	}
	if _, err := Select("plan-a", "video-a", "dates", []string{" "}); err == nil {
		t.Fatal("blank date must fail")
	}
}

func TestSelectReachesEveryStyleAcrossPlans(t *testing.T) {
	dates := []string{"1948", "1969", "1998", "2026"}
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		style, err := Select("plan-"+string(rune('a'+i%26))+"-"+itoa(i), "video", "dates", dates)
		if err != nil {
			t.Fatal(err)
		}
		seen[style.ID] = true
	}
	if len(seen) != 13 {
		t.Fatalf("reachable styles = %d, want 13: %v", len(seen), seen)
	}
}

func TestSelectFromTextReachesEveryStyleAcrossPlans(t *testing.T) {
	text := "Nel 1948 nasce; nel 1969 cambia; nel 1998 arriva il lancio; nel 2026 il presente."
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		style, err := SelectFromText("plan-"+string(rune('a'+i%26))+"-"+itoa(i), "video", "dates", text)
		if err != nil {
			t.Fatal(err)
		}
		seen[style.ID] = true
	}
	if len(seen) != 13 {
		t.Fatalf("text-reachable styles = %d, want 13: %v", len(seen), seen)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
