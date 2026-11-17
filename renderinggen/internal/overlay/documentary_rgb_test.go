package overlay

import "testing"

func TestDocumentaryRGBRejectsInvalidHex(t *testing.T) {
	fallback := []float64{1, 0.9, 0, 0.88}
	for _, hex := range []string{"", "#12345", "#ZZZZZZ", "DC2626", "#DC262"} {
		got := documentaryRGB(hex)
		if len(got) != len(fallback) {
			t.Fatalf("documentaryRGB(%q) returned %d channels, want %d", hex, len(got), len(fallback))
		}
		for i := range fallback {
			if got[i] != fallback[i] {
				t.Fatalf("documentaryRGB(%q) = %v, want fallback %v", hex, got, fallback)
			}
		}
	}

	got := documentaryRGB("#DC2626")
	want := []float64{float64(0xDC) / 255, float64(0x26) / 255, float64(0x26) / 255, 0.88}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("documentaryRGB(%q) = %v, want %v", "#DC2626", got, want)
		}
	}
}
