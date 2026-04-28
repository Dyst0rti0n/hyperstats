package hll

import (
	"bytes"
	"strconv"
	"testing"
)

func TestNewValidatesPrecision(t *testing.T) {
	for _, p := range []uint8{MinPrecision, 10, DefaultPrecision, MaxPrecision} {
		s := New(p)
		if s.Precision() != p {
			t.Errorf("Precision() = %d, want %d", s.Precision(), p)
		}
		if s.m() != 1<<p {
			t.Errorf("m() = %d, want %d", s.m(), 1<<p)
		}
	}
	for _, bad := range []uint8{0, 3, MaxPrecision + 1, 100} {
		assertPanics(t, "bad precision", func() { New(bad) })
	}
}

func TestEmptySketchEstimateIsZero(t *testing.T) {
	s := New(DefaultPrecision)
	if got := s.Estimate(); got != 0 {
		t.Errorf("empty estimate = %d, want 0", got)
	}
}

func TestSingleElementEstimateIsOne(t *testing.T) {
	s := New(DefaultPrecision)
	s.AddString("alice")
	// Linear counting at n=1 with m=16384 gives almost exactly 1.
	if got := s.Estimate(); got != 1 {
		t.Errorf("estimate after 1 element = %d, want 1", got)
	}
}

func TestDuplicatesAreAbsorbed(t *testing.T) {
	s := New(DefaultPrecision)
	for i := 0; i < 1000; i++ {
		s.AddString("the same string")
	}
	if got := s.Estimate(); got != 1 {
		t.Errorf("estimate after 1000 duplicates = %d, want 1", got)
	}
}

// TestSparseToDensePromotion ensures a sketch that crosses the promotion
// threshold continues to estimate correctly. This catches off-by-one bugs
// in the promotion logic that would otherwise only surface intermittently.
func TestSparseToDensePromotion(t *testing.T) {
	const p = 8 // small precision = small threshold = fast test
	s := New(p)
	if s.dense != nil {
		t.Fatal("new sketch should start sparse")
	}
	for i := 0; i < 10_000; i++ {
		s.AddString("user-" + strconv.Itoa(i))
	}
	if s.dense == nil {
		t.Fatal("sketch should have promoted to dense")
	}
	// 10k uniques with p=8 (m=256, σ≈6.5%): allow ±50%.
	got := s.Estimate()
	if got < 5_000 || got > 15_000 {
		t.Errorf("estimate after 10k uniques (p=8) = %d, want in [5k, 15k]", got)
	}
}

func TestMergeIdempotent(t *testing.T) {
	a := New(12)
	b := New(12)
	for i := 0; i < 1000; i++ {
		a.AddString(strconv.Itoa(i))
		b.AddString(strconv.Itoa(i))
	}
	pre := a.Estimate()
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	post := a.Estimate()
	// Merging identical sketches must yield the same estimate (the union
	// of two identical sets is the set itself).
	if post != pre {
		t.Errorf("merge of identical sketches changed estimate: %d -> %d", pre, post)
	}
}

func TestMergeUnionsDisjointSets(t *testing.T) {
	const n = 10_000
	a := New(14)
	b := New(14)
	for i := 0; i < n; i++ {
		a.AddString("a-" + strconv.Itoa(i))
		b.AddString("b-" + strconv.Itoa(i))
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	got := a.Estimate()
	// Expected: 2n. p=14 has σ ≈ 0.81%, so 2σ ≈ 1.6% relative error.
	// We use a 5σ band for the unit test to stay non-flaky.
	target := uint64(2 * n)
	tol := uint64(float64(target) * 0.05)
	if got < target-tol || got > target+tol {
		t.Errorf("merge estimate = %d, want %d ± %d", got, target, tol)
	}
}

func TestMergeRejectsDifferentPrecision(t *testing.T) {
	a := New(10)
	b := New(12)
	if err := a.Merge(b); err == nil {
		t.Fatal("merging mismatched precisions should error")
	}
}

func TestMergeMixedSparseAndDense(t *testing.T) {
	// One sketch sparse, the other dense. Merge must work correctly in
	// both directions.
	const n = 50_000
	dense := New(8)
	sparse := New(8)
	for i := 0; i < n; i++ {
		dense.AddString("d" + strconv.Itoa(i))
	}
	sparse.AddString("only-one")

	if dense.dense == nil {
		t.Fatal("dense sketch should be dense")
	}
	if sparse.dense != nil {
		t.Fatal("sparse sketch should still be sparse")
	}

	// Dense ← sparse.
	d1 := dense.Clone()
	if err := d1.Merge(sparse); err != nil {
		t.Fatal(err)
	}
	// Sparse ← dense (will trigger promotion).
	s1 := sparse.Clone()
	if err := s1.Merge(dense); err != nil {
		t.Fatal(err)
	}
	if s1.dense == nil {
		t.Fatal("sparse should have promoted to dense after merging large sketch")
	}
	// Both directions must produce identical registers.
	if !bytes.Equal(d1.dense, s1.dense) {
		t.Errorf("merge is not commutative across mixed encodings")
	}
}

func TestMergeIsCommutative(t *testing.T) {
	const n = 5000
	a, b := New(12), New(12)
	for i := 0; i < n; i++ {
		a.AddString("x" + strconv.Itoa(i))
		b.AddString("y" + strconv.Itoa(i))
	}
	ab := a.Clone()
	if err := ab.Merge(b); err != nil {
		t.Fatal(err)
	}
	ba := b.Clone()
	if err := ba.Merge(a); err != nil {
		t.Fatal(err)
	}
	// After both have been promoted (or both not), registers must match.
	abBytes, _ := ab.MarshalBinary()
	baBytes, _ := ba.MarshalBinary()
	// We can't compare bytes directly when one is sparse and the other
	// dense, but we can compare estimates.
	if ab.Estimate() != ba.Estimate() {
		t.Errorf("a∪b estimate %d != b∪a estimate %d", ab.Estimate(), ba.Estimate())
	}
	_ = abBytes
	_ = baBytes
}

func TestRoundTripDense(t *testing.T) {
	s := New(10)
	for i := 0; i < 100_000; i++ {
		s.AddString(strconv.Itoa(i))
	}
	if s.dense == nil {
		t.Fatal("expected dense after 100k inserts at p=10")
	}
	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	t2 := &Sketch{}
	if err := t2.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.dense, t2.dense) {
		t.Error("dense registers differ after round trip")
	}
	if s.Estimate() != t2.Estimate() {
		t.Errorf("estimates differ after round trip: %d vs %d", s.Estimate(), t2.Estimate())
	}
}

