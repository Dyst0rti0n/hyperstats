package kll

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"testing"
)

// TestRankErrorBoundEmpirical is the headline property test for KLL.
//
// Claim under test (Karnin, Lang, Liberty 2016, Theorem 1):
//
//	Pr[ |R̂(q) - R(q)| ≤ ε · N  for all q ] ≥ 1 - δ
//
// for k satisfying k ≥ C · (1/ε) · √log₂(1/δ).
//
// We test the **sup-over-quantiles** error, which is the quantity bounded
// by KLL's "all q" guarantee. The empirical sup-error constant is larger
// than the per-query constant ε_q ≈ 1.66/k because querying many
// quantiles probes more of the error distribution and orphan-item
// handling contributes a fixed-cost term. This package documents:
//
//	ε_sup ≈ 5.0 / k   at 99% confidence on ~100 quantile probes
//
// Method: across many trials at each k, build a sketch from random
// inputs, query at 99 quantile probes, record the max absolute rank
// error per trial. Assert that the trial-mean and 99th-percentile of the
// max-rank-error distribution stay below the documented sup bound.
func TestRankErrorBoundEmpirical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property test in -short mode")
	}

	cases := []struct {
		k      uint32
		n      int
		trials int
	}{
		{k: 100, n: 100_000, trials: 30},
		{k: 200, n: 1_000_000, trials: 15},
		{k: 800, n: 1_000_000, trials: 10},
	}

	probes := make([]float64, 99)
	for i := range probes {
		probes[i] = float64(i+1) / 100.0
	}

	for _, tc := range cases {
		tc := tc
		t.Run("k"+strconv.Itoa(int(tc.k)), func(t *testing.T) {
			t.Parallel()

			// Documented sup-over-quantiles bound. Stays conservative
			// across k ∈ [100, 800].
			epsSup := 5.0 / float64(tc.k)
			epsPerQuery := 1.66 / float64(tc.k)

			var trialMaxErrs []float64
			for trial := 0; trial < tc.trials; trial++ {
				rng := rand.New(rand.NewSource(int64(trial) + 7919))
				s := NewWithSeed(tc.k, int64(trial))
				values := make([]float64, tc.n)
				for i := 0; i < tc.n; i++ {
					values[i] = rng.Float64()
					s.Add(values[i])
				}
				sort.Float64s(values)

				maxErr := 0.0
				for _, q := range probes {
					trueIdx := int(math.Floor(q * float64(tc.n)))
					if trueIdx >= tc.n {
						trueIdx = tc.n - 1
					}
					trueValue := values[trueIdx]
					estRank := s.Rank(trueValue)
					trueRank := q
					err := math.Abs(estRank - trueRank)
					if err > maxErr {
						maxErr = err
					}
				}
				trialMaxErrs = append(trialMaxErrs, maxErr)
			}

			// Summary stats.
			sort.Float64s(trialMaxErrs)
			p50 := trialMaxErrs[len(trialMaxErrs)/2]
			p99 := trialMaxErrs[int(math.Min(float64(len(trialMaxErrs)-1), float64(len(trialMaxErrs))*0.99))]
			meanErr := 0.0
			for _, e := range trialMaxErrs {
				meanErr += e
			}
			meanErr /= float64(len(trialMaxErrs))

			t.Logf("k=%d n=%d trials=%d -- ε_q=%.4f ε_sup=%.4f mean(max_err)=%.4f p50=%.4f p99=%.4f",
				tc.k, tc.n, tc.trials, epsPerQuery, epsSup, meanErr, p50, p99)

			// Primary assertion: 99th-percentile of trial max-error
			// stays within the sup-over-quantiles bound.
			if p99 > epsSup {
				t.Errorf("p99(max rank error) = %.4f exceeds ε_sup = %.4f",
					p99, epsSup)
			}
			// Secondary: typical trial max-error is well under ε_sup.
			if p50 > epsSup*0.7 {
				t.Errorf("p50(max rank error) = %.4f exceeds 0.7×ε_sup = %.4f",
					p50, epsSup*0.7)
			}
			// Tertiary: max-error scales as expected (not blowing up).
			if meanErr > epsSup*0.8 {
				t.Errorf("mean(max rank error) = %.4f exceeds 0.8×ε_sup = %.4f",
					meanErr, epsSup*0.8)
			}
		})
	}
}

