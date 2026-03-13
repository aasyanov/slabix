// Package slabix provides high-performance, low-GC memory allocation
// primitives for Go applications.
//
// slabix is designed for databases, write-ahead logs, caches, message
// brokers, and any system where predictable allocation latency and
// minimal garbage collection pressure are critical.
//
// # Allocators
//
// Three allocator types cover distinct allocation patterns:
//
//   - [Arena] is a bump-pointer allocator for short-lived, bulk allocations.
//     Objects are allocated sequentially and freed all at once via [Arena.Reset].
//     Ideal for per-request or per-batch scratch memory.
//
//   - [Slab] is a fixed-size object pool with per-shard freelists and optional
//     sharding. Objects can be individually allocated and freed via typed
//     [Handle] references. Ideal for tree nodes, cache entries, and message
//     structs.
//
//   - [Huge] handles large byte buffer allocations (typically > 64 KB) that
//     would fragment slab or arena memory. Each allocation is backed by its
//     own slice with optional size-class pool reuse.
//
// # Design
//
// Arena and Slab use Go generics for type safety — no interface{} casts
// in the allocation hot path. Huge operates on []byte directly. Memory
// remains GC-visible; unsafe usage is minimal and confined to size
// calculations and internal alignment helpers.
//
// Configuration follows the functional option pattern:
//
//	arena := slabix.NewArena[Entry](
//	    slabix.WithBlockSize(4 * 1024 * 1024),
//	)
//	entry, _ := arena.Alloc()
//	arena.Reset()
//
//	pool := slabix.NewSlab[TreeNode](
//	    slabix.WithSlabCapacity(8192),
//	    slabix.WithShards(runtime.GOMAXPROCS(0)),
//	)
//	h, _ := pool.Alloc()
//	node := pool.Get(h)
//	pool.Free(h)
//
// # Observability
//
// Each allocator exposes a [Stats] snapshot via its Stats method. Counters
// are maintained with atomic operations for lock-free collection on the
// hot path.
//
// # Safety
//
// All allocated memory is GC-visible. Unsafe usage is limited to
// compile-time size calculations and slice-pointer identity; no pointer
// arithmetic leaks through the public API. [Handle] references carry
// generation counters to detect use-after-free. Double-free returns a
// clear error instead of corrupting state.
//
// # Zero Dependencies
//
// slabix depends only on the Go standard library.
package slabix
