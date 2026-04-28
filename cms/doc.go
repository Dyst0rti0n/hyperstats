// Package cms implements a Count-Min Sketch for streaming frequency
// estimation with rigorous (ε, δ) error guarantees.
//
// # Quick start
//
//	// Allow ε=0.1% additive error with probability ≥ 99%.
//	s := cms.NewWithGuarantees(0.001, 0.01)
//	s.AddString("error", 1)
//	s.AddString("error", 1)
//	s.AddString("info",  1)
//	got := s.CountString("error") // → 2 (or slightly more)
//
// # Theory
//
// A Count-Min Sketch (Cormode & Muthukrishnan, 2005) maintains a w×d
// matrix of counters. For each update (x, v) it increments d entries — one
// per row — using d pairwise-independent hashes:
//
//	for i = 1..d:    C[i, h_i(x)] += v
//
// The estimate for x is the minimum of those d counters:
//
//	f̂(x) = min_i C[i, h_i(x)]
//
// Because every counter touched by x has its true count plus the counts
// of any colliding elements, f̂(x) ≥ f(x) always: CMS never underestimates
// (assuming non-negative weights).
//
// # Error bound
//
// Let N = Σ_x f(x) be the total mass. For w = ⌈e/ε⌉ and d = ⌈ln(1/δ)⌉,
// the standard CMS bound (Cormode & Muthukrishnan, 2005, Theorem 1) is:
//
//	Pr[ f̂(x) - f(x)  ≤  ε · N ]  ≥  1 - δ     for every x
//
// In words: with probability at least 1 - δ, the over-estimate is at most
// ε × (total stream mass). A few worked examples for guidance:
//
//	ε      δ      width w   depth d   memory @ uint64
//	----   -----  --------  --------  ----------------
//	10%    1%     28        5         1.1 KiB
//	1%     1%     272       5         10.6 KiB
//	0.1%   1%     2719      5         106 KiB
//	0.1%   0.01%  2719      10        212 KiB
//	0.01%  0.001% 27183     14        2.96 MiB
//
// The bound is tight only when the stream is well-mixed across keys. With
// extreme skew (one key dominates), the absolute error εN can swamp the
// true counts of small keys; CMS is still useful for *heavy hitters* but
// poor for tail counts. See cms_property_test.go for an empirical study.
//
// # Hash construction
//
// We need d pairwise-independent hashes. Computing d full MurmurHash3
// values would be slow and unnecessary. Instead we use the
// Kirsch-Mitzenmacher (2008) double-hashing scheme:
//
//	h_i(x) = (h1(x) + i · h2(x)) mod w
//
// where (h1, h2) are the two halves of one 128-bit MurmurHash3 output.
// Kirsch and Mitzenmacher show this preserves the asymptotic error bound
// for Bloom filters; the same argument extends to CMS because both rely
// only on pairwise independence of the row hashes. Cost: one
// MurmurHash3_x64_128 per update, regardless of d.
//
// # Conservative Update
//
// A common practical optimisation is *Conservative Update*
// (Estan & Varghese, 2002): on Add, only increment counters whose current
// value equals the minimum of the d touched counters. CU has the same
// worst-case bound but typically cuts empirical error by 30-50%. It is
// NOT mergeable: merging two CU sketches violates the CU invariant. Use
// CU for monolithic counting, plain CMS when you need to merge shards.
//
// This package ships plain CMS in v0.1. CU is a roadmap item; see
// DESIGN.md.
//
// # References
//
//   - Cormode, G., Muthukrishnan, S. (2005). "An improved data stream
//     summary: the count-min sketch and its applications." Journal of
//     Algorithms 55(1).
//   - Kirsch, A., Mitzenmacher, M. (2008). "Less hashing, same performance:
//     building a better Bloom filter." Random Structures & Algorithms.
//   - Estan, C., Varghese, G. (2002). "New directions in traffic
//     measurement and accounting." SIGCOMM.
package cms
