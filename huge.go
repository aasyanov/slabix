package slabix

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/aasyanov/slabix/internal/align"
)

// Huge is an allocator for large byte buffers that would fragment slab
// or arena memory. Each allocation is backed by its own Go slice.
//
// When [WithPoolReuse] is enabled (the default), freed buffers are
// returned to internal size-class pools for reuse instead of being
// released to the garbage collector. Size classes are powers of two
// from 64 KB (1<<16) to 16 MB (1<<24); buffers larger than 16 MB
// bypass the pool entirely.
//
// Huge is designed for buffers in the 64 KB – 16 MB range: WAL
// segments, compaction buffers, bulk I/O pages. Smaller allocations
// work but waste memory because the backing buffer is rounded up to
// the smallest size-class boundary (64 KB minimum).
//
//	h := slabix.NewHuge()
//	buf, _ := h.Alloc(128 * 1024)
//	_ = h.Free(buf)
//
// Huge is safe for concurrent use.
type Huge struct {
	cfg    hugeConfig
	mu     sync.Mutex
	live   map[uintptr]hugeEntry
	pools  [hugeSizeClasses]sync.Pool
	closed atomic.Bool
	stats  statsCollector
}

// hugeEntry tracks a single live allocation: the full backing buffer
// and the pool size-class it belongs to (-1 if it bypasses the pool).
type hugeEntry struct {
	buf   []byte
	class int
}

// Size-class boundaries for the internal buffer pool.
// Buffers are bucketed into powers of two from 2^hugeMinClass (64 KB)
// to 2^hugeMaxClass (16 MB). Larger allocations bypass the pool.
const (
	hugeMinClass    = 16 // 64 KB
	hugeMaxClass    = 24 // 16 MB
	hugeSizeClasses = hugeMaxClass - hugeMinClass + 1
)

// NewHuge creates a [Huge] allocator for large byte buffers.
func NewHuge(opts ...HugeOption) *Huge {
	cfg := defaultHugeConfig()
	for _, o := range opts {
		o(&cfg)
	}

	return &Huge{
		cfg:  cfg,
		live: make(map[uintptr]hugeEntry),
	}
}

// Alloc allocates a byte slice of exactly size bytes. The returned
// slice is zeroed. The backing buffer may be larger than size due to
// alignment and size-class rounding (minimum 64 KB when pool reuse is
// enabled).
//
// The caller must pass the exact returned slice to [Huge.Free] when
// done. Re-slicing the buffer (e.g. buf[1:]) before Free will cause
// Free to fail.
//
// Alloc(0) and Alloc with negative size return (nil, nil).
func (h *Huge) Alloc(size int) ([]byte, error) {
	if h.closed.Load() {
		return nil, ErrClosed
	}

	if size <= 0 {
		return nil, nil
	}

	aligned := int(align.Of(uintptr(size), uintptr(h.cfg.alignment)))
	class := sizeClass(aligned)

	var buf []byte
	if h.cfg.poolReuse && class >= 0 {
		if v := h.pools[class].Get(); v != nil {
			buf = *v.(*[]byte)
			clear(buf)
		}
	}

	if buf == nil {
		allocSz := aligned
		if class >= 0 {
			allocSz = 1 << (class + hugeMinClass)
		}
		buf = make([]byte, allocSz)
	}

	key := sliceKey(buf)
	h.mu.Lock()
	if h.live == nil {
		h.mu.Unlock()
		return nil, ErrClosed
	}
	h.live[key] = hugeEntry{buf: buf, class: class}
	h.mu.Unlock()

	h.stats.addAlloc()
	h.stats.addBytes(uint64(len(buf)))
	h.stats.addInUse(uint64(len(buf)))
	h.stats.addBlock()

	return buf[:size], nil
}

// Free releases a buffer previously returned by [Huge.Alloc]. The
// buffer must not be used after Free. The caller must pass the exact
// slice returned by Alloc — re-slicing or appending invalidates it.
//
// If pool reuse is enabled, the backing buffer may be returned to an
// internal pool for future allocations. Returns [ErrInvalidHandle]
// for nil or empty slices, [ErrDoubleFree] if the buffer was already
// freed.
func (h *Huge) Free(buf []byte) error {
	if h.closed.Load() {
		return ErrClosed
	}

	if len(buf) == 0 {
		return ErrInvalidHandle
	}

	key := sliceKey(buf)
	h.mu.Lock()
	entry, ok := h.live[key]
	if ok {
		delete(h.live, key)
	}
	h.mu.Unlock()

	if !ok {
		return ErrDoubleFree
	}

	sz := uint64(len(entry.buf))
	h.stats.addFree()
	h.stats.subInUse(sz)
	h.stats.subBlock()

	if h.cfg.poolReuse && entry.class >= 0 {
		h.pools[entry.class].Put(&entry.buf)
	}

	return nil
}

// Stats returns a point-in-time snapshot of huge allocator statistics.
func (h *Huge) Stats() Stats {
	return h.stats.snapshot()
}

// Release releases all tracked allocations and marks the allocator as
// closed. Pool buffers become eligible for garbage collection.
func (h *Huge) Release() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}

	h.mu.Lock()
	h.live = nil
	h.mu.Unlock()

	h.stats.bytesInUse.Store(0)
	h.stats.bytesAllocated.Store(0)
	h.stats.blockCount.Store(0)
}

// Len returns the number of live (un-freed) allocations.
func (h *Huge) Len() int {
	h.mu.Lock()
	n := len(h.live)
	h.mu.Unlock()
	return n
}

// sizeClass maps an aligned allocation size to a pool bucket index.
// Sizes up to 2^hugeMinClass land in class 0, sizes up to 2^(hugeMinClass+1)
// in class 1, and so on. Returns -1 if size exceeds 2^hugeMaxClass.
func sizeClass(size int) int {
	if size < (1 << hugeMinClass) {
		return 0
	}

	for i := 0; i < hugeSizeClasses; i++ {
		if size <= (1 << (i + hugeMinClass)) {
			return i
		}
	}

	return -1
}

// sliceKey derives a map key from the slice's backing-array pointer.
// Two slices sharing the same backing array at offset 0 produce the
// same key. Returns 0 for empty or nil slices.
func sliceKey(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}

	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}
