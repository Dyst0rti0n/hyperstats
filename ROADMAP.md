# hyperstats Roadmap

This document is the living plan for hyperstats releases. Each item lists
what it is, why we want it, the design references, and concrete acceptance
criteria so it's clear when the work is done.

Items are organised by target release, but priority can shift based on
real-world feedback. For each milestone the goal is **functional + tested
+ documented + benchmarked**, not just merged.

---

## v0.2 — accuracy round

The headline of v0.2 is closing the documented accuracy gaps from v0.1
without breaking the public API.

### v0.2.1 — HLL++ empirical bias-correction tables

**Why**: v0.1 has a documented +1.5% bias in the n/m ∈ (2, 5] transition
regime (`TestKnownTransitionBias`). HLL++ (Heule, Nunkesser, Hall 2013
§3.3) ships precision-specific lookup tables that remove this bias and
typically tighten σ by ~1.5× in the small-n regime.

**Plan**:
1. Source the bias and raw-estimate tables from the HLL++ paper appendix
   (one pair per precision p ∈ [4, 18]) into `hll/biastables.go`.
2. Verify table integrity by reproducing the paper's "raw → corrected"
   mapping in a unit test before wiring it into `Estimate()`.
3. In `Estimate()`, when raw is in the (2.5m, 5m] range, do a 1-D
   linear interpolation against the precision's table to compute the
   corrected estimate.
4. Tighten `TestKnownTransitionBias` to assert |bias| ≤ 0.5% and RMSE
   ≤ 1.5%; the test is already in place as a regression detector.

**Acceptance**:
- The transition-regime test passes with the new tighter bound.
- Asymptotic-regime property tests still pass.
- Memory footprint of the tables (≤ 200 KiB total, embedded as `var
  []float64` in code, not data files) is documented in `MemoryBytes()`.

### v0.2.2 — Conservative Update CMS variant

**Why**: CU (Estan & Varghese 2002) reduces empirical error by 30-50%
on skewed streams without changing the worst-case bound. Many
production CMS deployments use it.

**Plan**:
1. Add `cms.SketchCU` as a separate type (NOT a mode flag on
   `cms.Sketch`) since CU sketches are not mergeable.
2. The CU update rule: only increment counters whose current value
   equals min over all touched counters.
3. Property test: on a Zipfian stream, CU's max-error must be strictly
   smaller than plain CMS's max-error in 95%+ of trials.
4. Document the no-merge limitation in big bold red letters at the
   top of `cms/cu.go` doc.

**Acceptance**:
- New test file `cms/cu_property_test.go` shows CU dominates plain CMS
  on three skewed-distribution benchmarks.
- `cms.SketchCU` does not expose a `Merge` method (compile-time
  guarantee, not just runtime error).

### v0.2.3 — t-digest weighted Add improvements

**Why**: v0.1 t-digest accepts `AddWeighted(x, w)` but doesn't optimise
for sparse-but-heavy weighted streams (e.g. session durations where
each session has weight equal to event count). The buffer flush
currently treats every entry as an unsorted insert; weighted entries
can be merged in-place at insertion time.

**Plan**:
1. Add a fast path in `flush()` that detects weighted entries and uses
   them as merge anchors, reducing centroid count by ~30% for typical
   weighted workloads.
2. Property test on a weighted Pareto stream.

**Acceptance**:
- Throughput on weighted workloads increases by ≥ 1.5×.
- Quantile accuracy unchanged on uniform-weight workloads.

---

## v0.3 — new sketches

### v0.3.1 — Bloom filter (`bloom`)

**Why**: Approximate set membership is the natural companion to CMS for
"have I seen this?" before "how many of this?" workflows.

**Plan**:
- Standard Bloom filter with k = ⌈(m/n) · ln(2)⌉ hashes via
  Kirsch-Mitzenmacher double hashing (reuses `hash` package).
- Variants: classic Bloom and counting Bloom (supports deletion).
- Documented false-positive rate (1 - e^(-kn/m))^k.

**Acceptance**:
- Property test: empirical FP rate within ±20% of theoretical.
- Mergeable for non-counting variant.
- Compatible with `bloom.Sketch` round-trip serialisation.

### v0.3.2 — HyperLogLog set operations

**Why**: HLL supports inclusion-exclusion via merge: |A∪B| − |A| − |B|
gives a (rough) estimate of |A∩B|. We can expose this as a top-level
helper.

