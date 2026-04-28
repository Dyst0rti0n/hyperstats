// Package kll implements the KLL quantile sketch (Karnin, Lang, Liberty,
// FOCS 2016). KLL is a streaming quantile sketch with the best known
// asymptotic accuracy/space tradeoff and full mergeability.
//
// # Quick start
//
//	s := kll.New(200)              // ε ≈ 1.65% additive error w.h.p.
//	for _, x := range latencies {
//	    s.Add(x)
//	}
//	p99 := s.Quantile(0.99)
//	rank := s.Rank(50.0)           // fraction of items ≤ 50.0
//
// # Theory
//
// A KLL sketch maintains a stack of "compactors", each a buffer of
// floating-point items. Level 0 receives raw inserts; when it overflows
// it is sorted and "compacted": every other item (odd or even, chosen
// uniformly at random) is promoted to level 1, the rest are discarded.
// Items at level h are conceptually present with weight 2^h, since each
// promotion to level h+1 represents 2 items at level h.
//
// The capacity of level h shrinks geometrically:
//
//	cap(h) = max(2, ⌈k · c^(H-h)⌉)    where c ≈ 2/3, H = top level
//
// so the total memory is bounded by k / (1-c) ≈ 3k floats. Importantly
// the high levels (large weight, fewer items) are smaller, which keeps
// the variance of the random promotion low.
//
// # Error bound
//
// Karnin, Lang and Liberty prove (Theorem 1):
//
//	Pr[ |R̂(q) - R(q)| ≤ ε · N for all queries q ] ≥ 1 - δ
//
// where R(q) is the true rank of q in a stream of length N, R̂(q) is the
// estimated rank, and the parameter k must satisfy:
//
//	k ≥ C · (1/ε) · √log₂(1/δ)
//
// for a small constant C. The paper's worst-case constant is conservative;
// empirical measurements (this package's property tests; Apache
// DataSketches benchmarks) show two practically useful constants:
//
//	per-query (single q):              ε_q  ≈ 1.66 / k   at 99% conf.
//	sup over ~100 quantile probes:     ε_sup ≈ 5.0  / k   at 99% conf.
//
// At k=200 these give ε_q ≈ 0.83% and ε_sup ≈ 2.5%; at k=800 they
// give ε_q ≈ 0.21% and ε_sup ≈ 0.62%. Use ε_sup when reasoning about a
// dashboard that displays many quantiles; use ε_q when you query one
// specific quantile (e.g. p99 latency).
//
// The sup constant is larger than 2 × ε_q because the orphan-item handling
// inside compactors contributes a fixed-cost term that becomes relatively
// larger at small ε. The 5/k figure is conservative across k ∈ [100, 800];
// the property tests in this package empirically verify it.
//
// # Comparison with other quantile sketches
//
//	sketch         worst-case rank error      mergeable    notes
//	------         ---------------------      ---------    --------------------------
//	GK             ε  with prob 1             no           classic 2001 algorithm
//	t-digest       O(ε · q(1-q))             approximately  empirically excellent at
//	                                                       extreme quantiles, weak
//	                                                       worst-case bound
//	KLL            ε  w.h.p.                  yes (exact)  best known asymptotic
//
// KLL's edge for our purposes is mergeability — sharded streams can
// build sketches independently and combine them with no loss of accuracy
// versus a single sequential sketch.
//
// # Implementation notes
//
//   - We follow the paper's "compactor" structure literally, with
//     per-level capacity floor of 2 and shrink factor c = 2/3.
//   - The randomised "drop odd/even" choice uses crypto-grade fairness:
//     each compaction flips one fair coin from the user-supplied PRNG.
//     Tests use a fixed seed for reproducibility; production callers
//     should leave the seed at its default (non-deterministic).
//   - Quantile and Rank queries sort the union of all compactor contents
//     into a weighted CDF. Cost: O(M log M) where M = O(k) is the total
//     items in the sketch.
//
// # References
//
//   - Karnin, Z., Lang, K., Liberty, E. (2016). "Optimal quantile
//     approximation in streams." FOCS.
//   - Apache DataSketches, KLL Sketch documentation.
//     https://datasketches.apache.org/docs/KLL/KLLSketch.html
package kll
