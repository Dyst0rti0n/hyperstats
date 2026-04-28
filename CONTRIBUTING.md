# Contributing to hyperstats

Thanks for considering a contribution. This document describes the process
and the standards.

## Ground rules

- **Property tests are load-bearing.** Every documented error bound has a
  property test that empirically verifies it. If you change a sketch, run
  the property tests; if you change a documented bound, update the
  property test in the same PR.
- **No new dependencies without justification.** The library is
  zero-dependency on purpose (see DESIGN.md D-009). If you need a third
  party package, open an issue first to discuss.
- **Public API is stable.** Pre-1.0, breaking changes go through a
  deprecation cycle of at least one minor release.

## Development workflow

```bash
# Clone and build
git clone https://github.com/Dyst0rti0n/hyperstats
cd hyperstats
go build ./...

# Run the full test suite (unit + property tests, race detector)
make test

# Run only unit tests (fast, ~2 seconds)
make test-short

# Run benchmarks
make bench

# Lint (requires golangci-lint installed)
make lint

# Format
make fmt

# Coverage report
make cover-html
```

## What to work on

The easiest contributions to land:
- Fixing a documented error bound that doesn't actually hold (file
  showing the violation as a property test).
- Improving error messages and docs.
- Adding more examples for real-world use cases.

The roadmap items in [ROADMAP.md](ROADMAP.md) are bigger contributions
that need design discussion in an issue first.

## Pull request checklist

- [ ] `make test` passes locally.
- [ ] `make lint` passes (or you've justified the exception).
- [ ] New code is covered by tests, including property tests for
      bound-affecting changes.
- [ ] Public-facing changes are reflected in `README.md`,
      `CHANGELOG.md`, and the package `doc.go`.
- [ ] Commit messages follow conventional commits (`feat:`, `fix:`,
      `docs:`, `test:`, `perf:`, `refactor:`).
- [ ] No new external dependencies, or design rationale documented.

## Reporting issues

See the issue templates. The most useful bug reports include:
- A minimal Go reproducer.
- The sketch and parameters used.
- The expected behaviour and the observed behaviour.
- Ideally, a property test (or extension to an existing one) that
  fails.

## License

By contributing you agree your contribution is licensed under
Apache-2.0, the same license as the project.
