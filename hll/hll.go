package hll

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/dystortion/hyperstats/hash"
)

// Sketch is a HyperLogLog cardinality estimator.
//
// A Sketch is not safe for concurrent use. Wrap with a sync.Mutex or use
// per-goroutine sketches and Merge them; merging is the canonical way to
// parallelise HLL.
type Sketch struct {
	p uint8 // precision: register count m = 2^p, valid range [4, 18]

	// Exactly one of {sparse, dense} is non-nil at any time.
	// sparse maps register index -> ρ value. We promote to dense once
	// the sparse representation would exceed dense memory.
	sparse map[uint32]uint8
	dense  []uint8
}

const (
	// MinPrecision is the smallest allowed precision (16 registers, ~26%
	// standard error). Useful only for tests; production should use ≥ 10.
	MinPrecision uint8 = 4
	// MaxPrecision is the largest allowed precision (262144 registers,
	// ~0.20% standard error, 256 KiB memory).
	MaxPrecision uint8 = 18
	// DefaultPrecision balances memory (~16 KiB) against accuracy (~0.81%
	// standard error). It is the recommended starting point.
	DefaultPrecision uint8 = 14

	// hashSeed is fixed so that two sketches built independently from the
	// same stream produce identical registers and merge correctly. Changing
	// this value silently breaks compatibility with previously serialised
	// sketches; treat it as part of the on-disk format.
	hashSeed uint32 = 0x68_6c_6c_2b // "hll+"
)

// New returns an empty Sketch with the given precision. p must be in
// [MinPrecision, MaxPrecision]; New panics otherwise because precision is
// a static configuration choice and a programming bug to get wrong.
//
// New starts in sparse mode and uses O(1) memory until elements are added.
func New(precision uint8) *Sketch {
	if precision < MinPrecision || precision > MaxPrecision {
		panic(fmt.Sprintf("hll: precision %d out of range [%d, %d]",
			precision, MinPrecision, MaxPrecision))
	}
	return &Sketch{
		p:      precision,
		sparse: make(map[uint32]uint8),
	}
}

// Precision returns the configured precision parameter p.
func (s *Sketch) Precision() uint8 { return s.p }

// m returns the register count, 2^p.
func (s *Sketch) m() uint32 { return 1 << s.p }

// AddHash incorporates a pre-hashed value. Use this if you already have a
// 64-bit hash from MurmurHash3 or another high-quality non-cryptographic
// hash. The hash MUST have good avalanche; weak hashes (FNV, FxHash) bias
// the register distribution and break the σ bound.
func (s *Sketch) AddHash(h uint64) {
	idx := uint32(h >> (64 - s.p))                               // top p bits → register index
	rho := uint8(bits.LeadingZeros64((h<<s.p)|(1<<(s.p-1)))) + 1 // ρ on remaining bits
	s.update(idx, rho)
}

// Add incorporates a byte slice element.
func (s *Sketch) Add(b []byte) {
	s.AddHash(hash.Sum64(b, hashSeed))
}

// AddString incorporates a string element. Equivalent to Add([]byte(s)).
func (s *Sketch) AddString(v string) {
	h1, _ := hash.SumString(v, hashSeed)
	s.AddHash(h1)
}

// update applies max(register[idx], rho) and triggers sparse→dense
// promotion if the sparse map has grown past its memory advantage.
func (s *Sketch) update(idx uint32, rho uint8) {
	if s.dense != nil {
		if rho > s.dense[idx] {
			s.dense[idx] = rho
		}
		return
	}
	// Sparse path.
	if cur, ok := s.sparse[idx]; !ok || rho > cur {
		s.sparse[idx] = rho
	}
	// Promote when sparse becomes more expensive than dense.
	// Each Go map entry is empirically 12-16 bytes for uint32→uint8 in
	// the runtime's hmap; we use 5 as a deliberately conservative
	// promotion threshold so we never use more memory than dense would.
	if uint32(len(s.sparse))*5 > s.m() {
		s.promote()
	}
}

// promote converts the sparse map into a dense register array.
func (s *Sketch) promote() {
	d := make([]uint8, s.m())
	for idx, rho := range s.sparse {
		d[idx] = rho
	}
	s.dense = d
	s.sparse = nil
}

