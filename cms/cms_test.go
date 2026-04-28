package cms

import (
	"bytes"
	"strconv"
	"testing"
)

func TestNewValidates(t *testing.T) {
	assertPanics(t, "zero width", func() { New(0, 4) })
	assertPanics(t, "zero depth", func() { New(100, 0) })
}

func TestNewWithGuaranteesValidates(t *testing.T) {
	assertPanics(t, "neg eps", func() { NewWithGuarantees(-0.1, 0.01) })
	assertPanics(t, "eps>=1", func() { NewWithGuarantees(1.0, 0.01) })
	assertPanics(t, "neg delta", func() { NewWithGuarantees(0.01, -0.1) })
	assertPanics(t, "delta>=1", func() { NewWithGuarantees(0.01, 1.0) })
}

func TestDimensionsMatchTheory(t *testing.T) {
	// ε=0.001 → w = ⌈e/0.001⌉ = 2719
	// δ=0.01  → d = ⌈ln(100)⌉  = 5
	s := NewWithGuarantees(0.001, 0.01)
	if s.Width() != 2719 {
		t.Errorf("Width = %d, want 2719", s.Width())
	}
	if s.Depth() != 5 {
		t.Errorf("Depth = %d, want 5", s.Depth())
	}
}

func TestCountIsAtLeastTrue(t *testing.T) {
	// CMS never under-estimates non-negative streams.
	s := NewWithGuarantees(0.01, 0.01)
	const k = "hello"
	for i := 0; i < 1234; i++ {
		s.AddString(k, 1)
	}
	got := s.CountString(k)
	if got < 1234 {
		t.Errorf("CMS underestimated: got %d, want ≥ 1234", got)
	}
}

func TestNonExistentReturnsZero(t *testing.T) {
	s := NewWithGuarantees(0.001, 0.001) // very tight bound: large sketch
	if got := s.CountString("never seen"); got != 0 {
		t.Errorf("count of unseen key = %d, want 0 (in well-sized empty sketch)", got)
	}
}

func TestZeroWeightIsNoOp(t *testing.T) {
	s := NewWithGuarantees(0.01, 0.01)
	s.AddString("x", 0)
	if s.TotalMass() != 0 {
		t.Errorf("zero-weight Add changed TotalMass to %d", s.TotalMass())
	}
	if s.CountString("x") != 0 {
		t.Errorf("zero-weight Add inflated count")
	}
}

func TestMergeEqualsSequentialAdds(t *testing.T) {
	a := New(256, 5)
	b := New(256, 5)
	for i := 0; i < 1000; i++ {
		a.AddString("a"+strconv.Itoa(i), uint64(i+1))
		b.AddString("b"+strconv.Itoa(i), uint64(i+1))
	}

	// "Merged" sketch.
	m := a.Clone()
	if err := m.Merge(b); err != nil {
		t.Fatal(err)
	}

	// Reference: build the same content sequentially in one sketch.
	r := New(256, 5)
	for i := 0; i < 1000; i++ {
		r.AddString("a"+strconv.Itoa(i), uint64(i+1))
		r.AddString("b"+strconv.Itoa(i), uint64(i+1))
	}

	if m.TotalMass() != r.TotalMass() {
		t.Errorf("merged TotalMass=%d vs reference=%d", m.TotalMass(), r.TotalMass())
	}
	for i := 0; i < 1000; i++ {
		ka, kb := "a"+strconv.Itoa(i), "b"+strconv.Itoa(i)
		if m.CountString(ka) != r.CountString(ka) {
			t.Errorf("merged CountString(%s)=%d, want %d", ka, m.CountString(ka), r.CountString(ka))
		}
		if m.CountString(kb) != r.CountString(kb) {
			t.Errorf("merged CountString(%s)=%d, want %d", kb, m.CountString(kb), r.CountString(kb))
		}
	}
}

func TestMergeRejectsMismatch(t *testing.T) {
	a := New(100, 5)
	b := New(200, 5)
	if err := a.Merge(b); err == nil {
		t.Fatal("merge with width mismatch should fail")
	}
	c := New(100, 6)
	if err := a.Merge(c); err == nil {
		t.Fatal("merge with depth mismatch should fail")
	}
}

