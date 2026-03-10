# slabix — High-Performance Memory Allocators

[![CI](https://github.com/aasyanov/slabix/actions/workflows/ci.yml/badge.svg)](https://github.com/aasyanov/slabix/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aasyanov/slabix.svg)](https://pkg.go.dev/github.com/aasyanov/slabix)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Typed, generic memory allocators for Go 1.24+. Zero external dependencies.

```
go get github.com/aasyanov/slabix
```

## Overview

slabix provides three allocator types — Arena, Slab, and Huge — that cover distinct allocation patterns in systems where GC pressure matters: databases, write-ahead logs, caches, and message brokers.

**Not** a garbage collector replacement, a `malloc`, or a framework. Each allocator is independently usable.

## Architecture

```
NewArena[T]()  ──► block₀ ──► block₁ ──► block₂ ──► ...
                   bump ptr    bump ptr    bump ptr
                   ───────────────────────────────────►
                   Alloc() O(1)        Reset() O(1)

NewSlab[T]()   ──► shard₀ ──────────── shard₁ ──────────── ...
                   chunk₀  chunk₁       chunk₀  chunk₁
                   [free→free→free]     [free→free→free]
                   Alloc() → Handle   Free(h) → freelist

NewHuge()      ──► map[ptr]entry ──► sync.Pool[class₀..class₈]
                   Alloc(size) → []byte    Free(buf) → pool reuse
```

| Allocator | Pattern | Use Case |
|---|---|---|
| **Arena[T]** | Bump-pointer, bulk reset | Per-request scratch, WAL batches, parser buffers |
| **Slab[T]** | Fixed-size freelist, per-shard | Tree nodes, cache entries, index records |
| **Huge** | Per-allocation with pool reuse | Large buffers (> 64 KB), compaction pages |

## Quick Start

### Arena — Bulk Allocation

```go
arena := slabix.NewArena[Entry](
    slabix.WithBlockSize(4 * 1024 * 1024),
)
defer arena.Release()

entry, _ := arena.Alloc()
entry.Key = "foo"

batch, _ := arena.AllocSlice(128)
// use batch...

arena.Reset() // free everything at once
```

### Arena — Pre-sized Cycles

```go
arena := slabix.NewArena[Node]()
defer arena.Release()

for _, input := range inputs {
    arena.EnsureCap(len(input) * 3) // reset + ensure capacity
    for i := 0; i < len(input)*3; i++ {
        node, _ := arena.Alloc()
        // ...
    }
}
```

### Slab — Object Pool

```go
pool := slabix.NewSlab[TreeNode](
    slabix.WithSlabCapacity(8192),
    slabix.WithShards(runtime.GOMAXPROCS(0)),
    slabix.WithGrowthPolicy(slabix.GrowAdaptive),
    slabix.WithMaxChunks(64),
)
defer pool.Release()

h, _ := pool.Alloc()
node := pool.Get(h)
node.Key = 42

pool.Free(h) // individual free
```

### Huge — Large Buffers

```go
huge := slabix.NewHuge(
    slabix.WithAlignment(64),
    slabix.WithPoolReuse(true),
)
defer huge.Release()

buf, _ := huge.Alloc(128 * 1024)
// use buf...
huge.Free(buf) // returned to size-class pool for reuse
```

## Public API

### Arena[T]

| Method | Description |
|---|---|
| `NewArena[T](opts...)` | Create a new arena allocator |
| `Alloc() (*T, error)` | Allocate a single zeroed object |
| `AllocSlice(n) ([]T, error)` | Allocate a contiguous slice of n objects |
| `EnsureCap(n)` | Ensure capacity for n objects, reset bump pointer |
| `Reset()` | Free all objects, retain backing memory |
| `Release()` | Free all objects and backing memory |
| `Stats() Stats` | Point-in-time statistics snapshot |
| `Cap() int` | Total capacity across all blocks |
| `Len() int` | Number of allocated objects |

### Slab[T]

| Method | Description |
|---|---|
| `NewSlab[T](opts...)` | Create a new slab pool |
| `Alloc() (Handle[T], error)` | Allocate a single object, return handle |
| `Get(Handle[T]) *T` | Dereference a handle to a pointer |
| `Free(Handle[T]) error` | Return an object to the pool |
| `BatchAlloc(n) ([]Handle[T], error)` | Allocate n objects under one lock |
| `BatchFree([]Handle[T]) error` | Free multiple objects |
| `Release()` | Release all memory |
| `Stats() Stats` | Point-in-time statistics snapshot |
| `Cap() int` | Total slot capacity across all shards |
| `Len() int` | Number of live (allocated) objects |

### Huge

| Method | Description |
|---|---|
| `NewHuge(opts...)` | Create a new huge allocator |
| `Alloc(size) ([]byte, error)` | Allocate a zeroed byte buffer |
| `Free([]byte) error` | Free a buffer (returned to pool if enabled) |
| `Release()` | Release all tracked allocations |
| `Stats() Stats` | Point-in-time statistics snapshot |
| `Len() int` | Number of live allocations |

### Handle[T]

| Method | Description |
|---|---|
| `IsZero() bool` | Reports whether h is the zero handle (never allocated) |

## Configuration

All configuration is via functional options passed to constructors:

### Arena Options

| Option | Default | Description |
|---|---|---|
| `WithBlockSize(n)` | 4 MB | Size of each arena block in bytes |
| `WithMaxBlocks(n)` | 0 (unlimited) | Maximum number of blocks |
| `WithGrowable(bool)` | true | Whether to allocate new blocks on exhaustion |

### Slab Options

| Option | Default | Description |
|---|---|---|
| `WithSlabCapacity(n)` | 4096 | Initial objects per slab chunk |
| `WithShards(n)` | 1 | Number of independent shards |
| `WithSlabGrowable(bool)` | true | Whether to grow on exhaustion |
| `WithGrowthPolicy(p)` | `GrowAdaptive` | How new chunks are sized |
| `WithMaxChunks(n)` | 0 (unlimited) | Max chunks per shard (backpressure) |
| `WithBatchHint(n)` | 64 | Expected batch size hint |

### Huge Options

| Option | Default | Description |
|---|---|---|
| `WithAlignment(n)` | 64 | Byte alignment (must be power of two) |
| `WithPoolReuse(bool)` | true | Reuse freed buffers via size-class pools |

### Growth Policies

| Policy | Behavior |
|---|---|
| `GrowFixed` | Every chunk same size as initial capacity |
| `GrowLinear` | Each chunk adds initial capacity (cap, 2*cap, 3*cap) |
| `GrowAdaptive` | Doubles until ceiling, then fixed (default) |

No policy allows exponential growth beyond the ceiling (max of initial capacity or 4096). This prevents the runaway memory consumption that plagues naive doubling strategies.

## Memory Management Patterns

### Arena: Alloc-Reset Cycle

Arena objects are never individually freed. The entire arena is reset at once:

```
Batch start → arena.Alloc() × N → Batch commit → arena.Reset()
```

Backing memory is retained across resets — no GC involvement.

### Slab: Individual Alloc/Free with Reuse

Freed objects return to the chunk's freelist. Subsequent allocations reuse freed slots before growing new chunks:

```
Alloc → use → Free → slot returns to freelist → next Alloc reuses it
```

### Huge: Size-Class Pool Reuse

Freed buffers are returned to internal `sync.Pool`-backed size-class pools (64 KB to 16 MB, powers of two). Subsequent allocations of similar size reuse pooled buffers instead of allocating from the heap.

## Observability

Every allocator exposes a `Stats()` method returning a `Stats` snapshot:

```go
type Stats struct {
    Allocs         uint64 // total allocations
    Frees          uint64 // total frees
    ActiveObjects  uint64 // allocs - frees
    BytesAllocated uint64 // total backing memory reserved
    BytesInUse     uint64 // memory occupied by live objects
    BlockCount     uint64 // number of backing blocks/chunks
    Resets         uint64 // bulk resets (arena only)
}
```

All counters are maintained with atomic operations — zero overhead on the hot path. Safe to call from any goroutine.

## Errors

5 sentinel errors, all comparable with `==` and `errors.Is`:

| Error | Condition |
|---|---|
| `ErrClosed` | Operation on a released allocator |
| `ErrOutOfMemory` | Capacity exhausted, growth disabled or max chunks reached |
| `ErrDoubleFree` | Handle or buffer freed twice |
| `ErrInvalidHandle` | Handle from wrong allocator or stale generation |
| `ErrTooLarge` | Allocation exceeds maximum size |

## Safety

- All memory is **GC-visible** — no `mmap` or `cgo` allocations
- `unsafe` is confined to `internal/align` and `sliceKey` — never leaks through the public API
- Handles carry **generation counters** to detect use-after-free
- Double-free returns a clear error instead of corrupting state
- **No exponential growth** — all growth policies are capped
- **Backpressure** via `WithMaxChunks` prevents unbounded memory consumption

## Benchmark Results

Measured on Intel Core i7-10510U @ 1.80GHz, Windows 10, Go 1.26. Values are medians from 5 runs (`-count=5`), measured in a clean terminal outside the IDE to avoid editor overhead.

### Arena

| Benchmark | Latency | B/op | Allocs/op |
|---|---|---|---|
| ArenaAlloc (single object) | 26 ns/op | 23 | 0 |
| ArenaAllocSlice (64 objects) | 254 ns/op | 1536 | 0 |
| ArenaAllocResetCycle (1000 allocs + reset) | 25,190 ns/op | 0 | 0 |
| ArenaAllocParallel (8 goroutines) | 84 ns/op | 23 | 0 |

### Slab

| Benchmark | Latency | B/op | Allocs/op |
|---|---|---|---|
| SlabAlloc (single object) | 33 ns/op | 47 | 0 |
| SlabAllocFree (alloc + free cycle) | 55 ns/op | 0 | 0 |
| SlabBatchAlloc (128 objects + free) | 5,970 ns/op | 1024 | 1 |
| SlabAllocParallel (alloc+free, 8 goroutines) | 193 ns/op | 0 | 0 |
| SlabAllocFreeParallel (alloc+get+free, 8 goroutines) | 213 ns/op | 0 | 0 |

### Huge

| Benchmark | Latency | B/op | Allocs/op |
|---|---|---|---|
| HugeAlloc (128 KB, pool reuse) | 2,489 ns/op | 32 | 1 |
| HugeAllocParallel (128 KB, 8 goroutines) | 1,497 ns/op | 42 | 1 |

### Analysis

**Arena**: Single-object alloc achieves ~38M ops/sec with zero heap allocations. The bump pointer is O(1) — just an index increment and a mutex (uncontended fast path). `AllocSlice` amortizes lock overhead across 64 objects. The alloc-reset cycle (1000 objects + reset) completes in ~25 us with zero GC pressure since backing memory is retained. Parallel throughput reaches ~12M ops/sec across 8 goroutines.

**Slab**: Single alloc/free cycle completes in ~55 ns with zero heap allocations thanks to the freelist-based slot reuse. `BatchAlloc` performs all 128 allocations under a single lock acquisition (1 alloc for the handle slice). Parallel throughput with 8 shards reaches ~5.2M alloc+free ops/sec.

**Huge**: Pool reuse avoids 128 KB heap allocations — freed buffers are returned to `sync.Pool`-backed size-class pools and reused on subsequent allocations of similar size. Parallel throughput nearly doubles thanks to reduced GC pressure from pool hits.

**Memory efficiency**: Arena and Slab hot paths produce zero heap allocations (`0 allocs/op`). The only heap allocation in Slab is the handle slice in `BatchAlloc`. Huge produces 1 alloc/op (the pool pointer wrapper), but the 128 KB buffer itself comes from the pool.

## Quality

| Metric | Value |
|---|---|
| Test functions | 76 |
| Benchmarks | 11 |
| Examples | 5 |
| Coverage | 96.6% |
| Race detector | All tests pass with `-race` |
| Go version | 1.24 |
| External deps | 0 (stdlib only) |

All tests pass with `-race` detector enabled on Windows/amd64.

Uncovered code (~3.4%) consists of defensive guards for zero-size type parameters (`unsafe.Sizeof(*new(T)) == 0`) and slab growth policy default-case fallbacks that are unreachable with the defined `GrowthPolicy` constants.

## File Structure

```
slabix/
├── doc.go              # Package documentation
├── arena.go            # Arena[T] bump-pointer allocator
├── slab.go             # Slab[T] fixed-size freelist allocator
├── huge.go             # Huge byte buffer allocator with pool reuse
├── handle.go           # Handle[T] typed reference
├── options.go          # Functional options (WithXxx), GrowthPolicy
├── stats.go            # Stats snapshot + atomic collector
├── errors.go           # 5 sentinel errors
├── internal/
│   └── align/
│       └── align.go    # Alignment helpers (unsafe, internal only)
├── *_test.go           # Unit tests (76 tests, 11 benchmarks)
├── example_test.go     # Runnable examples (5)
└── go.mod              # Zero dependencies
```

## License

MIT
