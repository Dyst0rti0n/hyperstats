package cms

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/Dyst0rti0n/hyperstats/hash"
)

// Sketch is a Count-Min Sketch with width w and depth d.
//
// A Sketch is not safe for concurrent use. Use per-shard sketches and
// Merge or wrap with a sync.Mutex for concurrent updates.
type Sketch struct {
	w      uint32   // width: counters per row
	d      uint32   // depth: number of rows
	counts []uint64 // flat row-major: counts[i*w + j] is row i column j
	total  uint64   // running sum of all weights added (for ε·N bound math)
}

// New constructs a Sketch with explicit (width, depth).
//
// Use NewWithGuarantees if you want to specify the (ε, δ) bound directly
// rather than computing dimensions yourself.
//
// Both width and depth must be positive. New panics otherwise; these are
// static configuration choices and a programming bug to get wrong.
func New(width, depth uint32) *Sketch {
	if width == 0 || depth == 0 {
		panic(fmt.Sprintf("cms: width=%d, depth=%d must both be > 0", width, depth))
	}
	// Guard against integer overflow in the flat allocation.
	if uint64(width)*uint64(depth) > 1<<32 {
		panic(fmt.Sprintf("cms: width*depth = %d overflows; pick smaller dimensions",
			uint64(width)*uint64(depth)))
	}
	return &Sketch{
		w:      width,
		d:      depth,
		counts: make([]uint64, uint64(width)*uint64(depth)),
	}
}

// NewWithGuarantees returns a Sketch sized to satisfy the bound:
//
//	Pr[ f̂(x) - f(x) ≤ ε · N ] ≥ 1 - δ
//
// Both eps and delta must be in (0, 1). Smaller eps/delta means more
// counters and more memory. NewWithGuarantees panics for out-of-range
// inputs.
//
// The chosen dimensions are w = ⌈e/ε⌉, d = ⌈ln(1/δ)⌉ as in Cormode &
// Muthukrishnan (2005).
func NewWithGuarantees(eps, delta float64) *Sketch {
	if !(eps > 0 && eps < 1) {
		panic(fmt.Sprintf("cms: eps = %f must be in (0, 1)", eps))
	}
	if !(delta > 0 && delta < 1) {
		panic(fmt.Sprintf("cms: delta = %f must be in (0, 1)", delta))
	}
	w := uint32(math.Ceil(math.E / eps))
	d := uint32(math.Ceil(math.Log(1.0 / delta)))
	if d == 0 {
		d = 1
	}
	return New(w, d)
}

// Width returns the number of counters per row.
func (s *Sketch) Width() uint32 { return s.w }

// Depth returns the number of rows.
func (s *Sketch) Depth() uint32 { return s.d }

// TotalMass returns the sum of all weights added (i.e. N in the ε·N bound).
// Useful for clients that want to convert estimates into a relative scale.
func (s *Sketch) TotalMass() uint64 { return s.total }

// hashes returns the d row offsets for x using the Kirsch-Mitzenmacher
// double-hashing trick. Inlined into hot paths for performance; this
// helper exists for clarity and is used by Count.
//
//go:inline
func (s *Sketch) hashes(x []byte) (h1, h2 uint64) {
	return hash.Sum128(x, hashSeed)
}

const hashSeed uint32 = 0x636d_732b // "cms+"

// Add increments x's count by v. v must be non-negative; CMS does not
// support deletion (use a Count-Min-Mean-Sketch or other variant for that).
func (s *Sketch) Add(x []byte, v uint64) {
	if v == 0 {
		return
	}
	h1, h2 := s.hashes(x)
	for i := uint32(0); i < s.d; i++ {
		col := uint32((h1 + uint64(i)*h2) % uint64(s.w))
		s.counts[i*s.w+col] += v
	}
	s.total += v
}

// AddString is the string-keyed convenience form of Add.
func (s *Sketch) AddString(x string, v uint64) {
	if v == 0 {
		return
	}
	h1, h2 := hash.SumString(x, hashSeed)
	for i := uint32(0); i < s.d; i++ {
		col := uint32((h1 + uint64(i)*h2) % uint64(s.w))
		s.counts[i*s.w+col] += v
	}
	s.total += v
}

// Count returns f̂(x), an upper bound on the true count of x with
// probability ≥ 1-δ given the sketch was sized for (ε, δ) and the
// over-estimate is at most ε·TotalMass().
func (s *Sketch) Count(x []byte) uint64 {
	h1, h2 := s.hashes(x)
	minimum := uint64(math.MaxUint64)
	for i := uint32(0); i < s.d; i++ {
		col := uint32((h1 + uint64(i)*h2) % uint64(s.w))
		if c := s.counts[i*s.w+col]; c < minimum {
			minimum = c
		}
	}
	if minimum == math.MaxUint64 {
		return 0 // d=0 would be invalid but be defensive
	}
	return minimum
}

