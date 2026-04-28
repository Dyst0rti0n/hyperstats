package kll

import (
	"strconv"
	"testing"
)

func BenchmarkAdd(b *testing.B) {
	for _, k := range []uint32{100, 200, 800} {
		b.Run("k"+strconv.Itoa(int(k)), func(b *testing.B) {
			s := NewWithSeed(k, 1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.Add(float64(i))
			}
		})
	}
}

func BenchmarkQuantile(b *testing.B) {
	s := NewWithSeed(DefaultK, 1)
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
	a := NewWithSeed(DefaultK, 1)
	c := NewWithSeed(DefaultK, 2)
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
