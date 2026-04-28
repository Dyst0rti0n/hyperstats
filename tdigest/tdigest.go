package tdigest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Centroid represents a single (mean, weight) pair in the digest. Public
// for consumers who want to inspect or stream the digest contents.
type Centroid struct {
	Mean   float64
	Weight float64
}

// Sketch is a merging t-digest.
//
// A Sketch is not safe for concurrent use. Use per-goroutine sketches
// and Merge them to combine.
type Sketch struct {
	delta float64 // compression parameter; smaller = more compression
	// centroids is kept sorted by Mean between flushes. Within a flush
	// (Add appended) the buffer at the tail may be unsorted.
	centroids []Centroid
	// buffered counts how many of the trailing centroids are unsorted
	// raw inserts that need to be merged on next flush.
	buffered int
	// bufferLimit triggers a flush when |buffered| ≥ bufferLimit.
	bufferLimit int

	totalWeight float64
	minVal      float64
	maxVal      float64
}

const (
	// MinDelta is the smallest valid compression parameter. Values below
	// 10 give too few centroids to estimate the median accurately.
	MinDelta float64 = 10
	// DefaultDelta balances memory (~6×δ × 16 bytes ≈ 9.6 KiB at δ=100)
	// against accuracy (~1% rank error mid, well under 0.1% at p99).
	DefaultDelta float64 = 100
	// MaxDelta is a safety cap; nothing in the algorithm prevents
	// larger values, but δ > 100000 typically signals misconfiguration.
	MaxDelta float64 = 100_000
)

// New returns an empty Sketch with the given compression parameter δ.
// δ must be in [MinDelta, MaxDelta]; New panics otherwise.
func New(delta float64) *Sketch {
	if !(delta >= MinDelta && delta <= MaxDelta) {
		panic(fmt.Sprintf("tdigest: delta = %g out of range [%g, %g]",
			delta, MinDelta, MaxDelta))
	}
	// Buffer size: 5×δ is the canonical choice. Larger amortises sort
	// cost; smaller keeps memory tighter.
	const bufferMul = 5
	return &Sketch{
		delta:       delta,
		centroids:   make([]Centroid, 0, int(bufferMul*delta)),
		bufferLimit: int(bufferMul * delta),
		minVal:      math.Inf(1),
		maxVal:      math.Inf(-1),
	}
}

// Delta returns the compression parameter.
func (s *Sketch) Delta() float64 { return s.delta }

// TotalWeight returns the sum of weights of all observations added.
func (s *Sketch) TotalWeight() float64 { return s.totalWeight }

// CentroidCount returns the current number of centroids. After Flush this
// is bounded by approximately π δ / 2 ≈ 1.57 δ for unimodal data.
func (s *Sketch) CentroidCount() int {
	s.flush()
	return len(s.centroids)
}

// Min returns the smallest value ever observed, or +Inf if empty.
func (s *Sketch) Min() float64 { return s.minVal }

// Max returns the largest value ever observed, or -Inf if empty.
func (s *Sketch) Max() float64 { return s.maxVal }

// Add observes value x with weight 1. NaN inputs are silently ignored
// (NaN propagates through sort and arithmetic and would poison the digest).
func (s *Sketch) Add(x float64) {
	s.AddWeighted(x, 1)
}

// AddWeighted observes value x with arbitrary positive weight w.
func (s *Sketch) AddWeighted(x, w float64) {
	if math.IsNaN(x) || math.IsNaN(w) || w <= 0 {
		return
	}
	if x < s.minVal {
		s.minVal = x
	}
	if x > s.maxVal {
		s.maxVal = x
	}
	s.centroids = append(s.centroids, Centroid{Mean: x, Weight: w})
	s.buffered++
	s.totalWeight += w
	if s.buffered >= s.bufferLimit {
		s.flush()
	}
}