// CountString is the string-keyed convenience form of Count.
func (s *Sketch) CountString(x string) uint64 {
	h1, h2 := hash.SumString(x, hashSeed)
	minimum := uint64(math.MaxUint64)
	for i := uint32(0); i < s.d; i++ {
		col := uint32((h1 + uint64(i)*h2) % uint64(s.w))
		if c := s.counts[i*s.w+col]; c < minimum {
			minimum = c
		}
	}
	if minimum == math.MaxUint64 {
		return 0
	}
	return minimum
}

// Merge folds o into s in place: cell-wise addition. Both sketches must
// have identical (width, depth). The merge corresponds exactly to having
// processed the union of the two input streams.
//
// Merging is the canonical way to parallelise CMS: shard your stream,
// build a sketch per shard, merge.
func (s *Sketch) Merge(o *Sketch) error {
	if s.w != o.w || s.d != o.d {
		return fmt.Errorf("cms: dimension mismatch: %dx%d vs %dx%d",
			s.w, s.d, o.w, o.d)
	}
	// Guard against TotalMass overflow on merge.
	if s.total > math.MaxUint64-o.total {
		return errors.New("cms: total-mass overflow on merge")
	}
	for i := range s.counts {
		// Per-cell overflow check.
		if s.counts[i] > math.MaxUint64-o.counts[i] {
			return fmt.Errorf("cms: counter[%d] overflow on merge", i)
		}
		s.counts[i] += o.counts[i]
	}
	s.total += o.total
	return nil
}

// Reset zeros all counters and the total mass without releasing memory.
func (s *Sketch) Reset() {
	for i := range s.counts {
		s.counts[i] = 0
	}
	s.total = 0
}

// Clone returns an independent copy of s.
func (s *Sketch) Clone() *Sketch {
	c := &Sketch{
		w:      s.w,
		d:      s.d,
		counts: make([]uint64, len(s.counts)),
		total:  s.total,
	}
	copy(c.counts, s.counts)
	return c
}

// MemoryBytes returns the heap footprint of the counter array.
func (s *Sketch) MemoryBytes() int { return len(s.counts) * 8 }

// On-disk format (little-endian):
//
//	[0..2]  magic 'C','M','S'
//	[3]     version (1)
//	[4..8]  width  (uint32)
//	[8..12] depth  (uint32)
//	[12..20] total mass (uint64)
//	[20..]  width*depth uint64 counters

const formatVersion byte = 1

const headerLen = 20

var magic = [...]byte{'C', 'M', 'S'}

// MarshalBinary serialises the sketch.
func (s *Sketch) MarshalBinary() ([]byte, error) {
	out := make([]byte, headerLen+len(s.counts)*8)
	copy(out[0:3], magic[:])
	out[3] = formatVersion
	binary.LittleEndian.PutUint32(out[4:], s.w)
	binary.LittleEndian.PutUint32(out[8:], s.d)
	binary.LittleEndian.PutUint64(out[12:], s.total)
	for i, c := range s.counts {
		binary.LittleEndian.PutUint64(out[headerLen+i*8:], c)
	}
	return out, nil
}

// UnmarshalBinary deserialises into s. Any prior contents are discarded.
//
// Returns an error rather than panicking on malformed input. Sketches may
// be passed across services and a panic on bad bytes is a DoS vector.
func (s *Sketch) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen {
		return errors.New("cms: short header")
	}
	if data[0] != magic[0] || data[1] != magic[1] || data[2] != magic[2] {
		return errors.New("cms: bad magic")
	}
	if data[3] != formatVersion {
		return fmt.Errorf("cms: unsupported version %d", data[3])
	}
	w := binary.LittleEndian.Uint32(data[4:])
	d := binary.LittleEndian.Uint32(data[8:])
	if w == 0 || d == 0 {
		return errors.New("cms: zero width or depth")
	}
	cells := uint64(w) * uint64(d)
	if cells > 1<<32 {
		return errors.New("cms: width*depth overflow")
	}
	expected := uint64(headerLen) + cells*8
	if uint64(len(data)) != expected {
		return fmt.Errorf("cms: body %d bytes, want %d", len(data), expected)
	}
	s.w = w
	s.d = d
	s.total = binary.LittleEndian.Uint64(data[12:])
	s.counts = make([]uint64, cells)
	for i := range s.counts {
		s.counts[i] = binary.LittleEndian.Uint64(data[headerLen+i*8:])
	}
	// Sanity: sum of any one row must equal total.
	rowSum := uint64(0)
	for j := uint32(0); j < w; j++ {
		// Overflow-safe sum across one row.
		c := s.counts[j]
		if rowSum > math.MaxUint64-c {
			return errors.New("cms: row sum overflow on validation")
		}
		rowSum += c
	}
	if rowSum != s.total {
		return fmt.Errorf("cms: row 0 sum %d != total %d", rowSum, s.total)
	}
	return nil
}
