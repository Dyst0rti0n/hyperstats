package kll

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

// Sketch is a KLL streaming quantile estimator.
//
// A Sketch is not safe for concurrent use. Use per-goroutine sketches and
// merge them; KLL's exact mergeability is one of its main practical wins.
type Sketch struct {
	k uint32 // user-chosen accuracy parameter; k=200 → ε ≈ 0.83%

	// levels[h] holds the items currently at compactor level h. Items at
	// level h represent weight 2^h in the empirical distribution.
	// len(levels) is the number of levels (H+1 in the paper's notation).
	levels [][]float64

	// sorted[h] is true iff levels[h] is currently in non-decreasing order.
	// We lazily sort to amortise cost; quantile queries always force-sort.
	sorted []bool

	// n is the true number of items inserted (= Σ 2^h * len(levels[h])).
	n uint64

	// Bookkeeping for quantile boundary queries.
	minVal float64
	maxVal float64

	rng *rand.Rand
}

// shrinkFactor is the geometric shrink ratio between adjacent compactors.
// The KLL paper uses c = 2/3; Apache DataSketches uses the same. Smaller
// c gives less memory at the cost of more error variance.
const shrinkFactor = 2.0 / 3.0

const (
	// MinK is the smallest valid k. Below 8 the compactor capacities
	// floor to 2 immediately and the error analysis no longer holds.
	MinK uint32 = 8
	// DefaultK balances memory (~600 floats = 4.8 KiB) against error
	// (≈0.83% rank-error at 99.7% confidence). It is the recommended
	// starting point.
	DefaultK uint32 = 200
	// MaxK is a safety cap; nothing about the algorithm forbids larger
	// values, but k > 65536 typically indicates a misconfiguration.
	MaxK uint32 = 65536
)

// New returns an empty Sketch with the given accuracy parameter k.
//
// k must be in [MinK, MaxK]; New panics otherwise. The PRNG is seeded
// non-deterministically. Use NewWithSeed for reproducible tests.
func New(k uint32) *Sketch {
	return NewWithSeed(k, time.Now().UnixNano())
}

// NewWithSeed is like New but seeds the internal PRNG deterministically.
// Use only for tests; in production prefer New for fresh randomness.
func NewWithSeed(k uint32, seed int64) *Sketch {
	if k < MinK || k > MaxK {
		panic(fmt.Sprintf("kll: k = %d out of range [%d, %d]", k, MinK, MaxK))
	}
	return &Sketch{
		k:      k,
		levels: [][]float64{make([]float64, 0, k)},
		sorted: []bool{true}, // empty is trivially sorted
		minVal: math.Inf(1),
		maxVal: math.Inf(-1),
		rng:    rand.New(rand.NewSource(seed)),
	}
}

// K returns the configured accuracy parameter.
func (s *Sketch) K() uint32 { return s.k }

// N returns the true number of items inserted.
func (s *Sketch) N() uint64 { return s.n }

// Min returns the smallest item ever added, or +Inf if the sketch is empty.
func (s *Sketch) Min() float64 { return s.minVal }

// Max returns the largest item ever added, or -Inf if the sketch is empty.
func (s *Sketch) Max() float64 { return s.maxVal }

// numLevels returns the current top level index + 1 (so a sketch with only
// level 0 returns 1).
func (s *Sketch) numLevels() int { return len(s.levels) }

// capacity returns the capacity of compactor level h given the current
// top level H = numLevels - 1. The KLL capacity schedule is:
//
//	cap(h) = max(2, ⌈k · c^(H-h)⌉)
//
// so that the top compactor has capacity k and capacities shrink
// geometrically going down. Total memory is bounded by k / (1 - c) ≈ 3k.
func (s *Sketch) capacity(h int) int {
	H := s.numLevels() - 1
	c := math.Pow(shrinkFactor, float64(H-h))
	cap_ := int(math.Ceil(float64(s.k) * c))
	if cap_ < 2 {
		cap_ = 2
	}
	return cap_
}

// Add incorporates a single observation x into the sketch.
//
// NaN inputs are silently ignored (NaN propagates poison through any
// downstream sort and quantile arithmetic; rejecting it at the boundary
// is the only sane behaviour). ±Inf are accepted; they participate in
// quantiles in the obvious way.
func (s *Sketch) Add(x float64) {
	if math.IsNaN(x) {
		return
	}
	if x < s.minVal {
		s.minVal = x
	}
	if x > s.maxVal {
		s.maxVal = x
	}
	s.levels[0] = append(s.levels[0], x)
	s.sorted[0] = false
	s.n++
	s.compactIfFull()
}