// flush sorts the full centroid list and merges adjacent centroids
// according to the k-space bound. Idempotent: if there are no buffered
// inserts, flush returns immediately.
func (s *Sketch) flush() {
	if s.buffered == 0 {
		return
	}
	// Sort by mean, with stable ordering on ties. Stable matters because
	// the merge step depends on adjacency.
	sort.SliceStable(s.centroids, func(i, j int) bool {
		return s.centroids[i].Mean < s.centroids[j].Mean
	})

	// Sweep left-to-right, greedily folding into the last accepted
	// centroid while the k-space bound permits.
	//
	// Critical detail: q1 anchors at the *start of the current
	// accepted centroid* and only advances when we accept a new one.
	// This keeps the per-centroid k-space budget bounded by 1, which
	// is what the t-digest paper proves bounds the centroid count by
	// O(δ).
	merged := s.centroids[:0:cap(s.centroids)]
	merged = append(merged, s.centroids[0])
	cumWeight := s.centroids[0].Weight
	startWeight := 0.0 // running weight before the current accepted centroid

	for i := 1; i < len(s.centroids); i++ {
		next := s.centroids[i]
		q1 := startWeight / s.totalWeight
		q2 := (cumWeight + next.Weight) / s.totalWeight
		// k-space distance bound (Dunning 2019, eq. 1).
		if kBound(q2, s.delta)-kBound(q1, s.delta) <= 1 {
			// Fold: weighted-mean update of the last centroid.
			last := &merged[len(merged)-1]
			combinedW := last.Weight + next.Weight
			last.Mean += (next.Mean - last.Mean) * next.Weight / combinedW
			last.Weight = combinedW
		} else {
			merged = append(merged, next)
			startWeight = cumWeight
		}
		cumWeight += next.Weight
	}

	s.centroids = merged
	s.buffered = 0
}

// kBound is the t-digest scale function k(q) = (δ / 2π) · arcsin(2q - 1).
// Its derivative is unbounded near q=0,1, which is what concentrates
// centroids in the tails.
func kBound(q, delta float64) float64 {
	// Clamp q into the open interval to avoid arcsin singularities.
	if q <= 0 {
		return -delta / 4 // arcsin(-1) = -π/2 → -δ/4
	}
	if q >= 1 {
		return delta / 4
	}
	return delta * math.Asin(2*q-1) / (2 * math.Pi)
}

// Quantile returns the estimated value at quantile q ∈ [0, 1]. Returns
// NaN for empty sketches and for q outside [0, 1].
//
// Algorithm: walk centroids accumulating weight; when the cumulative
// weight crosses q · TotalWeight, interpolate between adjacent centroid
// means proportionally to where in that centroid the target weight lies.
func (s *Sketch) Quantile(q float64) float64 {
	if s.totalWeight == 0 || math.IsNaN(q) || q < 0 || q > 1 {
		return math.NaN()
	}
	s.flush()
	if len(s.centroids) == 1 {
		return s.centroids[0].Mean
	}
	if q == 0 {
		return s.minVal
	}
	if q == 1 {
		return s.maxVal
	}

	target := q * s.totalWeight

	// First centroid: half its weight is "before" its mean, half "after".
	// Anchor the leading edge at minVal.
	cumLeft := 0.0
	for i, c := range s.centroids {
		cumRight := cumLeft + c.Weight
		// Define the centroid's "zone" as cumLeft .. cumRight in weight
		// space, with its mean at cumLeft + c.Weight/2.
		center := cumLeft + c.Weight/2

		if target < center {
			// Target falls between the previous centroid's center and this one.
			if i == 0 {
				// Linear interp between minVal and centroid[0].Mean over
				// [0, c.Weight/2].
				return interp(target, 0, c.Weight/2, s.minVal, c.Mean)
			}
			prev := s.centroids[i-1]
			prevCenter := cumLeft - prev.Weight/2
			return interp(target, prevCenter, center, prev.Mean, c.Mean)
		}
		// Target ≥ center; will be resolved on the next iteration unless
		// this is the last centroid.
		if i == len(s.centroids)-1 {
			// Linear interp from this centroid's center to maxVal over
			// [center, totalWeight].
			return interp(target, center, s.totalWeight, c.Mean, s.maxVal)
		}
		cumLeft = cumRight
	}
	// Unreachable, but be defensive.
	return s.centroids[len(s.centroids)-1].Mean
}

// Quantiles is a batched form of Quantile; same return semantics.
func (s *Sketch) Quantiles(qs []float64) []float64 {
	out := make([]float64, len(qs))
	for i, q := range qs {
		out[i] = s.Quantile(q)
	}
	return out
}

