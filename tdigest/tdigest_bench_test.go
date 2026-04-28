package tdigest

import (
	"strconv"
	"testing"
)

func BenchmarkAdd(b *testing.B) {
	for _, d := range []float64{50, 100, 500} {
		b.Run("delta"+strconv.FormatFloat(d, 'f', 0, 64), func(b *testing.B) {
			s := New(d)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.Add(float64(i))
			}
		})
	}
}

func BenchmarkQuantile(b *testing.B) {
	s := New(DefaultDelta)
	for i := 0; i < 1_000_000; i++ {
		s.Add(float64(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Quantile(0.99)
	}
}

func BenchmarkMerge(b *testing.B) {
	a := New(DefaultDelta)
	c := New(DefaultDelta)
	for i := 0; i < 100_000; i++ {
		a.Add(float64(i))
		c.Add(float64(i + 100_000))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clone := a.Clone()
		_ = clone.Merge(c)
	}
}
