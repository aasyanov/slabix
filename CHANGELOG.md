# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] — 2026-03-10

Initial public release.

### Added

- **Arena[T]** — bump-pointer allocator with bulk reset, per-block growth, and `EnsureCap` pre-sizing.
- **Slab[T]** — fixed-size object pool with per-shard freelist, `Handle[T]` typed references, and generation-based use-after-free detection.
- **Huge** — large byte buffer allocator with `sync.Pool`-backed size-class reuse (64 KB – 16 MB).
- `Handle[T]` opaque typed reference with packed chunk/index/generation fields.
- Functional options for all allocators: `WithBlockSize`, `WithMaxBlocks`, `WithGrowable`, `WithSlabCapacity`, `WithShards`, `WithSlabGrowable`, `WithGrowthPolicy`, `WithMaxChunks`, `WithBatchHint`, `WithAlignment`, `WithPoolReuse`.
- Three growth policies: `GrowFixed`, `GrowLinear`, `GrowAdaptive` — all capped to prevent runaway memory consumption.
- `BatchAlloc` / `BatchFree` for Slab — batch operations under single lock acquisition.
- Lock-free atomic `Stats` with 7 counters: `Allocs`, `Frees`, `ActiveObjects`, `BytesAllocated`, `BytesInUse`, `BlockCount`, `Resets`.
- 5 sentinel errors: `ErrClosed`, `ErrOutOfMemory`, `ErrDoubleFree`, `ErrInvalidHandle`, `ErrTooLarge`.
- `internal/align` package for cache-line-aware memory alignment (`unsafe`, internal only).
- Comprehensive test suite: 76 tests, 11 benchmarks, 5 runnable examples.
- Test coverage: 96.6% of statements.
- All tests pass with `-race` detector on Windows/amd64.
- GitHub Actions CI: lint (golangci-lint), test (race + 90% coverage gate), benchmark.
- GoDoc documentation for all exported symbols.
- Zero external dependencies (stdlib only).

### Dependencies

- Go 1.24+

[Unreleased]: https://github.com/aasyanov/slabix/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/aasyanov/slabix/releases/tag/v0.1.0