// Rank returns the estimated rank (i.e. quantile) of value x in [0, 1].
//
// Algorithm: scan centroids; cumulative weight at the first centroid
// whose mean exceeds x gives the lower bound; interpolate within the
// straddling centroid.
func (s *Sketch) Rank(x float64) float64 {
	if s.totalWeight == 0 {
		return 0
	}
	if math.IsNaN(x) {
		return math.NaN()
	}
	s.flush()
	if x < s.minVal {
		return 0
	}
	if x > s.maxVal {
		return 1
	}

	cumLeft := 0.0
	for i, c := range s.centroids {
		cumRight := cumLeft + c.Weight
		center := cumLeft + c.Weight/2

		// Decide whether x lies in this centroid's left half, right half,
		// or somewhere later.
		if x < c.Mean {
			if i == 0 {
				return interp(x, s.minVal, c.Mean, 0, center) / s.totalWeight
			}
			prev := s.centroids[i-1]
			prevCenter := cumLeft - prev.Weight/2
			return interp(x, prev.Mean, c.Mean, prevCenter, center) / s.totalWeight
		}
		if x == c.Mean {
			return center / s.totalWeight
		}
		// x > c.Mean: continue.
		cumLeft = cumRight
	}
	return 1
}

// interp linearly interpolates: at x=x0 returns y0, at x=x1 returns y1.
// If x0==x1, returns y0.
func interp(x, x0, x1, y0, y1 float64) float64 {
	if x1 == x0 {
		return y0
	}
	t := (x - x0) / (x1 - x0)
	return y0 + t*(y1-y0)
}

// MemoryBytes returns the approximate heap footprint of the centroid list.
func (s *Sketch) MemoryBytes() int { return cap(s.centroids) * 16 } // 2 × float64

// Centroids returns a copy of the current centroid list (after flush).
// Useful for diagnostics and visualisation.
func (s *Sketch) Centroids() []Centroid {
	s.flush()
	out := make([]Centroid, len(s.centroids))
	copy(out, s.centroids)
	return out
}

// Merge folds o into s in place. Both sketches must have the same δ.
//
// Strategy: append o's centroids into s's buffer, mark them buffered, and
// flush. Centroids from o keep their weights; the merge sweep handles the
// rest. The result is statistically equivalent to having processed the
// concatenated stream (modulo floating-point order-dependence that
// t-digest tolerates within its error budget).
func (s *Sketch) Merge(o *Sketch) error {
	if s.delta != o.delta {
		return fmt.Errorf("tdigest: delta mismatch: %g vs %g", s.delta, o.delta)
	}
	o.flush()
	if len(o.centroids) == 0 {
		return nil
	}
	for _, c := range o.centroids {
		s.centroids = append(s.centroids, c)
		s.buffered++
		s.totalWeight += c.Weight
	}
	if o.minVal < s.minVal {
		s.minVal = o.minVal
	}
	if o.maxVal > s.maxVal {
		s.maxVal = o.maxVal
	}
	s.flush()
	return nil
}

// Reset clears the sketch back to empty without releasing centroid memory.
func (s *Sketch) Reset() {
	s.centroids = s.centroids[:0]
	s.buffered = 0
	s.totalWeight = 0
	s.minVal = math.Inf(1)
	s.maxVal = math.Inf(-1)
}

// Clone returns an independent copy of s.
func (s *Sketch) Clone() *Sketch {
	c := &Sketch{
		delta:       s.delta,
		bufferLimit: s.bufferLimit,
		buffered:    s.buffered,
		totalWeight: s.totalWeight,
		minVal:      s.minVal,
		maxVal:      s.maxVal,
		centroids:   make([]Centroid, len(s.centroids), cap(s.centroids)),
	}
	copy(c.centroids, s.centroids)
	return c
}

// On-disk format (little-endian):
//
//	[0..2]   magic 'T','D','G'
//	[3]      version
//	[4..12]  delta (float64)
//	[12..20] totalWeight (float64)
//	[20..28] minVal (float64)
//	[28..36] maxVal (float64)
//	[36..40] centroid count (uint32)
//	[40..]   count × (mean float64, weight float64)

const formatVersion byte = 1

const (
	headerLen   = 40
	centroidLen = 16
)

var magic = [...]byte{'T', 'D', 'G'}

