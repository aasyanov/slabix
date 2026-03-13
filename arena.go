package slabix

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Arena is a bump-pointer allocator for short-lived, bulk allocations
// of type T. Objects are allocated sequentially within contiguous blocks
// and freed all at once via [Arena.Reset].
//
// Arena is designed for per-request, per-batch, or per-parse scratch
// memory where individual frees are unnecessary. Allocation is O(1)
// with no freelist overhead.
//
// After [Arena.Reset], all existing blocks are retained and reused by
// subsequent allocations. No memory is wasted on duplicate blocks.
// Use [Arena.EnsureCap] for repeated cycles with a known working set.
//
// Arena is safe for concurrent use. For maximum throughput in parallel
// workloads, create one Arena per goroutine or per request.
type Arena[T any] struct {
	cfg    arenaConfig
	objSz  uintptr
	mu     sync.Mutex
	blocks []arenaBlock[T]
	cur    int // index of the current block being filled
	pos    int // next free slot within blocks[cur]
	closed atomic.Bool
	stats  statsCollector
}

// arenaBlock is a contiguous slab of T values within an Arena. Each
// block is allocated as a single Go slice and filled sequentially.
type arenaBlock[T any] struct {
	data []T
}

// NewArena creates an [Arena] that allocates objects of type T from
// contiguous blocks. One initial block is allocated immediately based
// on the configured block size.
//
//	arena := slabix.NewArena[Entry](
//	    slabix.WithBlockSize(4 * 1024 * 1024),
//	)
//	entry, _ := arena.Alloc()
//	arena.Reset()
func NewArena[T any](opts ...ArenaOption) *Arena[T] {
	cfg := defaultArenaConfig()
	for _, o := range opts {
		o(&cfg)
	}

	objSz := unsafe.Sizeof(*new(T))
	if objSz == 0 {
		objSz = 1
	}

	objsPerBlock := uintptr(cfg.blockSize) / objSz
	if objsPerBlock == 0 {
		objsPerBlock = 1
	}

	a := &Arena[T]{
		cfg:   cfg,
		objSz: objSz,
	}

	block := arenaBlock[T]{data: make([]T, objsPerBlock)}
	a.blocks = append(a.blocks, block)
	a.stats.addBlock()
	a.stats.addBytes(uint64(objsPerBlock) * uint64(objSz))

	return a
}

// Alloc returns a pointer to a freshly zeroed T from the arena. If the
// current block is full, the next retained block is reused; if no
// retained blocks remain, a new block is allocated (subject to
// [WithGrowable] and [WithMaxBlocks] limits). Returns [ErrOutOfMemory]
// when capacity is exhausted.
//
// The returned pointer is valid until [Arena.Reset] or [Arena.Release]
// is called.
func (a *Arena[T]) Alloc() (*T, error) {
	if a.closed.Load() {
		return nil, ErrClosed
	}

	a.mu.Lock()
	if a.closed.Load() {
		a.mu.Unlock()
		return nil, ErrClosed
	}
	ptr, err := a.allocLocked()
	a.mu.Unlock()

	return ptr, err
}

// allocLocked performs the bump-pointer allocation under a.mu.
// On block exhaustion it first tries to advance to a retained block
// (from a previous growth cycle), falling back to a new allocation.
func (a *Arena[T]) allocLocked() (*T, error) {
	blk := &a.blocks[a.cur]

	if a.pos < len(blk.data) {
		ptr := &blk.data[a.pos]
		var zero T
		*ptr = zero
		a.pos++
		a.stats.addAlloc()
		a.stats.addInUse(uint64(a.objSz))
		return ptr, nil
	}

	if a.cur+1 < len(a.blocks) {
		a.cur++
		a.pos = 0
		return a.allocLocked()
	}

	if !a.cfg.growable {
		a.stats.addOOM()
		return nil, ErrOutOfMemory
	}

	if a.cfg.maxBlocks > 0 && len(a.blocks) >= a.cfg.maxBlocks {
		a.stats.addOOM()
		return nil, ErrOutOfMemory
	}

	objsPerBlock := uintptr(a.cfg.blockSize) / a.objSz
	if objsPerBlock == 0 {
		objsPerBlock = 1
	}

	newBlk := arenaBlock[T]{data: make([]T, objsPerBlock)}
	a.blocks = append(a.blocks, newBlk)
	a.cur = len(a.blocks) - 1
	a.pos = 0
	a.stats.addBlock()
	a.stats.addGrow()
	a.stats.addBytes(uint64(objsPerBlock) * uint64(a.objSz))

	return a.allocLocked()
}

// AllocSlice returns a contiguous slice of n freshly zeroed T values
// from the arena. If the current block cannot hold n objects, retained
// blocks are checked first; if none can fit the slice, a new block is
// allocated (subject to [WithGrowable] and [WithMaxBlocks] limits).
// The returned slice is always contiguous within a single block.
//
// AllocSlice(0) and AllocSlice with negative n return (nil, nil).
//
// The returned slice is valid until [Arena.Reset] or [Arena.Release].
func (a *Arena[T]) AllocSlice(n int) ([]T, error) {
	if a.closed.Load() {
		return nil, ErrClosed
	}

	if n <= 0 {
		return nil, nil
	}

	a.mu.Lock()
	if a.closed.Load() {
		a.mu.Unlock()
		return nil, ErrClosed
	}
	s, err := a.allocSliceLocked(n)
	a.mu.Unlock()

	return s, err
}