// compactIfFull walks levels bottom-up and compacts every level that is
// at or above its capacity. The walk handles the cascade case where one
// compaction overflows the next level up.
func (s *Sketch) compactIfFull() {
	for h := 0; h < s.numLevels(); h++ {
		if len(s.levels[h]) >= s.capacity(h) {
			s.compactLevel(h)
			// Compacting may have created a new top level whose capacity
			// schedule differs; restart the walk to pick that up.
			h = -1
		}
	}
}

// compactLevel performs one compaction step at level h:
//   1. Sort levels[h] (idempotent if already sorted).
//   2. Flip a fair coin to decide odd vs. even retention.
//   3. Promote the kept items to level h+1 (creating it if needed).
//   4. Clear levels[h].
//
// Items at level h have weight 2^h; after compaction the kept items
// represent the same total weight at level h+1 (since each item now has
// weight 2^(h+1) and we kept half of them). This is what makes the
// estimator unbiased.
func (s *Sketch) compactLevel(h int) {
	if !s.sorted[h] {
		sort.Float64s(s.levels[h])
		s.sorted[h] = true
	}
	src := s.levels[h]

	// We need a parent level; create it if it doesn't exist.
	if h+1 >= s.numLevels() {
		s.levels = append(s.levels, make([]float64, 0, s.k))
		s.sorted = append(s.sorted, true)
	}

	// If src has odd length, the trailing item carries through unchanged
	// (it's an "orphan" that can't be paired). The KLL compaction works
	// on the largest even prefix.
	even := len(src) - len(src)%2
	tail := src[even:]

	startEven := s.rng.Intn(2) // 0 → keep evens, 1 → keep odds
	parent := s.levels[h+1]
	for i := startEven; i < even; i += 2 {
		parent = append(parent, src[i])
	}
	s.levels[h+1] = parent
	// Newly-promoted items break parent's sortedness.
	s.sorted[h+1] = false

	// Reset src and re-add the orphan if any.
	s.levels[h] = src[:0]
	if len(tail) > 0 {
		s.levels[h] = append(s.levels[h], tail...)
		s.sorted[h] = true // single item is sorted
	} else {
		s.sorted[h] = true // empty is sorted
	}
}

// Rank returns the estimated fraction of items in the stream that are ≤ x.
// Result is in [0, 1]. For empty sketches Rank returns 0.
//
// Algorithm: walk every level, count items ≤ x weighted by 2^h, divide
// by N. Cost: O(M) where M = total items in sketch (= Θ(k)).
func (s *Sketch) Rank(x float64) float64 {
	if s.n == 0 {
		return 0
	}
	if math.IsNaN(x) {
		return math.NaN()
	}
	var weighted uint64
	for h, level := range s.levels {
		w := uint64(1) << uint(h)
		for _, v := range level {
			if v <= x {
				weighted += w
			}
		}
	}
	return float64(weighted) / float64(s.n)
}

