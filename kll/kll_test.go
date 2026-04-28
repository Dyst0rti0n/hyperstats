package kll

import (
	"bytes"
	"math"
	"sort"
	"testing"
)

func TestNewValidatesK(t *testing.T) {
	for _, k := range []uint32{MinK, 100, DefaultK, 1000, MaxK} {
		s := New(k)
		if s.K() != k {
			t.Errorf("K() = %d, want %d", s.K(), k)
		}
	}
	for _, bad := range []uint32{0, 1, MinK - 1, MaxK + 1} {
		assertPanics(t, "bad k", func() { New(bad) })
	}
}

func TestEmptySketch(t *testing.T) {
	s := New(DefaultK)
	if s.N() != 0 {
		t.Errorf("N() = %d, want 0", s.N())
	}
	if r := s.Rank(0); r != 0 {
		t.Errorf("Rank(0) on empty = %f, want 0", r)
	}
	if q := s.Quantile(0.5); !math.IsNaN(q) {
		t.Errorf("Quantile(0.5) on empty = %f, want NaN", q)
	}
	if !math.IsInf(s.Min(), 1) {
		t.Errorf("Min on empty = %f, want +Inf", s.Min())
	}
	if !math.IsInf(s.Max(), -1) {
		t.Errorf("Max on empty = %f, want -Inf", s.Max())
	}
}

func TestSingleElement(t *testing.T) {
	s := NewWithSeed(DefaultK, 1)
	s.Add(42.0)
	if s.N() != 1 {
		t.Errorf("N() = %d, want 1", s.N())
	}
	if s.Min() != 42 || s.Max() != 42 {
		t.Errorf("Min/Max = %f/%f, want 42/42", s.Min(), s.Max())
	}
	if r := s.Rank(42); r != 1.0 {
		t.Errorf("Rank(42) = %f, want 1.0", r)
	}
	if q := s.Quantile(0.5); q != 42 {
		t.Errorf("Quantile(0.5) = %f, want 42", q)
	}
}

func TestNaNIsIgnored(t *testing.T) {
	s := NewWithSeed(DefaultK, 1)
	s.Add(math.NaN())
	if s.N() != 0 {
		t.Errorf("NaN should be ignored, got N = %d", s.N())
	}
	s.Add(1.0)
	s.Add(math.NaN())
	s.Add(2.0)
	if s.N() != 2 {
		t.Errorf("N = %d after 2 valid + 2 NaN, want 2", s.N())
	}
}

// TestSortedDataExactness: with k≥N, no compaction happens and KLL becomes
// an exact sketch. This catches off-by-one and weight-tracking bugs.
func TestSortedDataExactness(t *testing.T) {
	const N = 100
	s := NewWithSeed(200, 1) // k=200, N=100 → no compaction
	for i := 1; i <= N; i++ {
		s.Add(float64(i))
	}
	if s.numLevels() != 1 {
		t.Fatalf("expected single level for non-overflow, got %d levels", s.numLevels())
	}
	// Rank should be exactly i/N for input i.
	for i := 1; i <= N; i++ {
		want := float64(i) / float64(N)
		got := s.Rank(float64(i))
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("Rank(%d) = %f, want %f (no-compaction regime should be exact)",
				i, got, want)
		}
	}
}

func TestQuantileMonotonic(t *testing.T) {
	s := NewWithSeed(DefaultK, 1)
	for i := 0; i < 10000; i++ {
		s.Add(float64(i))
	}
	prev := math.Inf(-1)
	for q := 0.0; q <= 1.0; q += 0.01 {
		v := s.Quantile(q)
		if v < prev {
			t.Errorf("Quantile non-monotonic at q=%f: got %f, prev %f", q, v, prev)
		}
		prev = v
	}
}

func TestQuantilesBatchMatchesSequential(t *testing.T) {
	s := NewWithSeed(DefaultK, 42)
	for i := 0; i < 5000; i++ {
		s.Add(float64(i))
	}
	qs := []float64{0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99}
	batch := s.Quantiles(qs)
	for i, q := range qs {
		seq := s.Quantile(q)
		if batch[i] != seq {
			t.Errorf("Quantiles[%d]=%f differs from Quantile(%f)=%f", i, batch[i], q, seq)
		}
	}
}

func TestCompactionTriggers(t *testing.T) {
	// k = MinK so compaction triggers fast.
	s := NewWithSeed(MinK, 1)
	for i := 0; i < 1000; i++ {
		s.Add(float64(i))
	}
	if s.numLevels() < 2 {
		t.Errorf("expected ≥2 levels after 1000 inserts at k=MinK, got %d", s.numLevels())
	}
	// N must equal the weighted sum.
	var weighted uint64
	for h, level := range s.levels {
		weighted += uint64(len(level)) << uint(h)
	}
	if weighted != s.N() {
		t.Errorf("weighted level sum %d != N %d", weighted, s.N())
	}
}

