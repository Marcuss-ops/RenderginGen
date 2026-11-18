package motionmath

import "testing"

func benchmarkTracks(count, keyframes int) []Track {
	tracks := make([]Track, count)
	for i := range tracks {
		keys := make([]Keyframe, keyframes)
		for j := range keys {
			keys[j] = Keyframe{Frame: int64(j * 2), Value: float64(j) / float64(keyframes)}
		}
		tracks[i] = Track{Property: "opacity", Easing: "out_cubic", Keyframes: keys}
	}
	return tracks
}

func BenchmarkRetime(b *testing.B) {
	tracks := benchmarkTracks(100, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Retime(tracks, 400, 200)
	}
}

func BenchmarkSamplePropertyTrack(b *testing.B) {
	keys := make([]Keyframe, 100)
	for i := range keys {
		keys[i] = Keyframe{Frame: int64(i * 6), Value: float64(i) / 100}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SamplePropertyTrack("opacity", "out_cubic", keys); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEasing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := EasingValue("in_out_cubic", 0.37); err != nil {
			b.Fatal(err)
		}
	}
}
