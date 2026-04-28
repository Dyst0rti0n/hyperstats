package hll

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// TestErrorBoundEmpirical is the headline property test for this package.
//
// Claim under test (in the regime where the asymptotic bound holds):
//
//	Pr[ |Ê - n| / n  >  k · σ ]  ≤  small  for k ≈ 3
//
// where σ = 1.04 / √m is the asymptotic standard error of HLL.
//
// The bound is asymptotic. It does NOT hold at all n. There are three
// regimes:
//
//	n ≤ ~2m :  linear-counting regime (tested in TestSmallCardinalityRegime)
//	2m < n ≤ 5m : transition regime, where raw HLL has a known positive
//	             bias bump that the HLL++ empirical bias-correction tables
//	             are designed to remove (see TestKnownTransitionBias).
//	n > 5m :    asymptotic regime, where σ = 1.04/√m holds tightly.
//
// This test deliberately runs in regime (3). Adding bias correction tables
// (DESIGN.md roadmap) will let us tighten regime (2) to the same bounds.
//
// Method: T independent trials per (precision, cardinality). Each trial
// uses a fresh RNG seed. We assert:
//
//  1. Estimator is approximately unbiased (mean rel err near 0).
//  2. Empirical RMSE within ~1.5 × theoretical σ.
//  3. Fewer than 5% of trials exceed 3σ (normal would be ~0.27%).
func TestErrorBoundEmpirical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property test in -short mode")
	}

	cases := []struct {
		p      uint8
		n      int
		trials int
	}{
		// All have n ≥ 5m so they are in the asymptotic regime.
		{p: 10, n: 10_000, trials: 200},  // n/m ≈ 9.8
		{p: 12, n: 50_000, trials: 200},  // n/m ≈ 12.2
		{p: 14, n: 100_000, trials: 100}, // n/m ≈ 6.1
		{p: 14, n: 1_000_000, trials: 50},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(testName(tc.p, tc.n), func(t *testing.T) {
			t.Parallel()

			theoreticalSigma := 1.04 / math.Sqrt(float64(uint32(1)<<tc.p))

			var sumSq float64
			var sumErr float64
			violations := 0
			for trial := 0; trial < tc.trials; trial++ {
				s := New(tc.p)
				rng := rand.New(rand.NewSource(int64(trial) + 0x5eed))

				// Use 8-byte random tokens; fixed-length to avoid any
				// length-induced bias from MurmurHash3's tail handling.
				var buf [8]byte
				for i := 0; i < tc.n; i++ {
					rng.Read(buf[:])
					s.Add(buf[:])
				}

				est := float64(s.Estimate())
				rel := (est - float64(tc.n)) / float64(tc.n)
				sumErr += rel
				sumSq += rel * rel
				if math.Abs(rel) > 3*theoreticalSigma {
					violations++
				}
			}

			meanErr := sumErr / float64(tc.trials)
			rmse := math.Sqrt(sumSq / float64(tc.trials))

			// (1) Estimator should be approximately unbiased.
			//     |meanErr| should be small relative to σ/√trials. We
			//     allow 3 sigmas of slack to keep the test stable.
			meanBoundLoose := 3 * theoreticalSigma / math.Sqrt(float64(tc.trials))
			if math.Abs(meanErr) > meanBoundLoose+0.005 {
				t.Errorf("bias: mean relative error %.4f, want |.| ≤ %.4f",
					meanErr, meanBoundLoose+0.005)
			}

			// (2) Empirical RMSE within ~1.5× theoretical σ. Sub-1×
			//     happens too (HLL is sometimes better than its bound at
			//     finite n); we just guard against runaway error.
			if rmse > 1.5*theoreticalSigma {
				t.Errorf("RMSE %.4f exceeds 1.5×σ = %.4f (σ = %.4f)",
					rmse, 1.5*theoreticalSigma, theoreticalSigma)
			}

			// (3) Fewer than 5% of trials should violate 3σ.
			//     Under normality the rate would be ~0.27%; HLL is
			//     slightly heavier-tailed but still well under 5%.
			vRate := float64(violations) / float64(tc.trials)
			if vRate > 0.05 {
				t.Errorf("3σ violation rate %.2f%% (%d/%d), want ≤ 5%%",
					vRate*100, violations, tc.trials)
			}

			t.Logf("p=%d n=%d trials=%d: σ=%.4f, RMSE=%.4f, mean=%+.4f, 3σ-violations=%d",
				tc.p, tc.n, tc.trials, theoreticalSigma, rmse, meanErr, violations)
		})
	}
}