// Estimate returns the estimated cardinality.
//
// Below the linear-counting threshold (V > 0 and E ≤ 5m/2) we use Whang
// et al.'s linear counting estimator, which is more accurate than HLL in
// that regime. Above it we use the classical HLL estimator.
func (s *Sketch) Estimate() uint64 {
	m := float64(s.m())

	var sum float64
	var zeros uint32

	if s.dense != nil {
		for _, r := range s.dense {
			sum += math.Ldexp(1, -int(r)) // 2^(-r)
			if r == 0 {
				zeros++
			}
		}
	} else {
		// Sparse: missing entries are zero registers.
		nonZero := uint32(len(s.sparse))
		zeros = s.m() - nonZero
		sum = float64(zeros) // each zero register contributes 2^0 = 1
		for _, r := range s.sparse {
			sum += math.Ldexp(1, -int(r))
		}
	}

	// HLL raw estimate.
	raw := alpha(s.m()) * m * m / sum

	// Linear-counting regime: when there are zero registers and raw is
	// small, the linear counting estimator V*ln(m/V) outperforms HLL.
	if zeros > 0 && raw <= 2.5*m {
		lc := m * math.Log(m/float64(zeros))
		return uint64(math.Round(lc))
	}

	return uint64(math.Round(raw))
}

// alpha returns the bias correction constant α_m from Flajolet et al.
// Closed form for m ≥ 128; tabulated values for the small precisions
// (used only by tests, not production).
func alpha(m uint32) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1.0 + 1.079/float64(m))
	}
}

// Merge folds o into s in place: register-wise max. Both sketches must
// have the same precision.
//
// Merge is associative and commutative, which is the property that makes
// HLL useful for distributed counting: shard the stream, sketch each
// shard independently, then Merge.
func (s *Sketch) Merge(o *Sketch) error {
	if s.p != o.p {
		return fmt.Errorf("hll: precision mismatch: %d vs %d", s.p, o.p)
	}

	// Promote s to dense if the merge will likely overflow sparse.
	if s.dense == nil {
		// Worst case: every entry in o is new. If that would push us over
		// the promotion threshold, promote eagerly to avoid repeated
		// rehashing during the merge.
		incoming := uint32(0)
		if o.dense != nil {
			for _, r := range o.dense {
				if r > 0 {
					incoming++
				}
			}
		} else {
			incoming = uint32(len(o.sparse))
		}
		if (uint32(len(s.sparse))+incoming)*5 > s.m() {
			s.promote()
		}
	}

	if s.dense != nil {
		if o.dense != nil {
			for i, r := range o.dense {
				if r > s.dense[i] {
					s.dense[i] = r
				}
			}
		} else {
			for idx, r := range o.sparse {
				if r > s.dense[idx] {
					s.dense[idx] = r
				}
			}
		}
		return nil
	}

	// Both sparse.
	if o.dense != nil {
		// o switched to dense before s; promote s and recurse.
		s.promote()
		return s.Merge(o)
	}
	for idx, r := range o.sparse {
		if cur, ok := s.sparse[idx]; !ok || r > cur {
			s.sparse[idx] = r
		}
	}
	return nil
}

// Reset clears the sketch back to empty without releasing the underlying
// buffer. Useful in hot paths where allocations matter.
func (s *Sketch) Reset() {
	if s.dense != nil {
		for i := range s.dense {
			s.dense[i] = 0
		}
		return
	}
	for k := range s.sparse {
		delete(s.sparse, k)
	}
}

// Clone returns an independent copy of s. Modifications to the returned
// sketch do not affect s.
func (s *Sketch) Clone() *Sketch {
	c := &Sketch{p: s.p}
	if s.dense != nil {
		c.dense = make([]uint8, len(s.dense))
		copy(c.dense, s.dense)
		return c
	}
	c.sparse = make(map[uint32]uint8, len(s.sparse))
	for k, v := range s.sparse {
		c.sparse[k] = v
	}
	return c
}

// On-disk format (little-endian):
//
//	[0]   magic: 'H'  ('H' == 0x48)
//	[1]   version: 1
//	[2]   precision (uint8)
//	[3]   mode: 0=sparse, 1=dense
//	dense: [4..4+m]  uint8 registers
//	sparse: [4..8] uint32 entry count, then count*(uint32 idx, uint8 rho)
//
// Magic and version let future formats be added without breaking readers.

