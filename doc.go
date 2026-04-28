// Package hyperstats provides production-grade streaming sketch algorithms
// with mathematically rigorous error bounds and empirical verification via
// property tests.
//
// hyperstats itself is a meta-package — each algorithm lives in its own
// sub-package and can be imported independently:
//
//	import "github.com/dystortion/hyperstats/hll"      // HyperLogLog
//	import "github.com/dystortion/hyperstats/cms"      // Count-Min Sketch
//	import "github.com/dystortion/hyperstats/kll"      // KLL quantile sketch
//	import "github.com/dystortion/hyperstats/tdigest"  // merging t-digest
//	import "github.com/dystortion/hyperstats/hash"     // MurmurHash3 (used by hll, cms)
//
// # Choosing a sketch
//
//	question                                         use this
//	----------------------------------------------   --------
//	how many distinct X are in this stream?          hll
//	how many times did key K appear?                 cms
//	what's the p50/p95/p99 of these values?          kll or tdigest
//	  ...with worst-case bounded rank error?         kll
//	  ...with the best tail (p99/p99.9) accuracy?    tdigest
//
// # Common API shape
//
// Every sketch in hyperstats follows the same pattern:
//
//   - Construct with a New variant (e.g. hll.New(precision),
//     cms.NewWithGuarantees(eps, delta)).
//   - Update with Add or AddString (string-keyed convenience).
//   - Query with Estimate, Count, Quantile, Rank, etc.
//   - Combine with Merge (every sketch is mergeable).
//   - Persist with MarshalBinary / UnmarshalBinary. UnmarshalBinary
//     validates and returns errors; it never panics on malformed input.
//
// # Documented and tested error bounds
//
// Every sketch ships with documented error bounds matching the original
// papers, and a corresponding property test that empirically verifies
// those bounds across many trials. If an implementation regresses, the
// property tests catch it; if a documented bound is too tight for some
// regime, the property tests catch that too.
//
// See the package-level documentation in each sub-package for the
// specific bounds, and DESIGN.md at the repository root for the
// rationale behind each design choice.
package hyperstats
