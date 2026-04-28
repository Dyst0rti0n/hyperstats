package cms

import (
	"strconv"
	"testing"
)

func BenchmarkAddString(b *testing.B) {
	for _, dim := range []struct {
		w, d uint32
		name string
	}{
		{w: 256, d: 5, name: "256x5"},
		{w: 2719, d: 5, name: "2719x5"},
		{w: 27183, d: 5, name: "27183x5"},
	} {
		b.Run(dim.name, func(b *testing.B) {
			s := New(dim.w, dim.d)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.AddString("event-"+strconv.Itoa(i), 1)
			}
		})
	}
}

func BenchmarkCount(b *testing.B) {
	s := NewWithGuarantees(0.001, 0.01)
	for i := 0; i < 100_000; i++ {
		s.AddString("k-"+strconv.Itoa(i%1000), 1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.CountString("k-" + strconv.Itoa(i%1000))
	}
}

func BenchmarkMerge(b *testing.B) {
	a := NewWithGuarantees(0.001, 0.01)
	c := NewWithGuarantees(0.001, 0.01)
	for i := 0; i < 100_000; i++ {
		a.AddString("a-"+strconv.Itoa(i), 1)
		c.AddString("b-"+strconv.Itoa(i), 1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clone := a.Clone()
		_ = clone.Merge(c)
	}
}