// MarshalBinary serialises the sketch (including any buffered inserts,
// after a flush so the on-disk form is canonical).
func (s *Sketch) MarshalBinary() ([]byte, error) {
	s.flush()
	out := make([]byte, headerLen+len(s.centroids)*centroidLen)
	copy(out[0:3], magic[:])
	out[3] = formatVersion
	binary.LittleEndian.PutUint64(out[4:], math.Float64bits(s.delta))
	binary.LittleEndian.PutUint64(out[12:], math.Float64bits(s.totalWeight))
	binary.LittleEndian.PutUint64(out[20:], math.Float64bits(s.minVal))
	binary.LittleEndian.PutUint64(out[28:], math.Float64bits(s.maxVal))
	binary.LittleEndian.PutUint32(out[36:], uint32(len(s.centroids)))
	off := headerLen
	for _, c := range s.centroids {
		binary.LittleEndian.PutUint64(out[off:], math.Float64bits(c.Mean))
		binary.LittleEndian.PutUint64(out[off+8:], math.Float64bits(c.Weight))
		off += centroidLen
	}
	return out, nil
}

// UnmarshalBinary deserialises into s. Any prior contents are discarded.
//
// Validates: magic, version, delta range, count sanity (must be ≤ 4×δ as
// safety bound), per-centroid weight positivity, monotonic mean order,
// and total-weight consistency. Never panics on bad data.
func (s *Sketch) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen {
		return errors.New("tdigest: short header")
	}
	if data[0] != magic[0] || data[1] != magic[1] || data[2] != magic[2] {
		return errors.New("tdigest: bad magic")
	}
	if data[3] != formatVersion {
		return fmt.Errorf("tdigest: unsupported version %d", data[3])
	}
	delta := math.Float64frombits(binary.LittleEndian.Uint64(data[4:]))
	if !(delta >= MinDelta && delta <= MaxDelta) {
		return fmt.Errorf("tdigest: delta=%g out of range", delta)
	}
	totalW := math.Float64frombits(binary.LittleEndian.Uint64(data[12:]))
	minV := math.Float64frombits(binary.LittleEndian.Uint64(data[20:]))
	maxV := math.Float64frombits(binary.LittleEndian.Uint64(data[28:]))
	count := binary.LittleEndian.Uint32(data[36:])
	// Safety cap: post-flush centroid count is bounded by ~π δ / 2 ≈ 1.57 δ
	// for typical inputs. Allow 4×δ as a loose envelope.
	if uint64(count) > uint64(math.Ceil(4*delta)) {
		return fmt.Errorf("tdigest: count=%d > 4*delta=%g", count, 4*delta)
	}
	expected := uint64(headerLen) + uint64(count)*centroidLen
	if uint64(len(data)) != expected {
		return fmt.Errorf("tdigest: body %d bytes, want %d", len(data), expected)
	}
	cs := make([]Centroid, count)
	off := headerLen
	var sum float64
	var prevMean float64 = math.Inf(-1)
	for i := uint32(0); i < count; i++ {
		m := math.Float64frombits(binary.LittleEndian.Uint64(data[off:]))
		w := math.Float64frombits(binary.LittleEndian.Uint64(data[off+8:]))
		off += centroidLen
		if math.IsNaN(m) || math.IsNaN(w) {
			return fmt.Errorf("tdigest: centroid[%d] NaN", i)
		}
		if w <= 0 {
			return fmt.Errorf("tdigest: centroid[%d] weight=%g must be > 0", i, w)
		}
		if m < prevMean {
			return fmt.Errorf("tdigest: centroid[%d] mean=%g < prev=%g (must be sorted)",
				i, m, prevMean)
		}
		cs[i] = Centroid{Mean: m, Weight: w}
		sum += w
		prevMean = m
	}
	const eps = 1e-9
	if math.Abs(sum-totalW) > eps*math.Max(1, math.Abs(totalW)) {
		return fmt.Errorf("tdigest: sum of centroid weights %g != stored total %g", sum, totalW)
	}
	if count > 0 && minV > cs[0].Mean {
		return fmt.Errorf("tdigest: minVal=%g > first centroid mean=%g", minV, cs[0].Mean)
	}
	if count > 0 && maxV < cs[count-1].Mean {
		return fmt.Errorf("tdigest: maxVal=%g < last centroid mean=%g", maxV, cs[count-1].Mean)
	}

	s.delta = delta
	s.totalWeight = totalW
	s.minVal = minV
	s.maxVal = maxV
	s.centroids = cs
	s.buffered = 0
	s.bufferLimit = int(5 * delta)
	return nil
}
