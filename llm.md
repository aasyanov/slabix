# slabix — LLM Reference

> Module: `github.com/aasyanov/slabix` | Go 1.24+ | Zero dependencies

## CRITICAL RULES

1. Config via **functional options** to constructors — never mutate structs
2. Errors are **sentinel values** (`==` / `errors.Is`) — never `fmt.Errorf`
3. Arena pointers are **invalid after Reset/Release** — copy data if it must outlive the arena
4. Slab handles carry **generation counters** — stale handle after Free returns `ErrInvalidHandle`
5. Huge buffers are tracked by **backing array pointer** — never reslice before Free
6. All allocators are **thread-safe** — but per-goroutine Arena gives best throughput
7. **No exponential growth** — all growth policies are capped at `max(initCap, 4096)`

---

## API

### Arena[T] — bump-pointer, bulk reset

```go
arena := slabix.NewArena[T](opts...)
defer arena.Release()

ptr, err := arena.Alloc()           // *T, zeroed
slice, err := arena.AllocSlice(n)   // []T, contiguous, zeroed
arena.Reset()                       // invalidate all, retain memory
arena.EnsureCap(n)                  // reset + ensure capacity for n objects

arena.Cap()    // int — total capacity across all blocks
arena.Len()    // int — currently allocated objects
arena.Stats()  // Stats — atomic snapshot
```

### Slab[T] — fixed-size freelist pool

```go
pool := slabix.NewSlab[T](opts...)
defer pool.Release()

h, err := pool.Alloc()              // Handle[T]
ptr := pool.Get(h)                  // *T — nil if invalid
err = pool.Free(h)                  // return to freelist
handles, err := pool.BatchAlloc(n)  // []Handle[T], single lock per shard
err = pool.BatchFree(handles)       // free all, first error returned

pool.Cap()    // int — total slots across shards
pool.Len()    // int — live objects (Allocs - Frees)
pool.Stats()  // Stats — atomic snapshot
```

### Huge — large byte buffers with pool reuse

```go
huge := slabix.NewHuge(opts...)
defer huge.Release()

buf, err := huge.Alloc(size)        // []byte, zeroed, aligned
err = huge.Free(buf)                // return to size-class pool

huge.Len()    // int — live allocations
huge.Stats()  // Stats — atomic snapshot
```

### Handle[T]

```go
var h slabix.Handle[T]
h.IsZero()  // true if never allocated (zero value)
```

Packed fields: `chunk = (shardIdx << 16) | chunkIdx`, `index = slotIdx | (gen << 20)`.

---

## OPTIONS

### Arena

```go
WithBlockSize(n)       // bytes per block, default 4 MB
WithMaxBlocks(n)       // 0 = unlimited
WithGrowable(bool)     // default true
```

### Slab

```go
WithSlabCapacity(n)    // objects per initial chunk, default 4096
WithShards(n)          // independent shards, default 1
WithSlabGrowable(bool) // default true
WithGrowthPolicy(p)    // GrowFixed | GrowLinear | GrowAdaptive (default)
WithMaxChunks(n)       // per shard, 0 = unlimited
WithBatchHint(n)       // expected batch size, default 64
```

### Huge

```go
WithAlignment(n)       // power of two, default 64 (cache line)
WithPoolReuse(bool)    // sync.Pool reuse, default true
```

### Growth Policies

- `GrowFixed` — every chunk = initial capacity
- `GrowLinear` — each chunk adds initial capacity (cap, 2*cap, 3*cap, ...)
- `GrowAdaptive` — doubles until ceiling, then fixed (default)

Ceiling = `max(initCap, 4096)`. No policy exceeds this.

---

## ERRORS

```go
ErrClosed        // operation on released allocator
ErrOutOfMemory   // capacity exhausted + growth disabled or max chunks/blocks reached
ErrDoubleFree    // handle or buffer freed twice
ErrInvalidHandle // wrong allocator, stale generation, or nil buffer
ErrTooLarge      // allocation exceeds maximum size
```

