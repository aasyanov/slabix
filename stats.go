package slabix

import "sync/atomic"

// Stats holds a point-in-time snapshot of allocator runtime statistics.
//
// Cumulative counters (Allocs, Frees, etc.) are maintained via atomic
// operations and never decrease. The snapshot is consistent for each
// individual field but not across fields — no global lock is held.
type Stats struct {
	// Allocs is the total number of successful allocations.
	Allocs uint64 `json:"allocs"`

	// Frees is the total number of successful frees.
	Frees uint64 `json:"frees"`

	// ActiveObjects is Allocs minus Frees: the number of objects
	// currently in use.
	ActiveObjects uint64 `json:"active_objects"`

	// BytesAllocated is the total number of bytes allocated to backing
	// storage (blocks, slabs). This is the reserved footprint, not the
	// user-visible portion.
	BytesAllocated uint64 `json:"bytes_allocated"`

	// BytesInUse is the number of bytes currently occupied by live
	// objects.
	BytesInUse uint64 `json:"bytes_in_use"`

	// BlockCount is the number of backing blocks or slabs allocated by
	// the allocator.
	BlockCount uint64 `json:"block_count"`

	// Resets is the number of times the allocator has been bulk-reset
	// (arena only).
	Resets uint64 `json:"resets"`
}

// statsCollector holds atomic counters for lock-free stats collection
// on the allocation hot path.
type statsCollector struct {
	allocs         atomic.Uint64
	frees          atomic.Uint64
	bytesAllocated atomic.Uint64
	bytesInUse     atomic.Uint64
	blockCount     atomic.Uint64
	resets         atomic.Uint64
}

func (sc *statsCollector) addAlloc()         { sc.allocs.Add(1) }
func (sc *statsCollector) addAllocs(n uint64) { sc.allocs.Add(n) }
func (sc *statsCollector) addFree()          { sc.frees.Add(1) }
func (sc *statsCollector) addFrees(n uint64) { sc.frees.Add(n) }
func (sc *statsCollector) addBytes(n uint64) { sc.bytesAllocated.Add(n) }
func (sc *statsCollector) addInUse(n uint64) {
	if n > 0 {
		sc.bytesInUse.Add(n)
	}
}
func (sc *statsCollector) subInUse(n uint64) {
	if n > 0 {
		sc.bytesInUse.Add(^(n - 1))
	}
}
func (sc *statsCollector) addBlock()  { sc.blockCount.Add(1) }
func (sc *statsCollector) addReset()  { sc.resets.Add(1) }

func (sc *statsCollector) snapshot() Stats {
	allocs := sc.allocs.Load()
	frees := sc.frees.Load()
	return Stats{
		Allocs:         allocs,
		Frees:          frees,
		ActiveObjects:  allocs - frees,
		BytesAllocated: sc.bytesAllocated.Load(),
		BytesInUse:     sc.bytesInUse.Load(),
		BlockCount:     sc.blockCount.Load(),
		Resets:         sc.resets.Load(),
	}
}
