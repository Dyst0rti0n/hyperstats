# hyperstats Design Decisions

This document records the non-obvious design choices in hyperstats and the
reasoning behind them. The goal is that a future contributor (or future-me)
can understand *why* the code is shaped the way it is — not just *what* it does.

## D-001: MurmurHash3, not xxhash or cityhash

**Decision**: Hash inputs with a pure-Go port of MurmurHash3_x64_128.

**Alternatives considered**:
- `xxhash` (Cloudflare): ~10× faster than Murmur for large inputs.
- `cityhash` (Google): comparable speed, more code.
- `siphash`: cryptographically hardened against collision attacks, ~3× slower.
- `blake3`: cryptographically secure but ~50× more expensive.

**Why MurmurHash3 won**:
1. **Sketch literature canonical choice.** All the bound proofs cited in
   the package docs assume Murmur or an equivalent hash with strong
   avalanche and uniform distribution. Switching to xxhash would require
   re-running every property test to confirm the bounds still hold;
   while xxhash *probably* satisfies them, "probably" is not what
   production claims should rest on.
2. **Well-studied via SMHasher.** Murmur passes Appleby's full
   avalanche, distribution, and collision suite. xxhash/cityhash do too,
   but Murmur has more sketch-specific empirical track record.
3. **Speed is sufficient.** ~2 GB/s on this hardware is much faster than
   sketch update rates (10s of M ops/sec for a string keyed sketch). The
   hash is not the bottleneck; map allocation, sort, and arithmetic are.
4. **Zero external dependencies.** Pure-Go vendor-free.

**Reconsider when**: Sketch-update throughput becomes a bottleneck for a
real workload, *and* the property tests can be re-baselined for the new
hash. Don't swap silently.

## D-002: No HLL++ bias-correction tables in v0.1

**Decision**: Ship raw HLL with linear-counting fallback. Defer the
empirical bias-correction tables from Heule et al. §3.3 to a later release.

**Cost of the decision**: A documented +1.5% positive bias bump in the
n/m ∈ (2, 5] cardinality range (the "transition regime"). Outside that
window the documented σ = 1.04/√m bound is empirically tight.

**Why defer**:
- The bias-correction tables in the paper are precision-specific and
  span ~200 data points per precision. Sourcing them, embedding them,
  and verifying their correctness is substantial work.
- The asymptotic bound at n > 5m is already excellent and matches what
  most users actually experience. The transition regime affects a
  narrow band of cardinalities.
- Shipping the basic implementation first lets the property test
  framework prove its value before we add complexity.

**How we keep ourselves honest**: `TestKnownTransitionBias` in the hll
package pins the current bias as a regression detector. When someone
adds bias tables, this test will start passing more tightly and the
PR can update the bound.

## D-003: Plain CMS, not Conservative Update

**Decision**: Ship the canonical Cormode-Muthukrishnan CMS for v0.1.
Defer Conservative Update (Estan & Varghese 2002) to v0.2.

**Cost**: Empirical error in plain CMS is typically 30-50% larger than
Conservative Update at the same dimensions.

**Why defer**: CU is **not mergeable**. Two CU sketches built from the
same multiset can have different counter values that, when added
cell-wise, violate the CU invariant. The "merge across shards" use case
is the most important practical motivation for CMS in distributed
systems, so we ship the mergeable variant as the default.

**Plan for CU**: Add `cms.SketchCU` as a separate type with the same
API minus `Merge`, with prominent documentation that two CU sketches
cannot be combined.

## D-004: KLL ε constants

**Decision**: Document two empirical constants for KLL:
- per-query: ε_q ≈ 1.66/k
- sup-over-quantiles: ε_sup ≈ 5/k

**Why two**: The Karnin-Lang-Liberty paper proves a "for all q" bound,
but most users querying a single quantile get a much tighter empirical
error than the sup. Documenting just the sup overestimates error for
common use cases (latency dashboards usually only care about p50/p99/p99.9).
Documenting just the per-query bound undersells the cost for users who
build a histogram of 100 quantile probes.