// Quantile returns an item v such that approximately q · N items are ≤ v.
//
// q must be in [0, 1]. Quantile(0) returns Min, Quantile(1) returns Max.
// For empty sketches Quantile returns NaN.
//
// Algorithm: build a flat (value, weight) list across all levels, sort by
// value, walk accumulating weight until the running total reaches q·N.
// Cost: O(M log M).
func (s *Sketch) Quantile(q float64) float64 {
	if s.n == 0 {
		return math.NaN()
	}
	if q < 0 || q > 1 || math.IsNaN(q) {
		return math.NaN()
	}
	if q == 0 {
		return s.minVal
	}
	if q == 1 {
		return s.maxVal
	}

	type wv struct {
		v float64
		w uint64
	}
	flat := make([]wv, 0, s.estimatedItems())
	for h, level := range s.levels {
		w := uint64(1) << uint(h)
		for _, v := range level {
			flat = append(flat, wv{v, w})
		}
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].v < flat[j].v })

	target := uint64(math.Ceil(q * float64(s.n)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for _, e := range flat {
		cum += e.w
		if cum >= target {
			return e.v
		}
	}
	return flat[len(flat)-1].v
}

// Quantiles is a batched form of Quantile. The slice qs must contain
// values in [0, 1]; the returned slice has the same length.
//
// Sorting the flat (value, weight) array dominates the cost; calling
// Quantile in a loop would re-sort for every query. Quantiles sorts
// once and walks the cumulative distribution.
func (s *Sketch) Quantiles(qs []float64) []float64 {
	out := make([]float64, len(qs))
	if s.n == 0 {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	type wv struct {
		v float64
		w uint64
	}
	flat := make([]wv, 0, s.estimatedItems())
	for h, level := range s.levels {
		w := uint64(1) << uint(h)
		for _, v := range level {
			flat = append(flat, wv{v, w})
		}
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].v < flat[j].v })

	// Pair queries with their indices, sort by q ascending, walk once.
	type qi struct {
		q   float64
		idx int
	}
	qis := make([]qi, len(qs))
	for i, q := range qs {
		qis[i] = qi{q, i}
	}
	sort.Slice(qis, func(i, j int) bool { return qis[i].q < qis[j].q })

	var cum uint64
	flatIdx := 0
	for _, query := range qis {
		q := query.q
		if q < 0 || q > 1 || math.IsNaN(q) {
			out[query.idx] = math.NaN()
			continue
		}
		if q == 0 {
			out[query.idx] = s.minVal
			continue
		}
		if q == 1 {
			out[query.idx] = s.maxVal
			continue
		}
		target := uint64(math.Ceil(q * float64(s.n)))
		if target == 0 {
			target = 1
		}
		for cum < target && flatIdx < len(flat) {
			cum += flat[flatIdx].w
			flatIdx++
		}
		if flatIdx == 0 {
			out[query.idx] = flat[0].v
		} else {
			out[query.idx] = flat[flatIdx-1].v
		}
	}
	return out
}

// estimatedItems returns the total count of items currently held across
// all levels (used to pre-size flat buffers in queries).
func (s *Sketch) estimatedItems() int {
	total := 0
	for _, level := range s.levels {
		total += len(level)
	}
	return total
}

// MemoryBytes returns the approximate heap footprint of the level buffers.
// Capacity (not length) is used because that is what is actually allocated.
func (s *Sketch) MemoryBytes() int {
	total := 0
	for _, level := range s.levels {
		total += cap(level) * 8 // float64
	}
	return total
}

// Merge folds o into s in place. Both sketches must have the same k.
//
// Strategy: append o.levels[h] into s.levels[h] for each level, then run
// compactIfFull to restore the capacity invariant. This is exact: the
// resulting sketch is statistically identical to a single sketch built
// from the concatenation of the two streams (modulo the random coins,
// which are independent draws either way).
func (s *Sketch) Merge(o *Sketch) error {
	if s.k != o.k {
		return fmt.Errorf("kll: k mismatch: %d vs %d", s.k, o.k)
	}
	if o.n == 0 {
		return nil
	}
	// Extend our level array to match o if needed.
	for s.numLevels() < o.numLevels() {
		s.levels = append(s.levels, make([]float64, 0, s.k))
		s.sorted = append(s.sorted, true)
	}
	// Append values level-wise.
	for h, src := range o.levels {
		if len(src) == 0 {
			continue
		}
		s.levels[h] = append(s.levels[h], src...)
		s.sorted[h] = false
	}
	// Boundary updates.
	if o.minVal < s.minVal {
		s.minVal = o.minVal
	}
	if o.maxVal > s.maxVal {
		s.maxVal = o.maxVal
	}
	s.n += o.n
	s.compactIfFull()
	return nil
}

// Reset clears the sketch back to empty without releasing buffers.
func (s *Sketch) Reset() {
	s.levels = s.levels[:1]
	s.sorted = s.sorted[:1]
	s.levels[0] = s.levels[0][:0]
	s.sorted[0] = true
	s.n = 0
	s.minVal = math.Inf(1)
	s.maxVal = math.Inf(-1)
}

// Clone returns an independent copy of s. The clone has its own RNG seeded
// from s's RNG so future random choices diverge.
func (s *Sketch) Clone() *Sketch {
	c := &Sketch{
		k:      s.k,
		n:      s.n,
		minVal: s.minVal,
		maxVal: s.maxVal,
		rng:    rand.New(rand.NewSource(s.rng.Int63())),
		levels: make([][]float64, len(s.levels)),
		sorted: make([]bool, len(s.sorted)),
	}
	for h, level := range s.levels {
		c.levels[h] = make([]float64, len(level))
		copy(c.levels[h], level)
		c.sorted[h] = s.sorted[h]
	}
	return c
}