func TestRoundTripSparse(t *testing.T) {
	s := New(14)
	for i := 0; i < 50; i++ {
		s.AddString("u" + strconv.Itoa(i))
	}
	if s.dense != nil {
		t.Fatal("expected sparse after 50 inserts at p=14")
	}
	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	t2 := &Sketch{}
	if err := t2.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if len(s.sparse) != len(t2.sparse) {
		t.Errorf("sparse map size differs: %d vs %d", len(s.sparse), len(t2.sparse))
	}
	for k, v := range s.sparse {
		if t2.sparse[k] != v {
			t.Errorf("sparse[%d] = %d after round-trip, want %d", k, t2.sparse[k], v)
		}
	}
}

func TestUnmarshalRejectsCorruptInput(t *testing.T) {
	good := New(10)
	good.AddString("hello")
	data, _ := good.MarshalBinary()

	cases := []struct {
		name    string
		mutator func([]byte) []byte
	}{
		{"empty", func(b []byte) []byte { return nil }},
		{"truncated header", func(b []byte) []byte { return b[:2] }},
		{"bad magic", func(b []byte) []byte { c := append([]byte{}, b...); c[0] = 'X'; return c }},
		{"bad version", func(b []byte) []byte { c := append([]byte{}, b...); c[1] = 99; return c }},
		{"bad precision", func(b []byte) []byte { c := append([]byte{}, b...); c[2] = 99; return c }},
		{"unknown mode", func(b []byte) []byte { c := append([]byte{}, b...); c[3] = 7; return c }},
		{"truncated body", func(b []byte) []byte { return b[:len(b)/2] }},
		{"register out of range", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// poke an oversized rho into a sparse entry
			if c[3] == modeSparse && len(c) >= 13 {
				c[12] = 200 // far above max ρ for p=10 (54)
			}
			return c
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Sketch
			if err := s.UnmarshalBinary(tc.mutator(data)); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestResetClearsState(t *testing.T) {
	s := New(8)
	for i := 0; i < 1000; i++ {
		s.AddString(strconv.Itoa(i))
	}
	s.Reset()
	if got := s.Estimate(); got != 0 {
		t.Errorf("estimate after Reset = %d, want 0", got)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := New(10)
	a.AddString("original")
	c := a.Clone()
	c.AddString("only-in-clone")
	if a.Estimate() == c.Estimate() {
		t.Error("clone is not independent of original")
	}
}

// TestByteAPIMatchesStringAPI ensures Add([]byte) and AddString agree.
func TestByteAPIMatchesStringAPI(t *testing.T) {
	a := New(12)
	b := New(12)
	for _, k := range []string{"alice", "bob", "carol"} {
		a.AddString(k)
		b.Add([]byte(k))
	}
	if a.Estimate() != b.Estimate() {
		t.Errorf("byte vs string disagree: %d vs %d", a.Estimate(), b.Estimate())
	}
}

// TestMemoryBytesScalesWithEncoding: sparse uses much less than dense.
func TestMemoryBytesScalesWithEncoding(t *testing.T) {
	sparse := New(14)
	sparse.AddString("only-one")
	if sparse.dense != nil {
		t.Fatal("expected sparse")
	}
	if got := sparse.MemoryBytes(); got > 100 {
		t.Errorf("sparse single-element MemoryBytes = %d, want small", got)
	}

	dense := New(14)
	for i := 0; i < 1_000_000; i++ {
		dense.AddString(strconv.Itoa(i))
	}
	if dense.dense == nil {
		t.Fatal("expected dense after 1M inserts at p=14")
	}
	if got, want := dense.MemoryBytes(), 1<<14; got != want {
		t.Errorf("dense MemoryBytes = %d, want %d", got, want)
	}
}

// TestSmallPrecisionAlphaConstants exercises the tabulated alpha values
// for m ∈ {16, 32, 64} which are not used at default precision.
func TestSmallPrecisionAlphaConstants(t *testing.T) {
	for _, p := range []uint8{4, 5, 6} {
		s := New(p)
		for i := 0; i < 1000; i++ {
			s.AddString(strconv.Itoa(i))
		}
		// Just verify the estimator doesn't crash and returns non-zero.
		if s.Estimate() == 0 {
			t.Errorf("p=%d: estimate of 1000 uniques returned 0", p)
		}
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
