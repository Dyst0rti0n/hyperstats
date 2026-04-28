package tdigest

import (
	"bytes"
	"math"
	"sort"
	"testing"
)

func TestNewValidatesDelta(t *testing.T) {
	for _, d := range []float64{MinDelta, 50, DefaultDelta, 1000, MaxDelta} {
		s := New(d)
		if s.Delta() != d {
			t.Errorf("Delta() = %g, want %g", s.Delta(), d)
		}
	}
	for _, bad := range []float64{0, 1, MinDelta - 0.1, MaxDelta + 1, math.NaN(), math.Inf(1)} {
		assertPanics(t, "bad delta", func() { New(bad) })
	}
}

func TestEmptySketch(t *testing.T) {
	s := New(DefaultDelta)
	if s.TotalWeight() != 0 {
		t.Errorf("empty TotalWeight = %g, want 0", s.TotalWeight())
	}
	if !math.IsNaN(s.Quantile(0.5)) {
		t.Errorf("empty Quantile = %g, want NaN", s.Quantile(0.5))
	}
	if r := s.Rank(0); r != 0 {
		t.Errorf("empty Rank = %g, want 0", r)
	}
	if !math.IsInf(s.Min(), 1) {
		t.Errorf("empty Min = %g, want +Inf", s.Min())
	}
	if !math.IsInf(s.Max(), -1) {
		t.Errorf("empty Max = %g, want -Inf", s.Max())
	}
}

func TestSingleElement(t *testing.T) {
	s := New(DefaultDelta)
	s.Add(42)
	if s.TotalWeight() != 1 {
		t.Errorf("after 1 Add: TotalWeight = %g, want 1", s.TotalWeight())
	}
	if s.Quantile(0.5) != 42 {
		t.Errorf("Quantile(0.5) of {42} = %g, want 42", s.Quantile(0.5))
	}
	if s.Min() != 42 || s.Max() != 42 {
		t.Errorf("Min/Max = %g/%g, want 42/42", s.Min(), s.Max())
	}
}

func TestNaNAndNonpositiveWeightIgnored(t *testing.T) {
	s := New(DefaultDelta)
	s.Add(math.NaN())
	s.AddWeighted(1.0, math.NaN())
	s.AddWeighted(1.0, 0)
	s.AddWeighted(1.0, -1)
	if s.TotalWeight() != 0 {
		t.Errorf("TotalWeight = %g, want 0", s.TotalWeight())
	}
}

func TestExactSmallSample(t *testing.T) {
	// With δ=100 and only 5 points, no compression should happen and
	// quantiles should be very close to the nearest input.
	s := New(100)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		s.Add(v)
	}
	if got := s.Quantile(0); got != 1 {
		t.Errorf("Quantile(0) = %g, want 1", got)
	}
	if got := s.Quantile(1); got != 5 {
		t.Errorf("Quantile(1) = %g, want 5", got)
	}
	mid := s.Quantile(0.5)
	if mid < 2.5 || mid > 3.5 {
		t.Errorf("Quantile(0.5) = %g, want in [2.5, 3.5]", mid)
	}
}

// TestCompressionBoundsCentroids: the t-digest scale function bounds the
// centroid count by O(δ). After many inserts, we should see far fewer
// centroids than inserts.
func TestCompressionBoundsCentroids(t *testing.T) {
	const N = 100_000
	s := New(100)
	for i := 0; i < N; i++ {
		s.Add(float64(i))
	}
	c := s.CentroidCount()
	// Theoretical asymptote is ~π δ / 2 ≈ 157. Allow up to 4×δ=400.
	if c > 400 {
		t.Errorf("centroid count %d exceeds 4×δ=400 after %d inserts", c, N)
	}
	t.Logf("after %d inserts at δ=100: %d centroids", N, c)
}

func TestQuantileMonotonic(t *testing.T) {
	s := New(DefaultDelta)
	for i := 0; i < 10000; i++ {
		s.Add(float64(i))
	}
	prev := math.Inf(-1)
	for q := 0.0; q <= 1.0; q += 0.01 {
		v := s.Quantile(q)
		if v < prev-1e-9 {
			t.Errorf("Quantile non-monotonic at q=%g: got %g, prev %g", q, v, prev)
		}
		prev = v
	}
}

func TestQuantileRankRoundtrip(t *testing.T) {
	s := New(DefaultDelta)
	for i := 0; i < 10000; i++ {
		s.Add(float64(i) * 0.1)
	}
	// For each q, Quantile then Rank should round-trip approximately.
	for _, q := range []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99} {
		v := s.Quantile(q)
		r := s.Rank(v)
		if math.Abs(r-q) > 0.02 {
			t.Errorf("Quantile→Rank: q=%g → v=%g → r=%g (drift %g)", q, v, r, r-q)
		}
	}
}