// allocSliceLocked performs the contiguous-slice allocation under a.mu.
// It scans forward through retained blocks looking for one with enough
// remaining capacity before falling back to a new allocation.
func (a *Arena[T]) allocSliceLocked(n int) ([]T, error) {
	blk := &a.blocks[a.cur]

	if a.pos+n <= len(blk.data) {
		s := blk.data[a.pos : a.pos+n : a.pos+n]
		var zero T
		for i := range s {
			s[i] = zero
		}
		a.pos += n
		a.stats.addAllocs(uint64(n))
		a.stats.addInUse(uint64(n) * uint64(a.objSz))
		return s, nil
	}

	for a.cur+1 < len(a.blocks) {
		a.cur++
		a.pos = 0
		if n <= len(a.blocks[a.cur].data) {
			return a.allocSliceLocked(n)
		}
	}

	if !a.cfg.growable {
		a.stats.addOOM()
		return nil, ErrOutOfMemory
	}

	if a.cfg.maxBlocks > 0 && len(a.blocks) >= a.cfg.maxBlocks {
		a.stats.addOOM()
		return nil, ErrOutOfMemory
	}

	needed := uintptr(n) * a.objSz
	blockBytes := uintptr(a.cfg.blockSize)
	if needed > blockBytes {
		blockBytes = needed
	}
	objsPerBlock := blockBytes / a.objSz

	newBlk := arenaBlock[T]{data: make([]T, objsPerBlock)}
	a.blocks = append(a.blocks, newBlk)
	a.cur = len(a.blocks) - 1
	a.pos = 0
	a.stats.addBlock()
	a.stats.addGrow()
	a.stats.addBytes(uint64(objsPerBlock) * uint64(a.objSz))

	return a.allocSliceLocked(n)
}

// Reset resets the arena's bump pointer to the beginning, making all
// previously allocated objects invalid. All backing blocks are retained
// and will be reused by subsequent allocations — no memory is freed or
// reallocated.
//
// After Reset, all pointers and slices returned by previous [Arena.Alloc]
// or [Arena.AllocSlice] calls are invalid and must not be used.
func (a *Arena[T]) Reset() {
	if a.closed.Load() {
		return
	}

	a.mu.Lock()
	a.stats.resetActive()
	a.cur = 0
	a.pos = 0
	a.stats.bytesInUse.Store(0)
	a.stats.addReset()
	a.mu.Unlock()
}

// Release releases all backing memory and marks the arena as closed.
// After Release, [Arena.Alloc] and [Arena.AllocSlice] return
// [ErrClosed].
func (a *Arena[T]) Release() {
	if !a.closed.CompareAndSwap(false, true) {
		return
	}

	a.mu.Lock()
	a.stats.resetActive()
	a.blocks = nil
	a.cur = 0
	a.pos = 0
	a.stats.bytesInUse.Store(0)
	a.stats.bytesAllocated.Store(0)
	a.stats.blockCount.Store(0)
	a.mu.Unlock()
}

// Stats returns a point-in-time snapshot of arena statistics.
func (a *Arena[T]) Stats() Stats {
	return a.stats.snapshot()
}

// EnsureCap ensures the arena can hold at least n objects without
// allocating new blocks. The bump pointer is always rewound to the
// beginning. If current capacity is sufficient, existing blocks are
// reused as-is. Otherwise, additional blocks are allocated to reach
// the requested capacity.
//
// This is the recommended pattern for repeated parse/execute cycles
// where the working set size is known ahead of time:
//
//	arena.EnsureCap(tokenCount * 3)
//	// ... allocate within arena ...
//	arena.EnsureCap(nextTokenCount * 3) // resets + resizes if needed
func (a *Arena[T]) EnsureCap(n int) {
	if a.closed.Load() || n <= 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	totalCap := 0
	for i := range a.blocks {
		totalCap += len(a.blocks[i].data)
	}

	if totalCap >= n {
		a.stats.resetActive()
		a.cur = 0
		a.pos = 0
		a.stats.bytesInUse.Store(0)
		a.stats.addReset()
		return
	}

	objsPerBlock := uintptr(a.cfg.blockSize) / a.objSz
	if objsPerBlock == 0 {
		objsPerBlock = 1
	}

	for totalCap < n {
		remaining := n - totalCap
		blockCap := int(objsPerBlock)
		if remaining > blockCap {
			blockCap = remaining
		}
		blk := arenaBlock[T]{data: make([]T, blockCap)}
		a.blocks = append(a.blocks, blk)
		a.stats.addBlock()
		a.stats.addGrow()
		a.stats.addBytes(uint64(blockCap) * uint64(a.objSz))
		totalCap += blockCap
	}

	a.stats.resetActive()
	a.cur = 0
	a.pos = 0
	a.stats.bytesInUse.Store(0)
}

// Cap returns the total number of objects that can be held across all
// currently allocated blocks.
func (a *Arena[T]) Cap() int {
	a.mu.Lock()
	n := 0
	for i := range a.blocks {
		n += len(a.blocks[i].data)
	}
	a.mu.Unlock()

	return n
}

// Len returns the number of objects currently allocated (live) across
// all blocks. This is computed from the bump-pointer position: all
// blocks before the current one are fully used, plus pos slots in the
// current block.
func (a *Arena[T]) Len() int {
	a.mu.Lock()
	n := 0
	for i := 0; i < a.cur; i++ {
		n += len(a.blocks[i].data)
	}
	n += a.pos
	a.mu.Unlock()

	return n
}
