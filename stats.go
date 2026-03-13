package slabix

import "sync/atomic"

// Stats holds a point-in-time snapshot of allocator runtime statistics.
//
// Cumulative counters (Allocs, Frees, OOMs, etc.) grow monotonically
// via atomic operations and never decrease. Gauge-style fields
// (ActiveObjects, BytesInUse) are derived or decremented on free.
//
// The snapshot is consistent for each individual field but not across
// fields — no global lock is held during collection.
type Stats struct {
	// Allocs is the cumulative number of successful allocations.
	Allocs uint64 `json:"allocs"`

	// Frees is the cumulative number of successful frees (or resets
	// that logically free all objects).
	Frees uint64 `json:"frees"`

	// ActiveObjects is Allocs minus Frees: the number of objects
	// currently in use. For arenas this resets to zero on Reset.
	ActiveObjects uint64 `json:"active_objects"`

	// BytesAllocated is the cumulative bytes committed to backing
	// storage (blocks, chunks, buffers). This is the reserved
	// footprint, not the user-visible portion.
	BytesAllocated uint64 `json:"bytes_allocated"`

	// BytesInUse is the number of bytes currently occupied by live
	// objects. Decreases on free or reset.
	BytesInUse uint64 `json:"bytes_in_use"`

	// BlockCount is the current number of backing blocks, chunks, or
	// buffers held by the allocator.
	BlockCount uint64 `json:"block_count"`

	// GrowEvents is the cumulative number of times the allocator
	// expanded its backing storage (added a new block or chunk)
	// beyond the initial allocation.
	GrowEvents uint64 `json:"grow_events"`

	// OOMs is the cumulative number of allocation attempts rejected
	// with ErrOutOfMemory because capacity was exhausted and growth
	// was disabled or capped.
	OOMs uint64 `json:"ooms"`

	// Resets is the number of times the allocator has been bulk-reset
	// (arena only). Zero for Slab and Huge.
	Resets uint64 `json:"resets"`
}

// statsCollector accumulates allocator metrics via lock-free atomic
// counters. It is embedded in every allocator and shared (by pointer)
// across slab shards so that Stats() returns a single unified view.
//
// All methods are designed for the allocation hot path: no locks, no
// allocations, single atomic Add per call.
type statsCollector struct {
	allocs         atomic.Uint64
	frees          atomic.Uint64
	bytesAllocated atomic.Uint64
	bytesInUse     atomic.Uint64
	blockCount     atomic.Uint64
	growEvents     atomic.Uint64
	ooms           atomic.Uint64
	resets         atomic.Uint64
}

// addAlloc increments the successful-allocation counter by one.
func (sc *statsCollector) addAlloc() {
	sc.allocs.Add(1)
}

// addAllocs increments the successful-allocation counter by n (batch path).
func (sc *statsCollector) addAllocs(n uint64) {
	sc.allocs.Add(n)
}

// addFree increments the successful-free counter by one.
func (sc *statsCollector) addFree() {
	sc.frees.Add(1)
}

// addFrees increments the successful-free counter by n (batch path).
func (sc *statsCollector) addFrees(n uint64) {
	sc.frees.Add(n)
}

// addBytes adds n to the cumulative bytes-allocated counter.
func (sc *statsCollector) addBytes(n uint64) {
	sc.bytesAllocated.Add(n)
}

// addInUse adds n to the bytes-in-use gauge. Zero is a no-op to avoid
// a spurious atomic store.
func (sc *statsCollector) addInUse(n uint64) {
	if n > 0 {
		sc.bytesInUse.Add(n)
	}
}

// subInUse subtracts n from the bytes-in-use gauge using two's
// complement addition. Zero is a no-op.
func (sc *statsCollector) subInUse(n uint64) {
	if n > 0 {
		sc.bytesInUse.Add(^(n - 1))
	}
}

// addBlock increments the current block/chunk count by one.
func (sc *statsCollector) addBlock() {
	sc.blockCount.Add(1)
}

// subBlock decrements the current block/chunk count by one. Used by
// Huge when a buffer is freed and is no longer tracked.
func (sc *statsCollector) subBlock() {
	sc.blockCount.Add(^uint64(0))
}

// addGrow increments the grow-event counter. Called when the allocator
// expands beyond its initial backing storage.
func (sc *statsCollector) addGrow() {
	sc.growEvents.Add(1)
}

// addOOM increments the out-of-memory rejection counter.
func (sc *statsCollector) addOOM() {
	sc.ooms.Add(1)
}

// addReset increments the bulk-reset counter (arena only).
func (sc *statsCollector) addReset() {
	sc.resets.Add(1)
}

// resetActive adjusts the free counter so that ActiveObjects
// (allocs − frees) becomes zero. Called when an allocator bulk-frees
// all objects (Reset, EnsureCap, Release).
func (sc *statsCollector) resetActive() {
	allocs := sc.allocs.Load()
	frees := sc.frees.Load()
	if delta := allocs - frees; delta > 0 {
		sc.frees.Add(delta)
	}
}

// snapshot returns a point-in-time Stats copy. Each field is loaded
// independently; the snapshot is per-field consistent but not globally
// linearizable.
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
		GrowEvents:     sc.growEvents.Load(),
		OOMs:           sc.ooms.Load(),
		Resets:         sc.resets.Load(),
	}
}
