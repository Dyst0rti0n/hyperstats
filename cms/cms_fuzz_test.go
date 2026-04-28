package cms

import "testing"

// FuzzUnmarshal validates that UnmarshalBinary never panics on
// arbitrary input.
//
// Run with:  go test ./cms/ -run=FuzzUnmarshal -fuzz=FuzzUnmarshal -fuzztime=30s
func FuzzUnmarshal(f *testing.F) {
	// Seed corpus.
	for _, dim := range [][2]uint32{{4, 2}, {64, 3}, {2719, 5}} {
		s := New(dim[0], dim[1])
		s.AddString("seed", 1)
		data, _ := s.MarshalBinary()
		f.Add(data)
	}
	f.Add([]byte{})
	f.Add([]byte{'C', 'M', 'S', 1, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		var s Sketch
		_ = s.UnmarshalBinary(data)
	})
}
