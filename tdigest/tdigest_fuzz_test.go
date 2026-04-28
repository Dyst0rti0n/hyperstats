package tdigest

import "testing"

// FuzzUnmarshal validates that UnmarshalBinary never panics on
// arbitrary input.
//
// Run with:  go test ./tdigest/ -run=FuzzUnmarshal -fuzz=FuzzUnmarshal -fuzztime=30s
func FuzzUnmarshal(f *testing.F) {
	// Seed corpus.
	for _, d := range []float64{MinDelta, 100, 500} {
		s := New(d)
		for i := 0; i < 50; i++ {
			s.Add(float64(i))
		}
		data, _ := s.MarshalBinary()
		f.Add(data)
	}
	f.Add([]byte{})
	f.Add([]byte{'T', 'D', 'G', 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		var s Sketch
		_ = s.UnmarshalBinary(data)
	})
}