// TestSmallCardinalityRegime checks that the linear-counting branch
// produces accurate estimates in the regime n < 5m/2 where it activates.
// This regime has its own bound (Whang et al. 1990) which is tighter than
// HLL's σ for small n. A bug in the regime selection would show up as
// either very inaccurate small-n estimates or a discontinuity at the
// crossover.
func TestSmallCardinalityRegime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const p = 12
	const trials = 50
	m := float64(uint32(1) << p)

	for _, n := range []int{10, 100, 1_000, 5_000, int(2.5 * m)} {
		var sumSq float64
		for trial := 0; trial < trials; trial++ {
			s := New(p)
			rng := rand.New(rand.NewSource(int64(trial)*7919 + int64(n)))
			var buf [8]byte
			for i := 0; i < n; i++ {
				rng.Read(buf[:])
				s.Add(buf[:])
			}
			rel := (float64(s.Estimate()) - float64(n)) / float64(n)
			sumSq += rel * rel
		}
		rmse := math.Sqrt(sumSq / trials)
		// Linear counting bound at this regime is roughly √(ln(m/n)/n).
		// We use a generous 5% absolute upper bound which is conservative
		// for all the n values tested at p=12.
		if rmse > 0.05 {
			t.Errorf("small-n regime: p=%d n=%d RMSE %.4f exceeds 5%%", p, n, rmse)
		}
		t.Logf("small-n: p=%d n=%d RMSE=%.4f", p, n, rmse)
	}
}

// TestMonotonicGrowth: estimate should not decrease as we add more unique
// elements. (Non-monotonicity is a known property of raw HLL but should be
// rare — at most one register can decrease by random chance per step. We
// allow a small fraction of micro-decreases but flag systematic regression.)
func TestMonotonicGrowth(t *testing.T) {
	const p = 14
	const n = 100_000

	s := New(p)
	prev := uint64(0)
	regressions := 0
	for i := 0; i < n; i++ {
		s.AddString("u" + strconv.Itoa(i))
		// Sample every 100 inserts to keep the test fast.
		if i%100 != 0 {
			continue
		}
		cur := s.Estimate()
		if cur < prev {
			regressions++
		}
		prev = cur
	}
	// HLL register-max updates are monotonic, but the *estimate* uses a
	// harmonic mean so it can wobble. Empirically, regressions occur in
	// well under 5% of samples.
	if rate := float64(regressions) / float64(n/100); rate > 0.10 {
		t.Errorf("estimate regressions in %.1f%% of samples, want < 10%%", rate*100)
	}
}

func testName(p uint8, n int) string {
	return "p" + strconv.Itoa(int(p)) + "_n" + strconv.Itoa(n)
}

// TestKnownTransitionBias characterises the bias bump in the transition
// regime n/m ∈ (2, 5]. It does NOT enforce the asymptotic σ bound — the
// asymptotic bound provably does not hold here without empirical bias
// correction. Instead it pins the current behaviour so we can detect
// regression and so a future PR adding HLL++ bias-correction tables has
// a concrete metric to improve against.
//
// If you add bias correction and this test fails because the bias is now
// SMALLER than the documented bound, that is the success signal — tighten
// the bound here as part of the PR.
func TestKnownTransitionBias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const (
		p      = 12
		n      = 10_000 // n/m ≈ 2.44, deep in the transition regime
		trials = 200
	)
	var sumErr, sumSq float64
	for trial := 0; trial < trials; trial++ {
		s := New(p)
		rng := rand.New(rand.NewSource(int64(trial) + 0x5eed))
		var buf [8]byte
		for i := 0; i < n; i++ {
			rng.Read(buf[:])
			s.Add(buf[:])
		}
		rel := (float64(s.Estimate()) - float64(n)) / float64(n)
		sumErr += rel
		sumSq += rel * rel
	}
	bias := sumErr / float64(trials)
	rmse := math.Sqrt(sumSq / float64(trials))

	// Current observed behaviour: |bias| ≈ 1.5%, RMSE ≈ 3%. We pin a
	// loose upper bound that will catch a regression but not flake.
	const maxAllowedBias = 0.025
	const maxAllowedRMSE = 0.040
	if math.Abs(bias) > maxAllowedBias {
		t.Errorf("transition-regime bias |%.4f| exceeds %.4f", bias, maxAllowedBias)
	}
	if rmse > maxAllowedRMSE {
		t.Errorf("transition-regime RMSE %.4f exceeds %.4f", rmse, maxAllowedRMSE)
	}
	t.Logf("transition regime (p=%d, n/m≈%.2f): bias=%+.4f, RMSE=%.4f -- "+
		"this is the documented bias bump; HLL++ tables would reduce it",
		p, float64(n)/float64(uint32(1)<<p), bias, rmse)
}