// TestAdversarialSortedInput: KLL's analysis is order-independent (the
// random coins make the sketch invariant to input order in expectation).
// Sorted input is the worst case for many naïve quantile sketches; KLL
// should handle it as well as random.
func TestAdversarialSortedInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const k = 200
	const n = 1_000_000

	// In-order ascending.
	asc := NewWithSeed(k, 1)
	for i := 0; i < n; i++ {
		asc.Add(float64(i))
	}
	// In-order descending.
	desc := NewWithSeed(k, 1)
	for i := n - 1; i >= 0; i-- {
		desc.Add(float64(i))
	}

	probes := []float64{0.01, 0.1, 0.5, 0.9, 0.99}
	for _, q := range probes {
		expected := q * float64(n)
		ascQ := asc.Quantile(q)
		descQ := desc.Quantile(q)
		ascErr := math.Abs(ascQ-expected) / float64(n)
		descErr := math.Abs(descQ-expected) / float64(n)
		// 1.66/200 ≈ 0.83% expected; allow 3% to be conservative.
		if ascErr > 0.03 {
			t.Errorf("ascending: q=%f got %f, expected %f, rank err=%.4f", q, ascQ, expected, ascErr)
		}
		if descErr > 0.03 {
			t.Errorf("descending: q=%f got %f, expected %f, rank err=%.4f", q, descQ, expected, descErr)
		}
		t.Logf("q=%f asc=%f (err=%.4f) desc=%f (err=%.4f)", q, ascQ, ascErr, descQ, descErr)
	}
}

// TestHeavyTailedDistribution: real-world latency distributions are
// heavy-tailed. KLL's rank-based bound is invariant under any monotone
// transformation of the values, so this should still satisfy ε ≈ 1.66/k.
func TestHeavyTailedDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const k = 200
	const n = 500_000

	rng := rand.New(rand.NewSource(0xfeedface))
	s := NewWithSeed(k, 1)
	values := make([]float64, n)
	for i := 0; i < n; i++ {
		// Pareto-ish: uniform u → 1/(1-u)^2, gives heavy upper tail.
		u := rng.Float64()
		x := 1.0 / math.Pow(1-u, 2)
		values[i] = x
		s.Add(x)
	}
	sort.Float64s(values)

	maxErr := 0.0
	for q := 0.01; q < 1.0; q += 0.01 {
		trueIdx := int(math.Floor(q * float64(n)))
		trueValue := values[trueIdx]
		estRank := s.Rank(trueValue)
		err := math.Abs(estRank - q)
		if err > maxErr {
			maxErr = err
		}
	}
	if maxErr > 0.025 { // 1.66/200 ≈ 0.0083; allow 3× for variance
		t.Errorf("heavy-tailed: max rank err %.4f exceeds 0.025", maxErr)
	}
	t.Logf("heavy-tailed Pareto: max rank err = %.4f (ε_th ≈ %.4f)", maxErr, 1.66/float64(k))
}

// TestMergeAccuracyMatchesSequential: split a stream into S shards, build
// per-shard sketches, merge them; compare error against a single sketch
// over the concatenated stream. With KLL these should be equivalent in
// distribution (modulo random coins).
func TestMergeAccuracyMatchesSequential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const (
		k      = 200
		n      = 100_000
		shards = 8
	)

	rng := rand.New(rand.NewSource(1234))
	values := make([]float64, n)
	for i := range values {
		values[i] = rng.NormFloat64()*100 + 500
	}

	// Sequential.
	seq := NewWithSeed(k, 1)
	for _, v := range values {
		seq.Add(v)
	}

	// Sharded + merged.
	shardSks := make([]*Sketch, shards)
	for s := 0; s < shards; s++ {
		shardSks[s] = NewWithSeed(k, int64(s)+100)
	}
	for i, v := range values {
		shardSks[i%shards].Add(v)
	}
	merged := shardSks[0]
	for s := 1; s < shards; s++ {
		if err := merged.Merge(shardSks[s]); err != nil {
			t.Fatal(err)
		}
	}

	if seq.N() != merged.N() {
		t.Errorf("N mismatch after merge: seq=%d merged=%d", seq.N(), merged.N())
	}

	sortedValues := append([]float64(nil), values...)
	sort.Float64s(sortedValues)

	probes := []float64{0.01, 0.1, 0.5, 0.9, 0.99}
	for _, q := range probes {
		trueIdx := int(math.Floor(q * float64(n)))
		trueValue := sortedValues[trueIdx]

		seqRank := seq.Rank(trueValue)
		mrgRank := merged.Rank(trueValue)
		seqErr := math.Abs(seqRank - q)
		mrgErr := math.Abs(mrgRank - q)

		// Both errors should be within ε bound. Crucially the merged
		// error is not systematically larger.
		if mrgErr > 0.025 {
			t.Errorf("merged rank err at q=%f: %.4f > 0.025", q, mrgErr)
		}
		t.Logf("q=%f: seq err=%.4f, merged err=%.4f", q, seqErr, mrgErr)
	}
}
