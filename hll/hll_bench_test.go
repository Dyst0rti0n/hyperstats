package hll

import (
	"strconv"
	"testing"
)

func BenchmarkAddString(b *testing.B) {
	for _, p := range []uint8{10, 14, 18} {
		b.Run("p"+strconv.Itoa(int(p)), func(b *testing.B) {
			s := New(p)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.AddString("user-" + strconv.Itoa(i))
			}
		})
	}
}

func BenchmarkEstimate(b *testing.B) {
	s := New(DefaultPrecision)
	for i := 0; i < 1_000_000; i++ {
		s.AddString(strconv.Itoa(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Estimate()
	}
}

func BenchmarkMerge(b *testing.B) {
	a := New(DefaultPrecision)
	c := New(DefaultPrecision)
	for i := 0; i < 100_000; i++ {
		a.AddString("a-" + strconv.Itoa(i))
		c.AddString("b-" + strconv.Itoa(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clone := a.Clone()
		_ = clone.Merge(c)
	}
}

func BenchmarkMarshalBinary(b *testing.B) {
	s := New(DefaultPrecision)
	for i := 0; i < 1_000_000; i++ {
		s.AddString(strconv.Itoa(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.MarshalBinary()
	}
}