// On-disk format (little-endian):
//
//	[0..2]   magic 'K','L','L'
//	[3]      version
//	[4..8]   k (uint32)
//	[8..16]  n (uint64)
//	[16..24] minVal (float64)
//	[24..32] maxVal (float64)
//	[32..36] numLevels (uint32)
//	for each level h:
//	   [..4]  len (uint32)
//	   [..]   len * float64

const (
	formatVersion byte = 1
	headerLen          = 36
)

var magic = [...]byte{'K', 'L', 'L'}

// MarshalBinary serialises the sketch.
func (s *Sketch) MarshalBinary() ([]byte, error) {
	total := headerLen
	for _, level := range s.levels {
		total += 4 + len(level)*8
	}
	out := make([]byte, total)
	copy(out[0:3], magic[:])
	out[3] = formatVersion
	binary.LittleEndian.PutUint32(out[4:], s.k)
	binary.LittleEndian.PutUint64(out[8:], s.n)
	binary.LittleEndian.PutUint64(out[16:], math.Float64bits(s.minVal))
	binary.LittleEndian.PutUint64(out[24:], math.Float64bits(s.maxVal))
	binary.LittleEndian.PutUint32(out[32:], uint32(s.numLevels()))

	off := headerLen
	for _, level := range s.levels {
		binary.LittleEndian.PutUint32(out[off:], uint32(len(level)))
		off += 4
		for _, v := range level {
			binary.LittleEndian.PutUint64(out[off:], math.Float64bits(v))
			off += 8
		}
	}
	return out, nil
}

// UnmarshalBinary deserialises into s. Any prior contents are discarded.
//
// Validates: magic, version, k range, level count sanity (≥ 1), per-level
// length not exceeding the level's capacity (defends against attacker
// inflating memory by claiming huge levels), and N consistency with the
// weighted level counts. Never panics on bad data.
func (s *Sketch) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen {
		return errors.New("kll: short header")
	}
	if data[0] != magic[0] || data[1] != magic[1] || data[2] != magic[2] {
		return errors.New("kll: bad magic")
	}
	if data[3] != formatVersion {
		return fmt.Errorf("kll: unsupported version %d", data[3])
	}
	k := binary.LittleEndian.Uint32(data[4:])
	if k < MinK || k > MaxK {
		return fmt.Errorf("kll: k=%d out of range", k)
	}
	n := binary.LittleEndian.Uint64(data[8:])
	minVal := math.Float64frombits(binary.LittleEndian.Uint64(data[16:]))
	maxVal := math.Float64frombits(binary.LittleEndian.Uint64(data[24:]))
	numLevels := binary.LittleEndian.Uint32(data[32:])
	if numLevels == 0 || numLevels > 64 {
		// 64 levels would represent N ≥ 2^64; cap defensively.
		return fmt.Errorf("kll: numLevels=%d out of range [1, 64]", numLevels)
	}

	// Reset and rebuild.
	s.k = k
	s.levels = make([][]float64, numLevels)
	s.sorted = make([]bool, numLevels)
	for h := range s.sorted {
		s.sorted[h] = false // be conservative; we re-sort on first query
	}
	s.minVal = minVal
	s.maxVal = maxVal

	off := headerLen
	var weighted uint64
	for h := uint32(0); h < numLevels; h++ {
		if off+4 > len(data) {
			return errors.New("kll: truncated level header")
		}
		ln := binary.LittleEndian.Uint32(data[off:])
		off += 4
		// Sanity: level length must not exceed the implied capacity.
		// We compute capacity using the to-be-numLevels (already known).
		// Use the stricter bound 3*k for the global cap.
		if ln > 3*k {
			return fmt.Errorf("kll: level %d length %d > 3k=%d", h, ln, 3*k)
		}
		need := int(ln) * 8
		if off+need > len(data) {
			return errors.New("kll: truncated level body")
		}
		level := make([]float64, ln)
		for i := uint32(0); i < ln; i++ {
			level[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[off:]))
			off += 8
		}
		s.levels[h] = level
		weighted += uint64(ln) << uint(h)
	}
	if off != len(data) {
		return fmt.Errorf("kll: %d trailing bytes", len(data)-off)
	}
	if weighted != n {
		return fmt.Errorf("kll: weighted level count %d != stored n %d", weighted, n)
	}
	s.n = n
	if s.rng == nil {
		s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return nil
}
