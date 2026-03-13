# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] — 2026-03-13

Observability, correctness, and safety hardening across all allocators.

### Added

- **Stats.GrowEvents** — cumulative counter for backing-storage expansions (new block/chunk allocations beyond initial).
- **Stats.OOMs** — cumulative counter for `ErrOutOfMemory` rejections. `Stats` now has 9 fields (was 7).
- **Handle bit-packing enforcement** — runtime guards prevent silent data corruption when Handle field limits are exceeded:
  - `maxSlotIndex` (2^20 = 1,048,576 slots per chunk) — panics in `addChunk`.
  - `maxChunkIndex` (2^16 = 65,536 chunks per shard) — returns `ErrOutOfMemory` on growth.
  - Shard count ≤ 65,536 — panics in `NewSlab`.
- **Arena block reuse** — `Alloc` and `AllocSlice` now scan retained blocks after `Reset` before allocating new ones. Eliminates memory waste on repeated Reset/Alloc cycles.
- **Arena.EnsureCap + WithMaxBlocks** — growth path now respects `WithMaxBlocks` limit instead of allocating unbounded blocks.
- **statsCollector.resetActive** — internal method that adjusts `Frees` counter on bulk-free operations so `ActiveObjects` accurately reports zero.
- 20 new tests: lifecycle edge cases (OOM → Reset → Alloc, OOM → Free → Alloc), block reuse after reset, batch operations, stats counter accuracy, `EnsureCap` with `maxBlocks`, negative-size inputs.
- `BenchmarkSlabGet` — isolated Get (read) performance benchmark.
- `ExampleSlab_BatchAlloc` — runnable example for batch allocation API.
- GoDoc for all unexported types, methods, fields, and constants across every file.

### Fixed

- **Arena block reuse after Reset** — allocations always grew new blocks instead of reusing retained ones. Now blocks are scanned sequentially, matching the documented "blocks retained" contract.
- **Arena.EnsureCap ignores WithMaxBlocks** — growth loop had no `maxBlocks` check, allowing unbounded block allocation. Now breaks the loop at the configured limit.
- **ActiveObjects inaccurate after bulk-free** — `Stats.ActiveObjects` (Allocs − Frees) did not reset to zero after `Arena.Reset`, `Arena.EnsureCap`, `Arena.Release`, `Slab.Release`, or `Huge.Release`. Now calls `resetActive` at every bulk-free point.
- **Slab.lastFreeChunk hint cleared too eagerly** — the hint was unconditionally reset when any chunk was exhausted, even if the hinted chunk still had free slots. Now only clears when the exhausted chunk was the hinted one.
- **Huge.Free(nil/empty) silent no-op** — returned `nil` for empty slices instead of a clear error. Now returns `ErrInvalidHandle`.
- **doc.go line break** — mid-sentence line break in Safety section ("generation counters to detect / use-after-free") joined into a single sentence.
- **WithSlabCapacity GoDoc** — said "per slab chunk" but the capacity is total, split across shards. Corrected.

### Changed

- Test suite: 96 tests, 12 benchmarks, 6 examples (was 76 / 11 / 5).
- Test coverage: 94.2% main package, 100% internal/align.
- Test and benchmark naming audit: all names now follow `Test{Allocator}{Method}{Scenario}` / `Benchmark{Allocator}{Operations}` convention.
- `ErrTooLarge` GoDoc updated to "Reserved for future use."

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

[Unreleased]: https://github.com/aasyanov/slabix/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/aasyanov/slabix/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aasyanov/slabix/releases/tag/v0.1.0
