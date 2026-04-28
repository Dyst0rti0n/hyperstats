// Package hll implements a HyperLogLog cardinality sketch with the core
// improvements from Heule, Nunkesser, and Hall (2013): 64-bit hashing and a
// sparse-to-dense register representation. This package estimates the number
// of distinct elements in a stream using sub-linear memory.
//
// # Quick start
//
//	s := hll.New(14)              // ~16 KB, ~0.81% relative error
//	s.AddString("alice")
//	s.AddString("bob")
//	s.AddString("alice")          // duplicate is absorbed
//	n := s.Estimate()             // returns ~2
//
// Sketches with the same precision can be merged in any order, making HLL
// embarrassingly parallel.
//
//	a.Merge(b)                    // a now estimates |A ∪ B|
//
// # Theory
//
// HyperLogLog (Flajolet, Fusy, Gandouet, Meunier, 2007) approximates
// |{x : x ∈ S}| using m = 2^p registers, each holding the maximum number of
// leading zeros (plus one) observed in the hash suffix routed to that
// register. The harmonic mean of 2^M[i] across registers, normalised by the
// constant α_m, is an unbiased estimator of cardinality:
//
//	E = α_m · m^2 / Σ_i 2^(-M[i])
//
// where α_m ≈ 0.7213 / (1 + 1.079/m) for m ≥ 128 (Flajolet et al., Fig. 3).
//
// # Error bounds
//
// Let n be the true cardinality and Ê the estimate. The estimator's
// relative standard error is asymptotically:
//
//	σ(Ê)/n ≈ 1.04 / √m   (Flajolet et al., 2007, Theorem 1)
//
// This implies the following 95%-confidence (≈ 2σ) relative error bounds
// for common precisions:
//
//	  p   m=2^p   memory     std err   95% relative err   typical use
//	  --  ------  ---------  --------  ----------------   --------------
//	   8     256    256 B     6.50%       ~13%            tiny demos
//	  10    1024     1 KiB    3.25%       ~6.5%           coarse approx.
//	  12    4096     4 KiB    1.62%       ~3.2%           dashboards
//	  14   16384    16 KiB    0.81%       ~1.6%           default
//	  16   65536    64 KiB    0.41%       ~0.8%           high accuracy
//	  18  262144   256 KiB    0.20%       ~0.4%           audit-grade
//
// These bounds are large-cardinality asymptotics. For small n (linear-
// counting regime), the estimator switches to V * ln(m/V), which has its
// own bound: σ/n ≈ √(ln(m/n)/n) (Whang, Vander-Zanden, Taylor, 1990).
//
// # 64-bit hashes (HLL++ improvement)
//
// The original HLL paper uses 32-bit hashes and adds a "large-range
// correction" because hash collisions become frequent past n ≈ 10^9. With
// 64-bit hashes the collision probability for n ≤ 10^15 is negligible
// (<10^-3) and no large-range correction is required. We therefore use the
// raw HLL formula on the entire upper range. See Heule et al. §4 for the
// full collision analysis.
//
// # Sparse representation
//
// At low cardinality almost every register is zero. Storing a sorted map
// of just the non-zero registers is much smaller than the dense
// 2^p × 6-bit array. We start sparse and switch to dense once the sparse
// representation would exceed the dense size:
//
//	switch when |sparse map| × 5 bytes  >  m bytes
//
// That gives us roughly m/5 non-zero registers before transition, with no
// loss of accuracy (registers store identical values in both forms).
//
// # Property tests
//
// File hll_property_test.go runs each estimator against random inputs of
// known cardinality across many trials and asserts that the empirical
// 99th-percentile relative error stays within the theoretical bound times
// a small slack factor. Failures here mean either the implementation is
// wrong or the bound documented above is.
//
// # References
//
//   - Flajolet, P., Fusy, É., Gandouet, O., Meunier, F. (2007).
//     "HyperLogLog: the analysis of a near-optimal cardinality estimation
//     algorithm." DMTCS Proceedings.
//   - Heule, S., Nunkesser, M., Hall, A. (2013). "HyperLogLog in practice:
//     algorithmic engineering of a state of the art cardinality estimation
//     algorithm." EDBT '13.
//   - Whang, K.-Y., Vander-Zanden, B. T., Taylor, H. M. (1990).
//     "A linear-time probabilistic counting algorithm for database
//     applications." ACM TODS 15(2).
//
// # Limitations vs. HLL++ as published
//
// This package implements the core HLL++ improvements (64-bit hashing,
// sparse encoding) but does NOT yet ship the empirical bias-correction
// tables from Heule et al. §3.3, which improve accuracy in the
// 5m..5m·5 cardinality regime by ~1-2σ. Adding those tables (one per
// precision, sourced from the paper's appendix) is a roadmap item; see
// DESIGN.md. The current implementation falls back to linear counting
// below m·5/2 and pure HLL above, which is what the original 2007 paper
// prescribes. Estimates remain within the documented σ bounds at all
// cardinalities; you simply pay slightly looser constants in the small-n
// regime than a fully bias-corrected implementation would.
package hll
