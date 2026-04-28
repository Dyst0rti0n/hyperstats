package hll

import "testing"

// FuzzUnmarshal validates that UnmarshalBinary never panics on
// arbitrary input. The function is exposed across trust boundaries
// (cross-service merge), so panic-on-malformed is a DoS vector.
//
// Run with:  go test ./hll/ -run=FuzzUnmarshal -fuzz=FuzzUnmarshal -fuzztime=30s
func FuzzUnmarshal(f *testing.F) {
	// Seed corpus: a few well-formed serialisations.
	for _, p := range []uint8{4, 10, 14, 18} {
		s := New(p)
		s.AddString("seed")
		data, _ := s.MarshalBinary()
		f.Add(data)
	}
	f.Add([]byte{}) // empty
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		var s Sketch
		// Don't care if it errors; we only care that it doesn't panic.
		_ = s.UnmarshalBinary(data)
	})
}