All created with `errors.New`, comparable with `==` and `errors.Is`.

---

## STATS

```go
type Stats struct {
    Allocs         uint64  // cumulative
    Frees          uint64  // cumulative
    ActiveObjects  uint64  // Allocs - Frees
    BytesAllocated uint64  // total backing memory reserved
    BytesInUse     uint64  // memory occupied by live objects
    BlockCount     uint64  // backing blocks/chunks
    Resets         uint64  // arena only
}
```

All fields are atomic. Snapshot is consistent per field but not across fields (no global lock).

---

## LIFECYCLE

```
NewArena/NewSlab/NewHuge
    │
    ▼
  OPEN ──── Alloc / Get / Free / Reset / EnsureCap / Stats / Cap / Len
    │
    ▼
  Release() ── CAS(closed, false→true), idempotent
    │
    ▼
  CLOSED ── Alloc/Free/BatchAlloc → ErrClosed
            Reset/EnsureCap → silent no-op
            Stats/Cap/Len → zeroed values
            Release() → no-op (CAS fails)
```

Double-check under lock prevents race between Alloc and concurrent Release.

---

## INTERNALS

### Arena

```
NewArena → blocks[0] (objsPerBlock = blockSize / sizeof(T))
Alloc    → blocks[cur].data[pos]; pos++; if full → grow (if allowed)
Reset    → cur=0, pos=0, bytesInUse=0 (blocks retained)
Release  → blocks=nil, closed=true
```

- Mutex protects `blocks`, `cur`, `pos`
- `EnsureCap(n)` resets + grows blocks if `totalCap < n`

### Slab

```
NewSlab → shards[0..N], each with chunks[0] (capPerShard entries)
Alloc   → pickShard (round-robin via atomic) → shard.alloc under lock
          findFreeChunk (O(1) via lastFreeChunk hint) → pop freelist head
Free    → entry.alive=false, gen++, push to freelist head
```

- Each shard is independently locked
- Handle packs: shard (16 bits), chunk (16 bits), slot (20 bits), generation (12 bits)
- `BatchAlloc(n)` holds shard lock for entire batch — single acquisition

### Huge

```
NewHuge → empty live map + 9 sync.Pool (64KB..16MB, powers of two)
Alloc   → align size → sizeClass → pool.Get or make([]byte) → live[ptr]=entry
Free    → delete(live, ptr) → pool.Put(&buf) if class >= 0
```

- `live` map tracks allocations by backing array pointer (`unsafe.SliceData`)
- Pool stores `*[]byte` (pointer to slice) to satisfy staticcheck SA6002
- Buffers returned to caller as `buf[:size]` (capacity may be larger)

### Concurrency

- Arena: single mutex for all operations; per-goroutine arena recommended
- Slab: per-shard mutex; round-robin shard selection via `atomic.Uint64`
- Huge: single mutex for `live` map; `sync.Pool` is internally concurrent
- Stats: atomic counters, no locks
- Release: `atomic.Bool` CAS, idempotent; double-checked under lock

---

## MISTAKES TO AVOID

1. **Holding Arena pointers after Reset** — they point into reused memory
2. **Using Handle after Free** — generation incremented, Get returns nil
3. **Reslicing Huge buffer before Free** — `sliceKey` uses backing array pointer, reslice changes it
4. **Ignoring ErrOutOfMemory** — means growth disabled or limit reached, not system OOM
5. **Sharing Arena across goroutines** — works but contention kills throughput; use one per goroutine
6. **Assuming Cap() is atomic with Alloc()** — Cap and Len acquire locks, snapshot may be stale
7. **Expecting Huge to free pool buffers on Release** — `sync.Pool` manages its own GC lifecycle

## NOT IN SCOPE

No garbage collector, no `mmap`/`cgo`, no thread pinning, no NUMA awareness, no compaction.
