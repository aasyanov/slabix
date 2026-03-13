package slabix

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Slab is a fixed-size object pool with per-shard freelists and optional
// sharding. Objects are individually allocated and freed via typed
// [Handle] references.
//
// Slab is designed for long-lived object pools where objects are
// frequently allocated and freed: tree nodes, cache entries, index
// records, message structs.
//
//	pool := slabix.NewSlab[Node](
//	    slabix.WithSlabCapacity(8192),
//	    slabix.WithShards(runtime.GOMAXPROCS(0)),
//	)
//	h, _ := pool.Alloc()
//	node := pool.Get(h)
//	node.Key = 42
//	_ = pool.Free(h)
//
// Slab is safe for concurrent use.
type Slab[T any] struct {
	cfg    slabConfig
	objSz  uintptr
	shards []slabShard[T]
	seq    atomic.Uint64 // round-robin counter for shard selection
	closed atomic.Bool
	stats  statsCollector
}

// slabShard is an independent allocator partition. Each shard has its
// own chunks and freelist, reducing contention in concurrent workloads.
type slabShard[T any] struct {
	mu            sync.Mutex
	chunks        []slabChunk[T]
	lastFreeChunk int // cached hint: index of a chunk with free slots, -1 = unknown
	initCap       int
	objSz         uintptr
	cfg           slabConfig
	stats         *statsCollector // points to the parent Slab's collector
}

// slabChunk holds a contiguous array of objects with per-slot metadata.
// Each chunk maintains its own embedded freelist via the entry.next
// linked list.
type slabChunk[T any] struct {
	entries  []slabEntry[T]
	freehead int32 // chunk-local freelist head, -1 = empty
	freeLen  int   // number of free slots in this chunk
}

// slabEntry is a single slot within a chunk. It stores the user value
// alongside allocation metadata: a generation counter for use-after-free
// detection, an alive flag, and a freelist link.
type slabEntry[T any] struct {
	value T
	gen   uint32 // incremented on each free; used to detect stale handles
	alive bool
	next  int32 // freelist link within this chunk, -1 = end
}

// maxSlotIndex is the maximum number of slots per chunk, constrained
// by the 20-bit slot field in [Handle]. See handle.go for the full
// bit-packing layout.
const maxSlotIndex = 1 << 20 // 1,048,576

// maxChunkIndex is the maximum number of chunks per shard, constrained
// by the 16-bit chunk field in [Handle]. Exceeding this limit returns
// [ErrOutOfMemory] instead of corrupting Handle values.
const maxChunkIndex = 1 << 16 // 65,536

// NewSlab creates a [Slab] pool for objects of type T. One initial
// chunk per shard is allocated immediately. The total initial capacity
// is split evenly across shards.
//
//	pool := slabix.NewSlab[Node](
//	    slabix.WithSlabCapacity(8192),
//	    slabix.WithShards(4),
//	)
func NewSlab[T any](opts ...SlabOption) *Slab[T] {
	cfg := defaultSlabConfig()
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.shards > maxChunkIndex {
		panic("slabix: shard count exceeds Handle limit (1<<16)")
	}

	objSz := unsafe.Sizeof(*new(T))
	if objSz == 0 {
		objSz = 1
	}

	capPerShard := cfg.capacity / cfg.shards
	if capPerShard < 1 {
		capPerShard = 1
	}

	s := &Slab[T]{
		cfg:    cfg,
		objSz:  objSz,
		shards: make([]slabShard[T], cfg.shards),
	}

	for i := range s.shards {
		sh := &s.shards[i]
		sh.initCap = capPerShard
		sh.objSz = objSz
		sh.cfg = cfg
		sh.stats = &s.stats
		sh.addChunk(capPerShard)
		sh.lastFreeChunk = 0
	}

	return s
}

// addChunk appends a new chunk with the given capacity and a full
// freelist. Panics if cap exceeds the Handle slot-index limit.
func (sh *slabShard[T]) addChunk(cap int) {
	if cap > maxSlotIndex {
		panic("slabix: chunk capacity exceeds Handle slot limit (1<<20)")
	}

	chunk := slabChunk[T]{
		entries:  make([]slabEntry[T], cap),
		freehead: 0,
		freeLen:  cap,
	}

	for i := range chunk.entries {
		chunk.entries[i].next = int32(i + 1)
		chunk.entries[i].gen = 1
	}

	chunk.entries[cap-1].next = -1

	sh.chunks = append(sh.chunks, chunk)
	sh.stats.addBlock()
	sh.stats.addBytes(uint64(cap) * uint64(sh.objSz))
}