**Plan**:
- `hll.Intersection(a, b *Sketch) uint64` returning the IE estimate.
- `hll.Jaccard(a, b *Sketch) float64` returning |A∩B| / |A∪B|.
- Document the catastrophic-cancellation regime (when a and b are
  nearly disjoint, IE produces large negative values that we clamp to
  zero with a documented loss of accuracy).

**Acceptance**:
- Property test: Jaccard estimate within ε = 0.05 of truth on
  controlled overlap experiments.

### v0.3.3 — KMV (k-Minimum-Values) sketch

**Why**: KMV is the asymptotically-optimal cardinality sketch (better
constants than HLL at the cost of slightly larger memory) and supports
exact set operations. Useful when set algebra matters more than
absolute memory.

**Plan**:
- Standard k-MV with min-heap; mergeable; serialisable.
- Comparison benchmark vs HLL at matched memory budgets.

**Acceptance**:
- Documented ε = 1/√k bound; verified by property test.
- Memory overhead vs HLL documented in DESIGN.md.

---

## v0.4 — performance round

### v0.4.1 — AVX2-accelerated MurmurHash3 for inputs ≥ 1 KiB

**Why**: For workloads dominated by long-key hashing (URL paths, JSON
payloads), MurmurHash3's scalar throughput is ~2 GB/s. AVX2 can hit
~6 GB/s on the body loop.

**Plan**:
- Add `hash/asm_amd64.s` with an AVX2 body-loop intrinsic for inputs
  ≥ 1 KiB; fall back to the scalar path for shorter inputs and other
  architectures.
- Build tag `hyperstats_pure_go` lets users opt out.

**Acceptance**:
- Benchmark `BenchmarkSum128_1KiB` shows ≥ 2× throughput on AVX2 hosts.
- Bit-exact output vs scalar implementation across the SMHasher
  vectors and a 100M-input fuzz comparison.

### v0.4.2 — Pool-based zero-allocation hot paths

**Why**: HLL `AddString` currently allocates 7-8 bytes per call (from
the string-to-bytes conversion path); for a million-events-per-second
service this is non-trivial GC pressure.

**Plan**:
- Refactor `AddString` to compute the hash directly from the string
  without going through `[]byte`, using `unsafe` for the read-only
  conversion (well-established Go pattern).
- Audit all hot paths with `-benchmem` and target zero allocations.

**Acceptance**:
- All Add* benchmarks report 0 B/op, 0 allocs/op.

### v0.4.3 — Concurrent sketch wrappers

**Why**: v0.1 sketches are not thread-safe; users must synchronise
externally. A canned wrapper that does this correctly (sharded for
contention reduction, periodic merge) is high-value.

**Plan**:
- New package `hyperstats/concurrent` with `concurrent.HLL`,
  `concurrent.CMS`, etc.
- Each wrapper holds N (default 8) per-shard sketches plus a
  read-write mutex for queries that triggers a transient merge.
- Document: this is *higher* throughput at the cost of *higher*
  memory (8× the underlying sketch).

**Acceptance**:
- Race-detector clean under chaos test (1000 goroutines, mixed Add and
  Estimate).
- Throughput ≥ 5× single-sketch on 8-core hardware.

---

## v1.0 — API stability

**Why**: Library is used in enough places that breaking changes have
real cost. v1.0 freezes the public API.

**Plan**:
1. Survey the issue tracker for any rough edges before lock-in.
2. Pass the API through `go-api-check` to catch breakage windows.
3. Run a final round of fuzzing for ≥ 1 hour per UnmarshalBinary.
4. Audit all public docs for drift vs implementation.
5. Tag v1.0.0; commit to semver from there.

**Acceptance**:
- Release notes documenting every public type/function and its
  guarantees.
- ≥ 95% test coverage across all packages.
- ≥ 90% documentation coverage (every exported symbol has a godoc
  paragraph, not a one-liner).

---

## Speculative / nice-to-have

These are tracked but not scheduled.

- **DDSketch** (Datadog): relative-error quantile sketch with strong
  bounds for log-normal data.
- **REQ Sketch**: relative-error quantile sketch from Apache
  DataSketches.
- **HLL++ sparse encoding** (the *encoded* sparse format using
  difference encoding + variable-length ints, not just the in-memory
  map): cuts wire-format size by ~3× for low-cardinality sketches.
- **Tail-biased KLL variants** for use cases where the tails matter
  more than the median.
- **GPU-accelerated batch insertion** via CUDA or compute shaders for
  offline analytics.
