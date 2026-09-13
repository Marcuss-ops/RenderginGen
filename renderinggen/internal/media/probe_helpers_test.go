package media

import "testing"

// TestParseRationalHonoursBothFfprobeForms pins the two forms ffprobe uses:
// "num/den" (time_base) and "num:den" (sample_aspect_ratio). An unreported or
// non-positive side must be (0,0) — "not reported" — never a guessed value,
// because the contract gate treats zero as unreported and skips the dimension.
func TestParseRationalHonoursBothFfprobeForms(t *testing.T) {
	cases := []struct {
		raw      string
		num, den int
	}{
		{"1/12288", 1, 12288},
		{"1:1", 1, 1},
		{"48000/1", 48000, 1},
		{"", 0, 0},
		{"N/A", 0, 0},
		{"0/0", 0, 0},
		{"0:1", 0, 0},
		{"1/0", 0, 0},
		{"garbage", 0, 0},
	}
	for _, tc := range cases {
		num, den := parseRational(tc.raw)
		if num != tc.num || den != tc.den {
			t.Errorf("parseRational(%q) = %d/%d, want %d/%d", tc.raw, num, den, tc.num, tc.den)
		}
	}
}

// TestLevelStringRendersFfprobeLevel pins the H.264 level projection: ffprobe
// reports an integer (41) while the contract declares the dotted form ("4.1").
func TestLevelStringRendersFfprobeLevel(t *testing.T) {
	cases := map[int]string{0: "", 40: "4.0", 41: "4.1", 51: "5.1", 31: "3.1"}
	for level, want := range cases {
		if got := levelString(level); got != want {
			t.Errorf("levelString(%d) = %q, want %q", level, got, want)
		}
	}
}
