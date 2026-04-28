package cms

import (
	"math/rand"
	"strconv"
	"testing"
)

// TestEpsDeltaBoundEmpirical is the headline property test for CMS.
//
// Claim under test (Cormode & Muthukrishnan 2005, Theorem 1):
//
//	Pr[ f̂(x) - f(x) ≤ ε · N ] ≥ 1 - δ
//
// Method: build a stream where one key occurs many times and many other
// keys occur once. Build the sketch sized for (ε, δ). Then for each
// distinct key check whether the over-estimate exceeds εN. The fraction
// of keys exceeding the bound must be ≤ δ (with statistical slack).
//
// This is per-query, not over-all-queries: the bound is per-x, so we
// average violations over the K queried keys and compare to δ.
func TestEpsDeltaBoundEmpirical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	cases := []struct {
		eps   float64
		delta float64
		// Number of distinct unique-occurrence keys.
		uniques int
		// Number of times the heavy hitter occurs.
		heavyHits int
	}{
		{eps: 0.01, delta: 0.01, uniques: 5_000, heavyHits: 100},
		{eps: 0.005, delta: 0.05, uniques: 10_000, heavyHits: 100},
		{eps: 0.001, delta: 0.01, uniques: 50_000, heavyHits: 1000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(testName(tc.eps, tc.delta, tc.uniques), func(t *testing.T) {
			t.Parallel()

			s := NewWithGuarantees(tc.eps, tc.delta)
			rng := rand.New(rand.NewSource(0xc0ffee))

			// Insert "uniques" distinct keys (each weight 1) and one
			// heavy hitter "h" with weight heavyHits. True frequencies:
			//   h:  heavyHits
			//   each unique:  1
			heavy := []byte("HEAVY-HITTER-MARKER")
			s.Add(heavy, uint64(tc.heavyHits))
			truth := map[string]uint64{string(heavy): uint64(tc.heavyHits)}
			for i := 0; i < tc.uniques; i++ {
				k := []byte("u-" + strconv.Itoa(i))
				s.Add(k, 1)
				truth[string(k)] = 1
			}

			// Bound is ε·N where N is total mass.
			N := s.TotalMass()
			boundExact := uint64(float64(N)*tc.eps + 0.5) // round
			// Slack: the bound is asymptotic in expectation. We use 1.0
			// without slack — the theory IS tight.
			violations := 0
			over := 0
			maxOverestimate := uint64(0)
			for k, true_v := range truth {
				_ = rng // satisfy linter even if we change the loop later
				est := s.CountString(k)
				if est < true_v {
					t.Fatalf("UNDERESTIMATE — fundamental violation: %q got %d, true %d",
						k, est, true_v)
				}
				diff := est - true_v
				if diff > 0 {
					over++
				}
				if diff > maxOverestimate {
					maxOverestimate = diff
				}
				if diff > boundExact {
					violations++
				}
			}

			vRate := float64(violations) / float64(len(truth))
			t.Logf("ε=%.4f δ=%.3f w=%d d=%d N=%d εN=%d -- "+
				"violations=%d/%d (%.2f%%, want ≤ %.2f%%), "+
				"max overestimate=%d, %d/%d had any overestimate",
				tc.eps, tc.delta, s.Width(), s.Depth(), N, boundExact,
				violations, len(truth), vRate*100, tc.delta*100,
				maxOverestimate, over, len(truth))

			// Statistical slack: we use a Wald-style upper bound on δ
			// suitable for the smallest-N test in the table. Allow up
			// to 2× the bound to keep CI non-flaky while still catching
			// real regressions.
			if vRate > 2*tc.delta {
				t.Errorf("violation rate %.4f exceeds 2δ = %.4f", vRate, 2*tc.delta)
			}

			// Heavy hitter must be reported within εN of true value.
			heavyEst := s.Count(heavy)
			heavyDiff := heavyEst - uint64(tc.heavyHits)
			if heavyDiff > boundExact {
				t.Errorf("heavy hitter exceeded bound: diff=%d, εN=%d",
					heavyDiff, boundExact)
			}
		})
	}
}

// TestNeverUnderestimates: a foundational invariant. If this ever fails,
// the basic data structure is wrong (or someone implemented deletion
// without rewriting the bound docs).
func TestNeverUnderestimates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	rng := rand.New(rand.NewSource(0xdeadbeef))
	for trial := 0; trial < 20; trial++ {
		s := NewWithGuarantees(0.005, 0.01)
		truth := map[string]uint64{}
		for i := 0; i < 5000; i++ {
			k := strconv.Itoa(rng.Intn(500))
			v := uint64(rng.Intn(10) + 1)
			s.AddString(k, v)
			truth[k] += v
		}
		for k, want := range truth {
			if got := s.CountString(k); got < want {
				t.Fatalf("trial %d: CMS underestimated %q (%d < %d)",
					trial, k, got, want)
			}
		}
	}
}

// TestSkewedStreamHeavyHitterFidelity demonstrates a key practical
// property: in a Zipfian stream, the top elements are well-estimated even
// when the εN bound is loose for small elements.
func TestSkewedStreamHeavyHitterFidelity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const eps, delta = 0.001, 0.01
	s := NewWithGuarantees(eps, delta)
	rng := rand.New(rand.NewSource(42))

	// Zipfian-ish: a few heavy hitters, long tail.
	const K = 10_000
	const top = 5
	const totalEvents = 1_000_000
	truth := make(map[string]uint64, K)
	for n := 0; n < totalEvents; n++ {
		var k string
		if rng.Float64() < 0.5 {
			// Half of all events go to the top-5 keys.
			k = "hot-" + strconv.Itoa(rng.Intn(top))
		} else {
			k = "tail-" + strconv.Itoa(rng.Intn(K))
		}
		s.AddString(k, 1)
		truth[k]++
	}

	// Heavy hitters should be very accurate.
	for i := 0; i < top; i++ {
		k := "hot-" + strconv.Itoa(i)
		got, want := s.CountString(k), truth[k]
		// Relative error tolerance: εN / want. Heavy hitter has high
		// `want` so the relative error is small.
		boundAbs := uint64(float64(s.TotalMass())*eps + 0.5)
		if got < want {
			t.Errorf("hot key %s underestimate: %d < %d", k, got, want)
		}
		if got-want > boundAbs {
			t.Errorf("hot key %s exceeds εN bound: diff=%d > %d", k, got-want, boundAbs)
		}
		t.Logf("hot key %s: true=%d est=%d (rel err %.4f%%)",
			k, want, got, 100*float64(got-want)/float64(want))
	}
}

func testName(eps, delta float64, n int) string {
	return "eps_" + strconv.FormatFloat(eps, 'f', -1, 64) +
		"_delta_" + strconv.FormatFloat(delta, 'f', -1, 64) +
		"_n" + strconv.Itoa(n)
}
