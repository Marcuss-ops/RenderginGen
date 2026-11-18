package mapgeom

import (
	"math"
	"testing"
)

func benchmarkRing(n int) Ring {
	ring := make(Ring, 0, n)
	for i := 0; i < n; i++ {
		angle := 2 * math.Pi * float64(i) / float64(n)
		radius := 1 + 0.2*math.Sin(6*angle) + 0.05*math.Sin(31*angle)
		ring = append(ring, Point{radius * math.Cos(angle), radius * math.Sin(angle)})
	}
	return ring
}

func BenchmarkSimplifyRing(b *testing.B) {
	ring := benchmarkRing(20000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := SimplifyRing(ring, 500); len(got) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkDecimateRing(b *testing.B) {
	ring := benchmarkRing(20000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := DecimateRing(ring, 500); len(got) != 500 {
			b.Fatalf("len=%d, want 500", len(got))
		}
	}
}

func BenchmarkRingsBounds(b *testing.B) {
	rings := []Ring{benchmarkRing(2000), benchmarkRing(2000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RingsBounds(rings)
	}
}
