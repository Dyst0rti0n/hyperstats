# Changelog

All notable changes to hyperstats will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `cmd/hyperdemo`: end-to-end demo binary that exercises every sketch
  against representative workloads, prints accuracy and throughput, and
  dumps CSVs for plotting.
- `scripts/plots.py`: matplotlib-based plot generator that turns the
  demo's CSVs into the graphs embedded in the README. Outputs to
  `docs/img/`.
- Makefile targets `make demo` and `make plots` for one-shot
  reproduction of the README graphs.
- `ROADMAP.md` documenting concrete plans for v0.2 (HLL++ bias tables,
  Conservative-Update CMS, weighted t-digest), v0.3 (Bloom, KMV, set
  operations), v0.4 (AVX2 hash, zero-alloc paths, concurrent
  wrappers), and v1.0 (API freeze) with acceptance criteria for each
  milestone.
- Top-level `doc.go` providing a meta-package overview that godoc
  renders at the repository root.
- `internal/testutil` package with shared property-test helpers.

## [0.1.0] - 2026-04-27

Initial release.

### Added

- `hash` package: pure-Go MurmurHash3_x64_128 (Sum128, Sum64, SumString),
  zero allocations, ~2 GB/s on 1 KiB inputs. Cross-verified against the
  canonical reference implementation via Python `mmh3`.
- `hll` package: HyperLogLog cardinality sketch with 64-bit hashes, sparse
  → dense register promotion, linear-counting fallback for small n,
  exact mergeability, versioned on-disk format with full validation on
  unmarshal. Documented standard error σ = 1.04/√m; empirically verified
  by property tests in the asymptotic regime.
- `cms` package: Count-Min Sketch with the standard (ε, δ) bound,
  Kirsch-Mitzenmacher double hashing, exact mergeability with overflow
  protection, row-sum-validated unmarshal. Property tests confirm the
  documented `Pr[f̂ - f ≤ εN] ≥ 1 - δ` bound holds with 0% violations.
- `kll` package: KLL quantile sketch (Karnin-Lang-Liberty 2016) with full
  exact mergeability, deterministic-seed support for tests, batched
  Quantiles for efficient multi-quantile queries. Documented per-query
  ε ≈ 1.66/k and sup-over-quantiles ε ≈ 5/k bounds, both empirically
  verified.
- `tdigest` package: merging-variant t-digest (Dunning 2019) with the
  arcsin scale function, asymmetric tail-biased accuracy profile,
  approximate mergeability across shards. Property tests confirm tail
  rank error well under 0.5% at δ=100 and below 0.01% at δ=500.
- `examples/`: three runnable demos — unique visitor counting (HLL),
  heavy-hitter detection (CMS + top-k tracker), and latency quantiles
  with side-by-side KLL vs t-digest comparison.
- Fuzz tests for every `UnmarshalBinary`. No panics found in 30 s of
  fuzzing per package; corpus committed under `testdata/fuzz/`.
- Benchmark suite covering Add, query, merge, and serialise paths.
- CI workflow on Linux/macOS/Windows × Go 1.22/1.23 with race detector
  and code coverage reporting.

### Notes

- HLL transition-regime bias (n/m ∈ (2, 5]) is *not* corrected in this
  release. Documented and pinned by `TestKnownTransitionBias`. HLL++
  empirical bias-correction tables are on the roadmap.
- CMS Conservative Update is on the roadmap as a separate non-mergeable
  type.