// nextChunkSize computes the size of the next chunk based on the
// configured [GrowthPolicy].
//
//   - [GrowFixed]: always returns initCap, ignoring the ceiling.
//   - [GrowLinear]: adds initCap to the previous chunk size, capped at
//     the ceiling (max of initCap and 4096).
//   - [GrowAdaptive]: doubles the previous chunk size, capped at the
//     same ceiling.
func (sh *slabShard[T]) nextChunkSize() int {
	prevCap := len(sh.chunks[len(sh.chunks)-1].entries)
	ceiling := sh.initCap
	if ceiling < 4096 {
		ceiling = 4096
	}

	switch sh.cfg.growth {
	case GrowFixed:
		return sh.initCap

	case GrowLinear:
		next := prevCap + sh.initCap
		if next > ceiling {
			return ceiling
		}
		return next

	case GrowAdaptive:
		next := prevCap * 2
		if next > ceiling {
			return ceiling
		}
		return next

	default:
		return sh.initCap
	}
}

// Alloc allocates a single zeroed object and returns its [Handle].
// Use [Slab.Get] to obtain a pointer to the object and [Slab.Free]
// to return it to the pool. Returns [ErrOutOfMemory] when capacity
// is exhausted (subject to [WithSlabGrowable] and [WithMaxChunks]).
func (s *Slab[T]) Alloc() (Handle[T], error) {
	if s.closed.Load() {
		return Handle[T]{}, ErrClosed
	}

	idx := s.pickShard()
	sh := &s.shards[idx]

	sh.mu.Lock()
	if s.closed.Load() {
		sh.mu.Unlock()
		return Handle[T]{}, ErrClosed
	}
	h, err := sh.alloc(uint32(idx))
	sh.mu.Unlock()

	return h, err
}

// alloc performs the allocation under sh.mu. It pops a slot from the
// freelist of a chunk with available space, growing the shard if needed.
func (sh *slabShard[T]) alloc(shardIdx uint32) (Handle[T], error) {
	chunkIdx := sh.findFreeChunk()
	if chunkIdx == -1 {
		if !sh.cfg.growable {
			sh.stats.addOOM()
			return Handle[T]{}, ErrOutOfMemory
		}

		if sh.cfg.maxChunks > 0 && len(sh.chunks) >= sh.cfg.maxChunks {
			sh.stats.addOOM()
			return Handle[T]{}, ErrOutOfMemory
		}

		if len(sh.chunks) >= maxChunkIndex {
			sh.stats.addOOM()
			return Handle[T]{}, ErrOutOfMemory
		}

		sh.addChunk(sh.nextChunkSize())
		sh.stats.addGrow()
		chunkIdx = len(sh.chunks) - 1
		sh.lastFreeChunk = chunkIdx
	}

	chunk := &sh.chunks[chunkIdx]
	slot := chunk.freehead
	entry := &chunk.entries[slot]
	chunk.freehead = entry.next
	chunk.freeLen--

	var zero T
	entry.value = zero
	entry.alive = true
	entry.next = -1

	sh.stats.addAlloc()
	sh.stats.addInUse(uint64(sh.objSz))

	if chunk.freeLen == 0 && sh.lastFreeChunk == chunkIdx {
		sh.lastFreeChunk = -1
	}

	return Handle[T]{
		chunk: (shardIdx << 16) | uint32(chunkIdx),
		index: uint32(slot) | (entry.gen << 20),
	}, nil
}

// findFreeChunk returns the index of a chunk with free slots using
// the cached lastFreeChunk hint for O(1) fast path. Falls back to a
// linear scan if the hint is stale.
func (sh *slabShard[T]) findFreeChunk() int {
	if sh.lastFreeChunk >= 0 && sh.lastFreeChunk < len(sh.chunks) {
		if sh.chunks[sh.lastFreeChunk].freeLen > 0 {
			return sh.lastFreeChunk
		}
	}

	for i := range sh.chunks {
		if sh.chunks[i].freeLen > 0 {
			sh.lastFreeChunk = i
			return i
		}
	}

	sh.lastFreeChunk = -1
	return -1
}

// Get returns a pointer to the object referenced by h. The pointer is
// valid until the handle is freed. Returns nil if h is invalid, stale,
// or the pool is closed.
func (s *Slab[T]) Get(h Handle[T]) *T {
	if s.closed.Load() || h.IsZero() {
		return nil
	}

	shardIdx := h.chunk >> 16
	chunkIdx := h.chunk & 0xFFFF
	slotIdx := h.index & 0xFFFFF
	gen := h.index >> 20

	if int(shardIdx) >= len(s.shards) {
		return nil
	}
	sh := &s.shards[shardIdx]

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if int(chunkIdx) >= len(sh.chunks) {
		return nil
	}

	chunk := &sh.chunks[chunkIdx]
	if int(slotIdx) >= len(chunk.entries) {
		return nil
	}

	entry := &chunk.entries[slotIdx]
	if !entry.alive || entry.gen != gen {
		return nil
	}

	return &entry.value
}

