package tdigest

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"testing"
)

// TestRankErrorEmpirical measures absolute rank error on uniform inputs at
// various quantile probes. t-digest's accuracy profile is asymmetric: it
// is most accurate at the tails (q close to 0 or 1) and weakest at the
// median. This test confirms both shape and magnitude.
func TestRankErrorEmpirical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property test in -short mode")
	}

	cases := []struct {
		delta float64
		n     int
		// Per-region rank error budgets. t-digest is far more accurate
		// at the tails than the middle. These budgets are conservative;
		// real performance is typically ~3-5× tighter.
		midBudget  float64 // for q ∈ [0.25, 0.75]
		tailBudget float64 // for q outside that
	}{
		{delta: 50, n: 100_000, midBudget: 0.04, tailBudget: 0.02},
		{delta: 100, n: 1_000_000, midBudget: 0.025, tailBudget: 0.012},
		{delta: 500, n: 1_000_000, midBudget: 0.006, tailBudget: 0.003},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("delta_"+strconv.FormatFloat(tc.delta, 'f', 0, 64), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(tc.delta) * 7919))
			s := New(tc.delta)
			values := make([]float64, tc.n)
			for i := 0; i < tc.n; i++ {
				values[i] = rng.Float64()
				s.Add(values[i])
			}
			sort.Float64s(values)

			probes := []float64{0.001, 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 0.999}
			worstMid, worstTail := 0.0, 0.0
			for _, q := range probes {
				trueIdx := int(math.Floor(q * float64(tc.n)))
				if trueIdx >= tc.n {
					trueIdx = tc.n - 1
				}
				trueValue := values[trueIdx]
				estRank := s.Rank(trueValue)
				err := math.Abs(estRank - q)

				inMiddle := q >= 0.25 && q <= 0.75
				budget := tc.tailBudget
				region := "tail"
				if inMiddle {
					budget = tc.midBudget
					region = "mid"
				}
				if err > budget {
					t.Errorf("[%s] q=%g rank err %g > budget %g", region, q, err, budget)
				}
				if inMiddle {
					if err > worstMid {
						worstMid = err
					}
				} else if err > worstTail {
					worstTail = err
				}
			}
			t.Logf("δ=%g n=%d centroids=%d -- worst mid err %g, worst tail err %g",
				tc.delta, tc.n, s.CentroidCount(), worstMid, worstTail)
		})
	}
}

// TestTailsAreExtremelyAccurate is the headline t-digest claim: tail
// quantiles are very accurate, much more so than mid-distribution. This
// confirms the asymmetric accuracy profile that makes t-digest the right
// choice for latency dashboards.
func TestTailsAreExtremelyAccurate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property test in -short mode")
	}
	const (
		delta = 100.0
		n     = 1_000_000
	)
	rng := rand.New(rand.NewSource(0xfacefade))
	s := New(delta)
	values := make([]float64, n)
	for i := 0; i < n; i++ {
		values[i] = rng.Float64()
		s.Add(values[i])
	}
	sort.Float64s(values)

	// Probe a range of tail quantiles.
	for _, q := range []float64{0.999, 0.9999, 0.0001, 0.001} {
		trueIdx := int(math.Floor(q * float64(n)))
		if trueIdx >= n {
			trueIdx = n - 1
		}
		if trueIdx < 0 {
			trueIdx = 0
		}
		trueValue := values[trueIdx]
		estRank := s.Rank(trueValue)
		err := math.Abs(estRank - q)
		// Tails should be within 0.5% absolute rank error.
		if err > 0.005 {
			t.Errorf("tail q=%g: rank err %g exceeds 0.5%%", q, err)
		}
		t.Logf("tail q=%g: estRank=%g err=%g", q, estRank, err)
	}
}

// TestMergeAccuracyMatchesSequential: shard a stream, build per-shard
// digests, merge them; compare to a sequential build. They should agree
// to within t-digest's normal error budget.
func TestMergeAccuracyMatchesSequential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const (
		delta  = 100.0
		n      = 200_000
		shards = 8
	)
	rng := rand.New(rand.NewSource(424242))
	values := make([]float64, n)
	for i := range values {
		values[i] = rng.NormFloat64()*10 + 100
	}

	// Sequential.
	seq := New(delta)
	for _, v := range values {
		seq.Add(v)
	}

	// Sharded + merged.
	shardSks := make([]*Sketch, shards)
	for s := 0; s < shards; s++ {
		shardSks[s] = New(delta)
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

	if math.Abs(seq.TotalWeight()-merged.TotalWeight()) > 1e-9 {
		t.Errorf("TotalWeight differs: seq=%g merged=%g", seq.TotalWeight(), merged.TotalWeight())
	}

	// Compare quantile estimates against ground truth.
	sortedValues := append([]float64(nil), values...)
	sort.Float64s(sortedValues)
	for _, q := range []float64{0.01, 0.1, 0.5, 0.9, 0.99} {
		idx := int(math.Floor(q * float64(n)))
		if idx >= n {
			idx = n - 1
		}
		trueV := sortedValues[idx]

		seqQ := seq.Quantile(q)
		mrgQ := merged.Quantile(q)

		seqErr := math.Abs(seqQ-trueV) / math.Abs(trueV)
		mrgErr := math.Abs(mrgQ-trueV) / math.Abs(trueV)

		// Both within ~5% relative error on a normal distribution.
		if mrgErr > 0.05 {
			t.Errorf("merged Quantile(%g)=%g vs true=%g (rel err %g) exceeds 5%%",
				q, mrgQ, trueV, mrgErr)
		}
		t.Logf("q=%g: true=%g, seq=%g (err %g), merged=%g (err %g)",
			q, trueV, seqQ, seqErr, mrgQ, mrgErr)
	}
}

// TestHeavyTailedDistribution: t-digest is designed for skewed/heavy-tailed
// data (latency, financial, etc). Verify accuracy holds.
func TestHeavyTailedDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const (
		delta = 100.0
		n     = 500_000
	)
	rng := rand.New(rand.NewSource(0xabcd1234))
	s := New(delta)
	values := make([]float64, n)
	for i := 0; i < n; i++ {
		// Pareto-like: 1 / (1-u)^2.
		x := 1.0 / math.Pow(1-rng.Float64(), 2)
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
	if maxErr > 0.03 {
		t.Errorf("heavy-tailed: max rank err %g exceeds 3%%", maxErr)
	}
	t.Logf("heavy-tailed Pareto: max rank err = %g (centroids = %d)",
		maxErr, s.CentroidCount())
}
