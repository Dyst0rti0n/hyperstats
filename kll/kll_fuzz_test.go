package kll

import "testing"

// FuzzUnmarshal validates that UnmarshalBinary never panics on
// arbitrary input.
//
// Run with:  go test ./kll/ -run=FuzzUnmarshal -fuzz=FuzzUnmarshal -fuzztime=30s
func FuzzUnmarshal(f *testing.F) {
	// Seed corpus.
	for _, k := range []uint32{MinK, 200, 800} {
		s := NewWithSeed(k, 1)
		for i := 0; i < 50; i++ {
			s.Add(float64(i))
		}
		data, _ := s.MarshalBinary()
		f.Add(data)
	}
	f.Add([]byte{})
	f.Add([]byte{'K', 'L', 'L', 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		var s Sketch
		_ = s.UnmarshalBinary(data)
	})
}