**How we set 5/k**: Empirical study at k ∈ {100, 200, 800} on uniform
inputs over 99 quantile probes (the property test
`TestRankErrorBoundEmpirical`). 5/k holds with margin across that range;
the actual constant trends slightly higher at smaller k (orphan-item
overhead dominates) and slightly lower at larger k. The 5/k figure is
conservative — pinned to never flake on CI.

**Reconsider when**: A user reports their workload exceeds the ε_sup
bound. Either the bound is wrong for their distribution and needs
tightening, or there's a bug in the implementation. Property tests
will catch the latter.

## D-005: Merging t-digest, not clustering

**Decision**: Implement the merging variant from Dunning's 2019 rewrite,
not the original clustering variant.

**Why**:
- Mergeable across shards (the clustering variant is technically
  mergeable but with worse properties).
- Simpler to implement correctly: one sort + linear sweep per flush.
- Dunning's paper recommends it as the default.

**Trade-off**: Slightly higher memory overhead from the buffer (5×δ)
between flushes. Worth it.

## D-006: All UnmarshalBinary returns errors, never panics

**Decision**: Every `UnmarshalBinary` validates inputs and returns
`error` for every malformed case. Never `panic`.

**Why**: Sketches travel across trust boundaries:
- Cross-service merge in distributed systems.
- Persisted to disk by service A, read by service B.
- Replicated across regions.

A `panic` on malformed input is a denial-of-service vector: an attacker
who can submit bytes to the unmarshal path can crash the receiving
process.

**What we validate**:
- Magic bytes / version
- Dimension parameters in their valid ranges
- Per-element validity (non-NaN, weight > 0, indices < m, etc.)
- Cross-element invariants (sorted means in t-digest, weighted sum equals
  stored N in KLL, row-sum equals total in CMS)

**What we do not validate**: We do not verify cryptographic integrity.
Callers needing integrity should wrap the bytes with HMAC before
transit. UnmarshalBinary defends against accidental corruption, not
against an adversary who has rewritten the bytes.

## D-007: On-disk format versioning

**Decision**: Every serialised format starts with magic bytes followed
by a single-byte version. Magic bytes and version are checked first
before any other parsing.

**Magic bytes used**:
- HLL: `'H'` (0x48)
- CMS: `'C', 'M', 'S'`
- KLL: `'K', 'L', 'L'`
- t-digest: `'T', 'D', 'G'`

**Forward compatibility**: Each `UnmarshalBinary` rejects unknown
versions. To add a v2 format, ship code that handles both v1 and v2,
let consumers upgrade, then later deprecate v1.

**Backward compatibility**: We will *not* break v1 readers without a
deprecation cycle of at least one minor release.

## D-008: Property tests as load-bearing infrastructure

**Decision**: Every documented error bound has at least one property
test that empirically verifies it across many random trials.

**Why this matters more than usual**: The point of a sketch library is
to provide *bounded approximations*. The bound is the spec; the
implementation just has to satisfy it. If the bound documented in the
README doesn't match what the code actually delivers, the library is
worse than useless — it's a quiet bug factory in someone's monitoring
stack.

**How property tests are structured**:
1. Document the bound in the package doc.go.
2. Write a test that runs T trials at K different parameter
   configurations.
3. Assert that the empirical error distribution is within the documented
   bound (with statistical slack to avoid CI flakes).
4. Log the actual numbers so a regression is visible even if the test
   passes.

When a property test fails:
- Either the implementation regressed (fix the code).
- Or the documented bound is too tight for some k (loosen the bound *and*
  update the doc — never just the test).

## D-009: Zero external dependencies

**Decision**: The library and tests have *no* dependencies outside the
Go standard library.

**Why**: Sketch libraries get embedded in critical-path infrastructure
(metrics pipelines, fraud detection, billing). Every transitive
dependency is a supply-chain risk and a future maintenance burden.

**Rejected dependencies**:
- `github.com/cespare/xxhash` for hashing (we ship MurmurHash3 inline).
- `github.com/stretchr/testify` for assertions (Go's `t.Errorf` is fine).
- Any benchmarking framework (Go's `testing.B` suffices).