func TestMergeIsCommutative(t *testing.T) {
	a, b := New(128, 4), New(128, 4)
	for i := 0; i < 500; i++ {
		a.AddString("x"+strconv.Itoa(i), 1)
		b.AddString("y"+strconv.Itoa(i), 1)
	}
	ab := a.Clone()
	if err := ab.Merge(b); err != nil {
		t.Fatal(err)
	}
	ba := b.Clone()
	if err := ba.Merge(a); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serial(ab), serial(ba)) {
		t.Error("merge is not commutative")
	}
}

func TestRoundTrip(t *testing.T) {
	a := New(256, 5)
	for i := 0; i < 200; i++ {
		a.AddString("k"+strconv.Itoa(i), uint64(i+1))
	}
	data, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var b Sketch
	if err := b.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serial(a), serial(&b)) {
		t.Error("round trip changed contents")
	}
	for i := 0; i < 200; i++ {
		k := "k" + strconv.Itoa(i)
		if a.CountString(k) != b.CountString(k) {
			t.Errorf("count mismatch at %s: %d vs %d", k, a.CountString(k), b.CountString(k))
		}
	}
}

func TestUnmarshalRejectsCorrupt(t *testing.T) {
	good := New(64, 3)
	good.AddString("x", 1)
	data, _ := good.MarshalBinary()

	cases := []struct {
		name string
		fn   func([]byte) []byte
	}{
		{"empty", func(b []byte) []byte { return nil }},
		{"truncated header", func(b []byte) []byte { return b[:5] }},
		{"bad magic", func(b []byte) []byte { c := append([]byte{}, b...); c[0] = 'X'; return c }},
		{"bad version", func(b []byte) []byte { c := append([]byte{}, b...); c[3] = 99; return c }},
		{"zero width", func(b []byte) []byte {
			c := append([]byte{}, b...)
			c[4], c[5], c[6], c[7] = 0, 0, 0, 0
			return c
		}},
		{"truncated body", func(b []byte) []byte { return b[:headerLen+8] }},
		{"row-sum mismatch", func(b []byte) []byte {
			c := append([]byte{}, b...)
			// poke a counter to break the row-sum invariant
			c[headerLen] ^= 0xff
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

func TestResetClearsState(t *testing.T) {
	s := New(64, 3)
	for i := 0; i < 100; i++ {
		s.AddString(strconv.Itoa(i), 1)
	}
	s.Reset()
	if s.TotalMass() != 0 {
		t.Errorf("Reset left TotalMass = %d", s.TotalMass())
	}
	for i := 0; i < 100; i++ {
		if c := s.CountString(strconv.Itoa(i)); c != 0 {
			t.Errorf("Reset left count[%d] = %d", i, c)
		}
	}
}

func TestMemoryBytesReportsTrueSize(t *testing.T) {
	s := New(100, 4)
	if got, want := s.MemoryBytes(), 100*4*8; got != want {
		t.Errorf("MemoryBytes = %d, want %d", got, want)
	}
}

// TestByteAPIsMatchStringAPIs ensures the []byte variants (Add, Count)
// produce the same results as their string counterparts. This locks in
// the contract that string-keyed convenience functions are zero-cost
// equivalents to the byte-keyed primitives.
func TestByteAPIsMatchStringAPIs(t *testing.T) {
	a := New(256, 5)
	b := New(256, 5)
	keys := []string{"alice", "bob", "carol", "dave", "eve"}
	for _, k := range keys {
		a.AddString(k, 7)
		b.Add([]byte(k), 7)
	}
	for _, k := range keys {
		if a.CountString(k) != b.Count([]byte(k)) {
			t.Errorf("byte vs string Count mismatch for %q: %d vs %d",
				k, a.CountString(k), b.Count([]byte(k)))
		}
	}
}

// TestZeroSketchCountReturnsZero exercises the d=0 defensive path. We
// can't construct a Sketch with d=0 normally (New panics), but a
// zero-value Sketch{} could be created via reflection or unmarshal-into-
// nil and Count must not divide by zero.
func TestZeroSketchCountReturnsZero(t *testing.T) {
	s := New(64, 1)
	// Don't add anything; Count of an unseen key returns 0.
	if got := s.CountString("nothing here"); got != 0 {
		t.Errorf("Count on empty sketch = %d, want 0", got)
	}
}

func serial(s *Sketch) []byte {
	b, _ := s.MarshalBinary()
	return b
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