func TestMergeIsApproximatelyExact(t *testing.T) {
	a := New(DefaultDelta)
	b := New(DefaultDelta)
	ref := New(DefaultDelta)
	for i := 0; i < 5000; i++ {
		a.Add(float64(i))
		ref.Add(float64(i))
	}
	for i := 5000; i < 10000; i++ {
		b.Add(float64(i))
		ref.Add(float64(i))
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.TotalWeight()-ref.TotalWeight()) > 1e-9 {
		t.Errorf("merged TotalWeight = %g, ref = %g", a.TotalWeight(), ref.TotalWeight())
	}
	for _, q := range []float64{0.1, 0.5, 0.9, 0.99} {
		mq := a.Quantile(q)
		rq := ref.Quantile(q)
		// Allow ~1% relative error (matches t-digest's accuracy budget).
		if math.Abs(mq-rq)/math.Max(1, math.Abs(rq)) > 0.02 {
			t.Errorf("Quantile(%g): merged=%g vs ref=%g (rel err %g)",
				q, mq, rq, math.Abs(mq-rq)/math.Max(1, math.Abs(rq)))
		}
	}
}

func TestMergeRejectsMismatchedDelta(t *testing.T) {
	a := New(50)
	b := New(100)
	if err := a.Merge(b); err == nil {
		t.Fatal("expected delta-mismatch error")
	}
}

func TestRoundTrip(t *testing.T) {
	s := New(DefaultDelta)
	for i := 0; i < 10_000; i++ {
		s.Add(math.Sin(float64(i) * 0.01))
	}
	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var t2 Sketch
	if err := t2.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if s.Delta() != t2.Delta() {
		t.Errorf("delta differs: %g vs %g", s.Delta(), t2.Delta())
	}
	if s.TotalWeight() != t2.TotalWeight() {
		t.Errorf("totalWeight differs: %g vs %g", s.TotalWeight(), t2.TotalWeight())
	}
	for _, q := range []float64{0.01, 0.5, 0.99} {
		a := s.Quantile(q)
		b := t2.Quantile(q)
		if a != b {
			t.Errorf("Quantile(%g): %g vs %g after round trip", q, a, b)
		}
	}
}

func TestUnmarshalRejectsCorrupt(t *testing.T) {
	good := New(DefaultDelta)
	for i := 0; i < 1000; i++ {
		good.Add(float64(i))
	}
	data, _ := good.MarshalBinary()

	cases := []struct {
		name string
		fn   func([]byte) []byte
	}{
		{"empty", func(b []byte) []byte { return nil }},
		{"truncated header", func(b []byte) []byte { return b[:5] }},
		{"bad magic", func(b []byte) []byte { c := append([]byte{}, b...); c[0] = 'X'; return c }},
		{"bad version", func(b []byte) []byte { c := append([]byte{}, b...); c[3] = 99; return c }},
		{"truncated body", func(b []byte) []byte { return b[:headerLen+8] }},
		{"weight zero", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// Zero out the first centroid's weight.
			putFloat64Le(c[headerLen+8:], 0)
			return c
		}},
		{"unsorted centroids", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// Swap means of first two centroids — tests monotonicity check.
			if len(c) >= headerLen+2*centroidLen {
				for i := 0; i < 8; i++ {
					c[headerLen+i], c[headerLen+centroidLen+i] = c[headerLen+centroidLen+i], c[headerLen+i]
				}
			}
			return c
		}},
		{"weight sum mismatch", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// Inflate stored totalWeight by 100.
			cur := math.Float64frombits(uint64Le(c[12:]))
			putFloat64Le(c[12:], cur+100)
			return c
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Sketch
			if err := s.UnmarshalBinary(tc.fn(data)); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestResetAndClone(t *testing.T) {
	s := New(DefaultDelta)
	for i := 0; i < 1000; i++ {
		s.Add(float64(i))
	}
	c := s.Clone()
	s.Reset()
	if s.TotalWeight() != 0 {
		t.Errorf("Reset left TotalWeight = %g", s.TotalWeight())
	}
	if c.TotalWeight() != 1000 {
		t.Errorf("clone TotalWeight = %g, want 1000", c.TotalWeight())
	}
}

// TestSortedAdjacentMeans: post-flush, centroid means must be in
// non-decreasing order. Used by Quantile/Rank for correctness.
func TestSortedAdjacentMeans(t *testing.T) {
	s := New(DefaultDelta)
	for i := 0; i < 50_000; i++ {
		s.Add(math.Mod(float64(i)*0.137, 1.0)) // pseudo-random fill
	}
	cs := s.Centroids()
	if !sort.SliceIsSorted(cs, func(i, j int) bool { return cs[i].Mean < cs[j].Mean }) {
		t.Error("centroids out of order after flush")
	}
}

func assertPanics(t *testing.T, label string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: expected panic", label)
		}
	}()
	f()
}

// Helpers for the corruption test above.

func uint64Le(b []byte) uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(b[i]) << (8 * i)
	}
	return v
}

func putFloat64Le(b []byte, v float64) {
	bits := math.Float64bits(v)
	for i := 0; i < 8; i++ {
		b[i] = byte(bits >> (8 * i))
	}
}

var _ = bytes.Equal // keep "bytes" import in case future tests need it
