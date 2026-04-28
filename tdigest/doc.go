// Package tdigest implements the merging variant of t-digest (Dunning
// 2019). t-digest is a quantile sketch with empirically excellent
// accuracy at the tails (p99, p99.9, p1, p0.1) — the regimes operations
// teams care about most for latency dashboards.
//
// # Quick start
//
//	d := tdigest.New(100)         // δ=100 → ~1% relative error mid, much better tails
//	for _, x := range latencies {
//	    d.Add(x)
//	}
//	p99 := d.Quantile(0.99)
//	p999 := d.Quantile(0.999)
//
// # Theory
//
// t-digest is a centroid-based quantile sketch. The stream is summarised
// as a list of centroids (mean, weight) pairs sorted by mean. The list
// is bounded by enforcing a *scale function* k(q) which constrains the
// maximum weight of any centroid covering quantile q:
//
//	weight_max(q) ≤ 4N · sin(π q (1-q) / δ) / π
//
// where δ is a user-tunable compression parameter. The scale function
// concentrates centroids near q=0 and q=1 — i.e. the tails of the
// distribution — at the expense of precision in the middle. The
// canonical scale function from Dunning 2019 is:
//
//	k(q) = (δ / 2π) · arcsin(2q - 1)
//
// which has bounded slope dk/dq ≈ δ/π√(q(1-q)), so each centroid covers
// at most one unit of "k-space" no matter where on the [0,1] axis it sits.
//
// # Error bounds
//
// Unlike HyperLogLog or KLL, t-digest does NOT have a simple closed-form
// rank-error bound. Empirical results from Dunning's paper and
// independent benchmarks (Rohrmann et al., Computational Statistics 2020)
// show:
//
//   - p50 ± δ-dependent absolute rank error (~1% at δ=100)
//   - p99/p99.9 typically within 0.01-0.1% rank error at δ=100
//   - extreme tails: error scales as O(1/δ) but with very small constant
//
// The asymmetric accuracy profile is the main reason to choose t-digest
// over KLL for latency monitoring: you usually care about p99 to fewer
// decimal places of *rank* but more decimal places of *value*.
//
// # Mergeable variant
//
// This package implements the *merging* t-digest variant (Dunning's 2019
// rewrite) rather than the older clustering variant. Merging works as
// follows:
//
//  1. Buffer incoming items in an unsorted list.
//  2. When the buffer fills, sort everything (existing centroids +
//     new items, treating each new item as a centroid of weight 1).
//  3. Sweep left to right, greedily folding adjacent centroids together
//     while the combined weight does not exceed the k-space bound.
//
// This procedure has a key property: it commutes with merging. Merging
// two t-digests is identical (up to floating point) to constructing a
// single t-digest from the concatenated stream.
//
// **However**, t-digest's merge is NOT bit-exact across orderings the
// way KLL's is. Two sketches built from the same multiset in different
// orders will have slightly different centroids. Empirically the
// resulting quantile estimates agree to within the same ~δ-dependent
// error budget; this is documented and tested in cms_property_test.go.
//
// # When to use t-digest vs. KLL
//
//	use case                                use this
//	------------------------------------    -------------
//	general-purpose quantiles               KLL
//	merging across shards (exact)           KLL
//	p99/p99.9/p99.99 latency dashboards     t-digest
//	heavy-tailed distributions              t-digest
//	worst-case bounded rank error           KLL
//	tightest tail bounds at fixed memory    t-digest
//
// # References
//
//   - Dunning, T. (2019). "The t-digest: Efficient estimates of
//     distributions." Software Impacts 7.
//     https://arxiv.org/abs/1902.04023
//   - Dunning, T., Ertl, O. Reference implementation.
//     https://github.com/tdunning/t-digest
//   - Rohrmann, T. et al. (2020). "Computational study of t-digest's
//     accuracy and performance." Computational Statistics.
package tdigest
