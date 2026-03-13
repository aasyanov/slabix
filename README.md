# slabix — High-Performance Memory Allocators for Go

[![CI](https://github.com/aasyanov/slabix/actions/workflows/ci.yml/badge.svg)](https://github.com/aasyanov/slabix/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aasyanov/slabix.svg)](https://pkg.go.dev/github.com/aasyanov/slabix)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Typed, generic memory allocators for Go 1.24+. Zero external dependencies.

```
go get github.com/aasyanov/slabix
```

## The Problem

Go's garbage collector is efficient, but allocation patterns determine how much work it has to do. In high-throughput systems — databases, WAL engines, caches, message brokers — uncontrolled `make()` and `new()` calls on every request produce millions of short-lived objects per second. Each one is tracked by the GC, and the resulting scan/mark/sweep work translates directly to tail latency spikes.

The root cause is not the GC itself, but the allocation pattern. If objects are scattered across the heap with unpredictable lifetimes, the collector must do proportionally more work. If objects are grouped by lifetime and reused, the collector sees fewer allocations and shorter pause times.

slabix provides three allocators, each designed for a specific allocation pattern. The goal is to move allocation-intensive hot paths from scattered heap allocations to structured, reusable memory pools — reducing GC pressure without changing application logic.

## Why Three Allocators

Every allocation falls into one of three patterns:

| Pattern | Lifetime | Free model | Example |
|---|---|---|---|
| **Batch/scratch** | Short, bounded | All at once | Request context, parse buffers, WAL batches |
| **Object pool** | Long, individual | One by one | Tree nodes, cache entries, index records |
| **Large buffer** | Variable | One by one | Compaction pages, bulk I/O, WAL segments |

A single "universal" allocator would be suboptimal for all three: batch allocators waste memory on freelists, freelist pools waste time on bulk resets, and large buffers fragment both. slabix provides one allocator per pattern:

- **Arena[T]** — bump-pointer allocator for batch/scratch memory
- **Slab[T]** — fixed-size freelist pool for individual object lifecycle
- **Huge** — large byte buffer allocator with size-class pool reuse

### Generics

Arena and Slab use Go generics (`[T any]`) for compile-time type safety. This eliminates `interface{}` boxing and type assertions on the allocation hot path. The compiler knows `sizeof(T)` at instantiation time — no reflection, no runtime dispatch. The object size is computed once in the constructor via `unsafe.Sizeof` and reused for all subsequent allocations.

Huge operates on `[]byte` directly, which is the natural type for large byte buffers and does not benefit from generic parameterization.

```
NewArena[Entry]()     →  *Arena[Entry]      →  Alloc() → *Entry        (typed)
NewSlab[TreeNode]()   →  *Slab[TreeNode]    →  Alloc() → Handle[TreeNode] → Get() → *TreeNode
NewHuge()             →  *Huge              →  Alloc(size) → []byte
```

---

## Arena[T]

Arena is a bump-pointer allocator. Objects are allocated sequentially within contiguous blocks and freed all at once via `Reset`. There is no per-object freelist, no per-object metadata, and no individual free operation. Allocation is a pointer increment under a mutex.

### When to Use

- Per-request or per-batch scratch memory where all objects share a common lifetime
- Parser buffers, SQL query plans, serialization contexts
- Any workload where you allocate N objects, use them, then discard all of them

### How It Works

```
blocks:  [ block₀ ──────────── ][ block₁ ──────────── ][ block₂ ... ]
                     ▲ pos
                     └── next allocation goes here

Reset:   pos → 0, cur → 0 (blocks retained, no deallocation)
```

1. `NewArena` allocates one initial block (`blockSize / sizeof(T)` objects).
2. `Alloc` returns `&blocks[cur].data[pos]`, zeroes the slot, increments `pos`.
3. When the current block is full, the allocator advances to the next retained block (from a previous growth cycle) before allocating a new one.
4. `Reset` rewinds the bump pointer to the beginning. All blocks are retained and reused — no memory is freed, no GC pressure.
5. `EnsureCap(n)` resets the pointer and, if needed, allocates additional blocks to reach the requested total capacity. Useful for repeated parse/execute cycles with a known working set size.

### Quick Start

```go
arena := slabix.NewArena[Entry](
    slabix.WithBlockSize(4 * 1024 * 1024), // 4 MB blocks
)
defer arena.Release()

// Allocate objects
entry, _ := arena.Alloc()
entry.Key = "foo"

batch, _ := arena.AllocSlice(128) // contiguous within one block

// Discard everything, retain backing memory
arena.Reset()
```

Pre-sized cycles for repeated workloads:

```go
for _, input := range inputs {
    arena.EnsureCap(len(input) * 3) // reset + grow if needed
    for i := 0; i < len(input)*3; i++ {
        node, _ := arena.Alloc()
        // ...
    }
}
```

### API

| Method | Signature | Description |
|---|---|---|
| `NewArena` | `NewArena[T](opts...) *Arena[T]` | Create arena with one initial block |
| `Alloc` | `() (*T, error)` | Allocate a single zeroed object |
| `AllocSlice` | `(n int) ([]T, error)` | Allocate n contiguous zeroed objects |
| `EnsureCap` | `(n int)` | Reset + ensure capacity for n objects |
| `Reset` | `()` | Invalidate all objects, retain blocks |
| `Release` | `()` | Free all memory, mark as closed |
| `Stats` | `() Stats` | Atomic statistics snapshot |
| `Cap` | `() int` | Total object capacity across all blocks |
| `Len` | `() int` | Number of currently allocated objects |

### Configuration

| Option | Default | Description |
|---|---|---|
| `WithBlockSize(n)` | 4 MB | Bytes per block. Objects per block = blockSize / sizeof(T) |
| `WithMaxBlocks(n)` | 0 (unlimited) | Hard cap on block count. 0 = unlimited |
| `WithGrowable(bool)` | `true` | Whether to allocate new blocks on exhaustion |

> [!WARNING]
> All pointers and slices returned by `Alloc` and `AllocSlice` become invalid after `Reset` or `Release`. Dereferencing them is undefined behavior — the backing memory is reused by subsequent allocations. Copy data out if it must outlive the arena.

> [!NOTE]
> `EnsureCap` is the recommended pattern for repeated cycles. It resets the bump pointer and, if the current capacity is insufficient, allocates additional blocks to reach the requested size. This avoids both unnecessary allocations (when capacity is sufficient) and unnecessary resets (when you need to grow). `EnsureCap` respects the `WithMaxBlocks` limit.

---

## Slab[T]

Slab is a fixed-size object pool with per-chunk freelists. Each allocated object gets a typed `Handle[T]` — an opaque reference that encodes the shard, chunk, slot, and a generation counter. Objects are individually allocated and freed.

### When to Use

- Long-lived object pools where objects are created and destroyed independently
- Tree nodes (B-tree, LSM), cache entries, index records, message structs
- Any workload where you need `new(T)` / `delete(T)` semantics with reuse

### How It Works

```
shards:   [ shard₀ ][ shard₁ ][ shard₂ ]...    ← round-robin via atomic counter
              │
              ▼
chunks:   [ chunk₀: [entry₀ → entry₁ → entry₂ → ...] ]    ← freelist
          [ chunk₁: [entry₀ → entry₁ → ...] ]
              │
              ▼
freelist: freehead → slot₃ → slot₇ → slot₁ → -1
```

1. `NewSlab` creates `N` shards (configurable via `WithShards`), each with one initial chunk.
2. `Alloc` picks a shard (round-robin via atomic counter), locks it, pops a slot from the freelist, zeroes it, and returns a `Handle[T]`.
3. `Get(handle)` decodes the shard/chunk/slot from the handle, verifies the generation counter, and returns `*T`.
4. `Free(handle)` pushes the slot back onto the freelist, increments the generation counter, zeroes the value.
5. When a chunk is exhausted, the slab grows by adding a new chunk (subject to `WithSlabGrowable`, `WithMaxChunks`, and `WithGrowthPolicy`).

### Handle[T]

Handle is an opaque 8-byte value that packs four fields into two `uint32`:

```
chunk: [ shardIdx  16 bits | chunkIdx  16 bits ]
index: [ generation 12 bits | slotIdx  20 bits ]
```

| Field | Bits | Max value | Enforced |
|---|---|---|---|
| Shard index | 16 | 65,536 | Panic in `NewSlab` |
| Chunk index | 16 | 65,536 | `ErrOutOfMemory` on growth |
| Slot index | 20 | 1,048,576 | Panic in `addChunk` |
| Generation | 12 | 4,096 | Wraparound (accepted trade-off) |

The generation counter wraps after 4,096 alloc/free cycles on the same slot. A stale handle whose generation has wrapped to the current value will appear valid. In practice, 4,096 cycles per slot is sufficient for most workloads.

### Quick Start

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

pool.Free(h) // individual free, slot returned to freelist
```

Batch operations under a single lock:

```go
handles, _ := pool.BatchAlloc(100)
for _, h := range handles {
    pool.Get(h).Key = rand.Int()
}
pool.BatchFree(handles) // frees all, returns first error
```

### API

| Method | Signature | Description |
|---|---|---|
| `NewSlab` | `NewSlab[T](opts...) *Slab[T]` | Create pool with one chunk per shard |
| `Alloc` | `() (Handle[T], error)` | Allocate one zeroed object, return handle |
| `Get` | `(Handle[T]) *T` | Dereference handle to pointer. `nil` if invalid |
| `Free` | `(Handle[T]) error` | Return object to freelist |
| `BatchAlloc` | `(n int) ([]Handle[T], error)` | Allocate n objects under single lock |
| `BatchFree` | `([]Handle[T]) error` | Free all handles, return first error |
| `Release` | `()` | Free all memory, mark as closed |
| `Stats` | `() Stats` | Atomic statistics snapshot |
| `Cap` | `() int` | Total slot capacity across all shards |
| `Len` | `() int` | Live objects (Allocs − Frees) |

### Configuration

| Option | Default | Description |
|---|---|---|
| `WithSlabCapacity(n)` | 4096 | Total initial capacity, split evenly across shards |
| `WithShards(n)` | 1 | Number of independent shards (max 65,536) |
| `WithSlabGrowable(bool)` | `true` | Whether to add chunks on exhaustion |
| `WithGrowthPolicy(p)` | `GrowAdaptive` | How new chunks are sized (see below) |
| `WithMaxChunks(n)` | 0 (unlimited) | Per-shard chunk cap. Hard backpressure limit |
| `WithBatchHint(n)` | 64 | Reserved for future pre-sizing. Currently unused |

### Growth Policies

| Policy | Behavior |
|---|---|
| `GrowFixed` | Every chunk = initial capacity |
| `GrowLinear` | Each chunk adds initial capacity (cap, 2×cap, 3×cap, ...) |
| `GrowAdaptive` | Doubles until ceiling, then fixed (default) |

The ceiling is `max(initCap, 4096)`. No policy exceeds this. This prevents the unbounded memory growth that plagues naive doubling strategies.

> [!WARNING]
> After `Free(h)`, the handle and any pointers obtained via `Get(h)` are invalid. `Get` returns `nil` for freed handles (generation mismatch). Using a stale pointer is a logic error — the backing slot is zeroed and may be reused by a subsequent `Alloc`.

> [!WARNING]
> `BatchAlloc` allocates from a single shard under one lock. If that shard is exhausted, it returns a partial result with `ErrOutOfMemory` — it does not try other shards. Use individual `Alloc` calls if you need cross-shard fallback.

> [!NOTE]
> For concurrent workloads, set `WithShards(runtime.GOMAXPROCS(0))` to create one shard per CPU. Each shard has its own mutex and freelist, reducing contention to near zero under parallel load.

---

## Huge

Huge is an allocator for large byte buffers that would fragment slab or arena memory. Each allocation is backed by its own Go slice. Freed buffers are returned to internal `sync.Pool`-backed size-class pools for reuse.

### When to Use

- Buffers in the 64 KB – 16 MB range: WAL segments, compaction pages, bulk I/O buffers
- Any large `[]byte` allocation that benefits from reuse across requests
- Complement to Arena (which is typed) and Slab (which is fixed-size objects)

### How It Works

```
Alloc(128KB):
    align(128KB, 64) → sizeClass(128KB) → class 1 (128KB bucket)
    pool[1].Get() → hit? reuse : make([]byte, 128KB)
    live[slicePtr] = {buf, class}
    return buf[:128KB]

Free(buf):
    key = unsafe.SliceData(buf)
    entry = live[key]   → delete(live, key)
    pool[entry.class].Put(&buf)   → returned to pool for reuse
```

Size classes are powers of two:

| Class | Size | Pool index |
|---|---|---|
| 0 | 64 KB (2^16) | 0 |
| 1 | 128 KB (2^17) | 1 |
| 2 | 256 KB (2^18) | 2 |
| ... | ... | ... |
| 8 | 16 MB (2^24) | 8 |
| — | > 16 MB | No pool (GC-managed) |

Buffers smaller than 64 KB are rounded up to 64 KB. Buffers larger than 16 MB bypass the pool entirely and are garbage collected normally.

### Quick Start

```go
huge := slabix.NewHuge(
    slabix.WithAlignment(64),
    slabix.WithPoolReuse(true),
)
defer huge.Release()

buf, _ := huge.Alloc(128 * 1024) // 128 KB, zeroed
// use buf...
huge.Free(buf) // returned to size-class pool for reuse
```

### API

| Method | Signature | Description |
|---|---|---|
| `NewHuge` | `NewHuge(opts...) *Huge` | Create allocator with empty tracking map |
| `Alloc` | `(size int) ([]byte, error)` | Allocate zeroed byte buffer |
| `Free` | `([]byte) error` | Free buffer, return to pool if enabled |
| `Release` | `()` | Release all tracking, mark as closed |
| `Stats` | `() Stats` | Atomic statistics snapshot |
| `Len` | `() int` | Number of live (un-freed) allocations |

### Configuration

| Option | Default | Description |
|---|---|---|
| `WithAlignment(n)` | 64 | Byte alignment. Must be a positive power of two |
| `WithPoolReuse(bool)` | `true` | Return freed buffers to size-class pools |

> [!WARNING]
> Pass the exact slice returned by `Alloc` to `Free`. Do not reslice (`buf[1:]`), append, or otherwise modify the slice header before freeing. Huge tracks allocations by the backing array pointer (`unsafe.SliceData`) — reslicing changes this pointer and causes `Free` to fail with `ErrDoubleFree`.

> [!NOTE]
> Buffers smaller than 64 KB work but waste memory — the backing buffer is always rounded up to the smallest size-class boundary (64 KB). For small allocations, use Arena or Slab instead.

> [!NOTE]
> `Release` nils the tracking map and marks the allocator as closed. Pool buffers remain in `sync.Pool` and become eligible for garbage collection on the next GC cycle — `Release` does not force immediate deallocation of pooled buffers.

---

## Observability

Every allocator exposes a `Stats()` method returning a point-in-time snapshot:

```go
type Stats struct {
    Allocs         uint64  // cumulative successful allocations
    Frees          uint64  // cumulative frees (includes bulk-free adjustments)
    ActiveObjects  uint64  // Allocs − Frees (live objects)
    BytesAllocated uint64  // total backing memory reserved
    BytesInUse     uint64  // memory occupied by live objects
    BlockCount     uint64  // backing blocks / chunks / buffers
    GrowEvents     uint64  // backing-storage expansions beyond initial
    OOMs           uint64  // ErrOutOfMemory rejections
    Resets         uint64  // bulk resets (Arena only)
}
```

All counters are maintained with `sync/atomic` operations — no locks on the allocation hot path. The snapshot is consistent per field but not globally linearizable (no lock is held during collection).

After `Release`: gauge fields (`ActiveObjects`, `BytesInUse`, `BytesAllocated`, `BlockCount`) are zeroed. Cumulative counters (`Allocs`, `Frees`, `GrowEvents`, `OOMs`, `Resets`) are preserved — they remain available for post-mortem analysis.

## Errors

Five sentinel errors, all comparable with `==` and `errors.Is`:

| Error | Condition |
|---|---|
| `ErrClosed` | Operation on a released allocator |
| `ErrOutOfMemory` | Capacity exhausted — growth disabled or max blocks/chunks reached |
| `ErrDoubleFree` | Handle or buffer freed twice |
| `ErrInvalidHandle` | Handle from wrong allocator, stale generation, or nil/empty buffer |
| `ErrTooLarge` | Reserved for future use |

## Safety

- All memory is **GC-visible** — no `mmap`, no `cgo`, no off-heap allocations
- `unsafe` usage is confined to `internal/align` (size calculations) and `sliceKey` (slice pointer identity). No pointer arithmetic leaks through the public API
- Slab handles carry **generation counters** to detect use-after-free
- Double-free returns a clear error instead of corrupting internal state
- Handle bit-packing limits are **enforced at runtime**: exceeding slot, chunk, or shard limits panics or returns `ErrOutOfMemory`
- Growth is **capped** by configurable limits (`WithMaxBlocks`, `WithMaxChunks`) and policy ceilings
- All allocators are **safe for concurrent use**. Arena and Huge use a single mutex. Slab uses per-shard mutexes with round-robin selection. Stats use lock-free atomic counters
- `Release` uses `atomic.Bool` CAS — idempotent, safe to call multiple times or concurrently

## Benchmarks

### Methodology

Intel Core i7-10510U @ 1.80GHz (4C/8T, mobile TDP 15W), Windows 10, Go 1.24. Two sessions of `go test -bench=. -count=7` (14 iterations per benchmark), clean terminal outside IDE. Second session includes `-benchmem` for allocation profiling. Mobile CPUs throttle under sustained load — reported values are **medians** from the `-benchmem` session, which inherently filters first-iteration warmup outliers.

### Arena[T]

| Benchmark | What it measures | Median | B/op | allocs/op | Throughput |
|---|---|---|---|---|---|
| ArenaAlloc | Single `*T` allocation | 25 ns | 23 | 0 | ~39M ops/sec |
| ArenaAllocSlice | 64 contiguous `T` values | 181 ns | 1536 | 0 | ~354M obj/sec |
| ArenaAllocResetCycle | 1000 allocs + `Reset` | 24.7 µs | 0 | 0 | ~40M obj/sec |
| ArenaAllocParallel | Single alloc, 8 goroutines | 86 ns | 23 | 0 | ~12M ops/sec |

**Observations**:
- Single-object allocation is a bump-pointer increment under an uncontended mutex — O(1), zero heap allocations. The 23 B/op is amortized bookkeeping overhead from the runtime, not from the allocator itself.
- `AllocSlice` amortizes the mutex acquisition across 64 objects: 181 ns / 64 = 2.8 ns per object. Warmup effect is visible in the raw data (first 2 iterations: 510 ns, 390 ns; stable: 178–203 ns) — the CPU branch predictor and cache need 1–2 iterations to warm.
- `AllocResetCycle` is the key metric for Arena's target workload: 1000 objects allocated, used, and bulk-freed in 24.7 µs with **zero bytes allocated and zero GC pressure**. The entire backing memory is retained across resets.
- Parallel throughput drops to ~12M ops/sec (3.3x slower than single-threaded) due to mutex contention. Arena uses a single `sync.Mutex` — for parallel workloads, use one Arena per goroutine.

### Slab[T]

| Benchmark | What it measures | Median | B/op | allocs/op | Throughput |
|---|---|---|---|---|---|
| SlabGet | Handle → `*T` dereference | 16 ns | 0 | 0 | ~62M ops/sec |
| SlabAlloc | Single object allocation | 32 ns | 47 | 0 | ~32M ops/sec |
| SlabAllocFree | Alloc + Free round-trip | 54 ns | 0 | 0 | ~19M cycles/sec |
| SlabBatchAllocFree | 128 objects + batch free | 5.9 µs | 1024 | 1 | ~22M obj/sec |
| SlabAllocFreeParallel | Alloc+Free, 8 goroutines | 212 ns | 0 | 0 | ~4.7M ops/sec |
| SlabAllocGetFreeParallel | Alloc+Get+Free, 8 goroutines | 227 ns | 0 | 0 | ~4.4M ops/sec |

**Observations**:
- `Get` is the fastest operation in the library: 16 ns to decode Handle bits, lock the shard, verify generation, and return a typed pointer. Zero allocations.
- A full alloc+free cycle completes in 54 ns with zero heap allocations. The freed slot returns to the freelist and is reused by the next `Alloc` — no GC involvement.
- `BatchAllocFree` processes 128 objects under a single lock acquisition: 5.9 µs total = 46 ns/object. The 1024 B / 1 alloc is the handle slice (`128 × 8 bytes`). Per-object cost is lower than individual `Alloc` due to amortized locking.
- Parallel throughput with 8 goroutines (default: 1 shard) reaches ~4.7M alloc+free ops/sec. Adding `Get` to the round-trip costs an additional ~15 ns (227 − 212 = 15 ns overhead, consistent with the isolated `Get` benchmark at 16 ns). With `WithShards(runtime.GOMAXPROCS(0))`, throughput scales near-linearly.

### Huge

| Benchmark | What it measures | Median | B/op | allocs/op | Throughput |
|---|---|---|---|---|---|
| HugeAllocFree | 128 KB alloc + free | 2.4 µs | 32 | 1 | ~416K ops/sec |
| HugeAllocFreeParallel | 128 KB, 8 goroutines | 1.6 µs | 43 | 1 | ~607K ops/sec |

**Observations**:
- An alloc+free cycle for a 128 KB buffer takes 2.4 µs. The 32 B/op and 1 alloc/op is the `sync.Pool` pointer wrapper (`*[]byte`) — the 128 KB buffer itself comes from the pool and is not a new heap allocation (after warmup).
- Parallel throughput is **1.46x higher** than single-threaded — the opposite of Arena/Slab. This is because `sync.Pool` maintains per-P (per-processor) caches: each goroutine hits its own local pool, reducing both mutex contention and GC pressure from pool misses.

### Thermal Throttling

The first session (without `-benchmem`) shows 40–60% higher latency on CPU-bound benchmarks (ArenaAlloc: 42 vs 25 ns, ArenaAllocResetCycle: 39 µs vs 24.7 µs) due to thermal throttling on a 15W mobile CPU. Memory-bound benchmarks (SlabGet, SlabAlloc) are stable across both sessions (< 5% variance). This confirms that Arena's bump-pointer path is CPU-bound (pointer arithmetic + mutex), while Slab's freelist path has a higher memory-access component (entry metadata reads).

### Summary

| Allocator | Best single-thread | Parallel (8 gor.) | Hot path allocs |
|---|---|---|---|
| **Arena** | 25 ns/alloc, 39M ops/sec | 86 ns, 12M ops/sec | 0 |
| **Slab** | 16 ns/get, 62M ops/sec | 227 ns (full trip), 4.4M ops/sec | 0 |
| **Huge** | 2.4 µs/alloc+free, 416K ops/sec | 1.6 µs, 607K ops/sec | 1 (pool wrapper) |

### What the Numbers Mean (and What They Don't)

These benchmarks measure allocator overhead — the cost of the bookkeeping, not the cost of using the memory. In a real application, the allocation latency is a small fraction of the total work done with each object.

**What slabix is faster at**: not individual allocations. A single `new(T)` in Go can be as fast as 5–10 ns when the compiler stack-allocates it. slabix's 25 ns Arena alloc is slower per-call. The advantage is structural: `new(T)` called 10 million times per second creates 10 million heap objects the GC must trace, mark, and sweep. Arena allocates the same 10 million objects from pre-allocated blocks with zero GC pressure — the collector never sees them individually. The cost shifts from runtime (GC pauses) to design time (choosing the right allocator for the pattern).

**Where it helps**: workloads with millions of allocations per second where GC pause latency matters — database query execution, WAL batch processing, cache eviction storms, message broker fan-out. If your application allocates a few thousand objects per request, standard Go allocation is fine and slabix adds unnecessary complexity.

**Where it does not help**: CPU-bound computation, I/O-bound workloads, applications where allocation is not the bottleneck. Profile first. If `runtime.mallocgc` is not in your top-10 pprof flamegraph, you do not need a custom allocator.

**Parallel scaling**: Arena's single mutex becomes a bottleneck at 8 goroutines (3.3x slowdown). This is by design — Arena targets per-goroutine or per-request use, not shared concurrent access. Slab scales better with sharding. Huge benefits from `sync.Pool`'s per-P architecture. Choose the allocator that matches both your allocation pattern and your concurrency model.

## Quality

| Metric | Value |
|---|---|
| Test functions | 96 |
| Benchmarks | 12 |
| Examples | 6 |
| Coverage (main) | 94.2% |
| Coverage (internal/align) | 100% |
| Race detector | All tests pass with `-race` |
| Go version | 1.24+ |
| External deps | 0 (stdlib only) |

Uncovered code (~5.8%) consists of defensive guards for zero-size type parameters (`unsafe.Sizeof(*new(T)) == 0`), growth policy default-case fallbacks unreachable with defined `GrowthPolicy` constants, and Handle bit-packing limit checks that require 65,536+ chunks to trigger.

## File Structure

```
slabix/
├── doc.go              # Package-level documentation
├── arena.go            # Arena[T] bump-pointer allocator
├── slab.go             # Slab[T] fixed-size freelist allocator
├── huge.go             # Huge byte buffer allocator with pool reuse
├── handle.go           # Handle[T] typed reference with bit-packing
├── options.go          # Functional options (WithXxx), GrowthPolicy
├── stats.go            # Stats snapshot + atomic collector
├── errors.go           # 5 sentinel errors
├── internal/
│   └── align/
│       ├── align.go    # Alignment helpers (unsafe, internal only)
│       └── align_test.go
├── arena_test.go       # 33 tests, 4 benchmarks
├── slab_test.go        # 36 tests, 6 benchmarks
├── huge_test.go        # 19 tests, 2 benchmarks
├── stats_test.go       # 5 tests
├── example_test.go     # 6 runnable examples
├── llm.md              # LLM reference (for AI-assisted development)
└── go.mod              # Zero dependencies
```

## License

MIT