func TestMergeIsExact(t *testing.T) {
	const k = DefaultK
	// Build two sketches and a reference sketch from concatenated stream.
	a := NewWithSeed(k, 7)
	b := NewWithSeed(k, 13)
	ref := NewWithSeed(k, 7) // same seed as a, so first half is identical
	for i := 0; i < 1000; i++ {
		x := float64(i)
		a.Add(x)
		ref.Add(x)
	}
	for i := 1000; i < 2000; i++ {
		x := float64(i)
		b.Add(x)
		ref.Add(x)
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if a.N() != ref.N() {
		t.Errorf("merged N=%d, ref N=%d", a.N(), ref.N())
	}
	// Quantile estimates should be within 5% of each other (different
	// random coins lead to different sample paths).
	for _, q := range []float64{0.1, 0.5, 0.9} {
		mq := a.Quantile(q)
		rq := ref.Quantile(q)
		diff := math.Abs(mq-rq) / 2000
		if diff > 0.05 {
			t.Errorf("merged vs ref disagree at q=%f: %f vs %f (rank diff %.4f)",
				q, mq, rq, diff)
		}
	}
}

func TestMergeRejectsMismatchedK(t *testing.T) {
	a := NewWithSeed(100, 1)
	b := NewWithSeed(200, 1)
	if err := a.Merge(b); err == nil {
		t.Fatal("expected k-mismatch error")
	}
}

func TestRoundTrip(t *testing.T) {
	s := NewWithSeed(DefaultK, 1)
	for i := 0; i < 10000; i++ {
		s.Add(float64(i) * 1.5)
	}
	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var t2 Sketch
	if err := t2.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if s.K() != t2.K() || s.N() != t2.N() {
		t.Errorf("k/n mismatch: %d/%d vs %d/%d", s.K(), s.N(), t2.K(), t2.N())
	}
	if s.Min() != t2.Min() || s.Max() != t2.Max() {
		t.Errorf("min/max mismatch")
	}
	for _, q := range []float64{0.01, 0.5, 0.99} {
		a := s.Quantile(q)
		b := t2.Quantile(q)
		if a != b {
			t.Errorf("Quantile(%f): %f vs %f after round trip", q, a, b)
		}
	}
}

func TestUnmarshalRejectsCorrupt(t *testing.T) {
	good := NewWithSeed(DefaultK, 1)
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
		{"bad k", func(b []byte) []byte {
			c := append([]byte{}, b...)
			c[4], c[5], c[6], c[7] = 0, 0, 0, 0
			return c
		}},
		{"truncated level body", func(b []byte) []byte { return b[:headerLen+10] }},
		{"n inconsistent with levels", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// Inflate stored n by 1 — should fail the weighted-sum check.
			c[8]++
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
	s := NewWithSeed(DefaultK, 1)
	for i := 0; i < 1000; i++ {
		s.Add(float64(i))
	}

	c := s.Clone()
	s.Reset()
	if s.N() != 0 {
		t.Errorf("Reset left N=%d", s.N())
	}
	if c.N() != 1000 {
		t.Errorf("clone N=%d, want 1000 (Reset of original should not affect clone)", c.N())
	}
}

func TestMemoryStaysBounded(t *testing.T) {
	const k = DefaultK
	s := NewWithSeed(k, 1)
	for i := 0; i < 1_000_000; i++ {
		s.Add(float64(i))
	}
	mem := s.MemoryBytes()
	// Theoretical bound is roughly k / (1-c) = 3k items × 8 bytes.
	// Allow generous slack for buffer overallocation in append.
	if mem > 64*int(k)*8 {
		t.Errorf("memory %d bytes exceeds 64×k×8 = %d", mem, 64*int(k)*8)
	}
	t.Logf("after 1M inserts at k=%d: %d levels, %d bytes (~%.1fx k)",
		k, s.numLevels(), mem, float64(mem)/float64(k*8))
}

// TestCompactionDoesNotChangeMinMax: Min/Max are book-kept directly and
// must never drift through compaction (which discards items).
func TestCompactionDoesNotChangeMinMax(t *testing.T) {
	s := NewWithSeed(MinK, 1)
	values := []float64{}
	for i := 0; i < 5000; i++ {
		v := float64(i%97) + 0.5
		s.Add(v)
		values = append(values, v)
	}
	sort.Float64s(values)
	if s.Min() != values[0] {
		t.Errorf("Min=%f, want %f", s.Min(), values[0])
	}
	if s.Max() != values[len(values)-1] {
		t.Errorf("Max=%f, want %f", s.Max(), values[len(values)-1])
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

// Force the unused-imports linter to leave bytes alone for byte-compare
// patterns we may want later.
var _ = bytes.Equal