// Free returns the object referenced by h to the pool. The handle and
// any pointers obtained via [Slab.Get] become invalid. Returns
// [ErrDoubleFree] if the slot is already free, [ErrInvalidHandle] if
// the handle is stale or out of range.
func (s *Slab[T]) Free(h Handle[T]) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if h.IsZero() {
		return ErrInvalidHandle
	}

	shardIdx := h.chunk >> 16
	chunkIdx := h.chunk & 0xFFFF
	slotIdx := h.index & 0xFFFFF
	gen := h.index >> 20

	if int(shardIdx) >= len(s.shards) {
		return ErrInvalidHandle
	}
	sh := &s.shards[shardIdx]

	sh.mu.Lock()
	err := sh.free(chunkIdx, slotIdx, gen)
	sh.mu.Unlock()

	return err
}

// free returns the slot to the chunk's freelist under sh.mu. It zeroes
// the value, increments the generation counter, and updates the
// freelist hint.
func (sh *slabShard[T]) free(chunkIdx, slotIdx, gen uint32) error {
	if int(chunkIdx) >= len(sh.chunks) {
		return ErrInvalidHandle
	}

	chunk := &sh.chunks[chunkIdx]
	if int(slotIdx) >= len(chunk.entries) {
		return ErrInvalidHandle
	}

	entry := &chunk.entries[slotIdx]
	if !entry.alive {
		return ErrDoubleFree
	}

	if entry.gen != gen {
		return ErrInvalidHandle
	}

	var zero T
	entry.value = zero
	entry.alive = false
	entry.gen++
	entry.next = chunk.freehead
	chunk.freehead = int32(slotIdx)
	chunk.freeLen++

	sh.stats.addFree()
	sh.stats.subInUse(uint64(sh.objSz))

	if sh.lastFreeChunk < 0 || int(chunkIdx) < sh.lastFreeChunk {
		sh.lastFreeChunk = int(chunkIdx)
	}

	return nil
}

// BatchAlloc allocates n objects and returns their handles. Allocations
// are performed under a single lock acquisition per shard for reduced
// overhead. If the slab cannot satisfy the full batch, it returns a
// partial result with [ErrOutOfMemory].
//
// BatchAlloc(0) and BatchAlloc with negative n return (nil, nil).
func (s *Slab[T]) BatchAlloc(n int) ([]Handle[T], error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}

	if n <= 0 {
		return nil, nil
	}

	handles := make([]Handle[T], 0, n)

	for len(handles) < n {
		idx := s.pickShard()
		sh := &s.shards[idx]

		sh.mu.Lock()
		if s.closed.Load() {
			sh.mu.Unlock()
			return handles, ErrClosed
		}
		for len(handles) < n {
			h, err := sh.alloc(uint32(idx))
			if err != nil {
				sh.mu.Unlock()
				return handles, err
			}
			handles = append(handles, h)
		}
		sh.mu.Unlock()
	}

	return handles, nil
}

// BatchFree frees all handles in the slice. Returns the first error
// encountered, but continues freeing remaining handles to avoid
// resource leaks.
func (s *Slab[T]) BatchFree(handles []Handle[T]) error {
	var firstErr error
	for _, h := range handles {
		if err := s.Free(h); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// Release releases all backing memory and marks the slab as closed.
// After Release, [Slab.Alloc], [Slab.Free], and [Slab.BatchAlloc]
// return [ErrClosed].
func (s *Slab[T]) Release() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}

	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		sh.chunks = nil
		sh.lastFreeChunk = -1
		sh.mu.Unlock()
	}

	s.stats.resetActive()
	s.stats.bytesInUse.Store(0)
	s.stats.bytesAllocated.Store(0)
	s.stats.blockCount.Store(0)
}

// Stats returns a point-in-time snapshot of slab statistics.
func (s *Slab[T]) Stats() Stats {
	return s.stats.snapshot()
}

// Cap returns the total number of object slots across all shards and chunks.
func (s *Slab[T]) Cap() int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for j := range sh.chunks {
			n += len(sh.chunks[j].entries)
		}
		sh.mu.Unlock()
	}

	return n
}

// Len returns the number of currently allocated (live) objects across
// all shards, derived from the atomic Allocs − Frees counters.
func (s *Slab[T]) Len() int {
	st := s.stats.snapshot()
	return int(st.ActiveObjects)
}

// pickShard returns the next shard index using a round-robin counter.
// Single-shard pools skip the atomic increment.
func (s *Slab[T]) pickShard() int {
	if len(s.shards) == 1 {
		return 0
	}

	return int(s.seq.Add(1) % uint64(len(s.shards)))
}