const (
	magic      byte = 'H'
	version    byte = 1
	modeSparse byte = 0
	modeDense  byte = 1
)

const (
	headerLen      = 4
	sparseEntryLen = 5 // uint32 idx + uint8 rho
)

// MarshalBinary serialises the sketch.
func (s *Sketch) MarshalBinary() ([]byte, error) {
	var out []byte
	if s.dense != nil {
		out = make([]byte, headerLen+len(s.dense))
		out[0] = magic
		out[1] = version
		out[2] = s.p
		out[3] = modeDense
		copy(out[headerLen:], s.dense)
		return out, nil
	}
	out = make([]byte, headerLen+4+len(s.sparse)*sparseEntryLen)
	out[0] = magic
	out[1] = version
	out[2] = s.p
	out[3] = modeSparse
	binary.LittleEndian.PutUint32(out[headerLen:], uint32(len(s.sparse)))
	off := headerLen + 4
	for idx, rho := range s.sparse {
		binary.LittleEndian.PutUint32(out[off:], idx)
		out[off+4] = rho
		off += sparseEntryLen
	}
	return out, nil
}

// UnmarshalBinary deserialises into s. Any prior contents are discarded.
//
// Errors are returned for: short input, wrong magic, unknown version,
// out-of-range precision, malformed mode, size/precision mismatch, or
// out-of-range register indices/values. We never panic on bad data; HLL
// data may come from untrusted sources (cross-service merge) and a panic
// would be a denial-of-service vector.
func (s *Sketch) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen {
		return errors.New("hll: short header")
	}
	if data[0] != magic {
		return fmt.Errorf("hll: bad magic %#x, want %#x", data[0], magic)
	}
	if data[1] != version {
		return fmt.Errorf("hll: unsupported version %d", data[1])
	}
	p := data[2]
	if p < MinPrecision || p > MaxPrecision {
		return fmt.Errorf("hll: precision %d out of range", p)
	}
	mode := data[3]
	s.p = p
	s.sparse = nil
	s.dense = nil
	m := uint32(1) << p
	maxRho := 64 - p + 1

	switch mode {
	case modeDense:
		body := data[headerLen:]
		if uint32(len(body)) != m {
			return fmt.Errorf("hll: dense body %d bytes, want %d", len(body), m)
		}
		// Validate register values.
		for i, r := range body {
			if r > maxRho {
				return fmt.Errorf("hll: register[%d] = %d > max ρ %d", i, r, maxRho)
			}
		}
		s.dense = make([]uint8, m)
		copy(s.dense, body)
		return nil
	case modeSparse:
		if len(data) < headerLen+4 {
			return errors.New("hll: short sparse header")
		}
		n := binary.LittleEndian.Uint32(data[headerLen:])
		if n > m {
			return fmt.Errorf("hll: sparse count %d > register count %d", n, m)
		}
		bodyLen := uint64(n) * sparseEntryLen
		if uint64(len(data)) != uint64(headerLen)+4+bodyLen {
			return fmt.Errorf("hll: sparse body length mismatch")
		}
		s.sparse = make(map[uint32]uint8, n)
		off := headerLen + 4
		for i := uint32(0); i < n; i++ {
			idx := binary.LittleEndian.Uint32(data[off:])
			rho := data[off+4]
			off += sparseEntryLen
			if idx >= m {
				return fmt.Errorf("hll: register index %d ≥ m=%d", idx, m)
			}
			if rho == 0 || rho > maxRho {
				return fmt.Errorf("hll: register[%d] = %d outside (0, %d]", idx, rho, maxRho)
			}
			if _, dup := s.sparse[idx]; dup {
				return fmt.Errorf("hll: duplicate sparse index %d", idx)
			}
			s.sparse[idx] = rho
		}
		return nil
	default:
		return fmt.Errorf("hll: unknown mode %d", mode)
	}
}

// MemoryBytes returns the approximate heap footprint of the sketch's
// register storage. Useful for capacity planning.
func (s *Sketch) MemoryBytes() int {
	if s.dense != nil {
		return len(s.dense)
	}
	// Go map overhead per entry is implementation-defined; this is an
	// optimistic lower bound suitable for "is sparse cheaper?" decisions.
	return len(s.sparse) * sparseEntryLen
}
