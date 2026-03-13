package slabix

// Handle is a typed, opaque reference to an object managed by a [Slab]
// allocator. Handles are the primary way to interact with slab-allocated
// objects without exposing raw pointers.
//
// A Handle is valid only within the [Slab] that created it. Passing
// a handle to a different slab or using it after [Slab.Free] results
// in [ErrInvalidHandle].
//
// # Bit-packing layout
//
// Handle packs shard, chunk, slot, and generation into two uint32 fields:
//
//	chunk: [shard 16 bits | chunkIdx 16 bits]
//	index: [gen 12 bits  | slotIdx 20 bits ]
//
// This yields the following limits:
//   - Max shards:          65,536  (2^16)
//   - Max chunks per shard: 65,536  (2^16)
//   - Max slots per chunk:  1,048,576  (2^20)
//   - Generation counter:   4,096 values (2^12) before wraparound
//
// All limits are enforced at runtime: exceeding the slot count per
// chunk panics in [NewSlab]; exceeding the chunk count per shard or
// the shard count returns [ErrOutOfMemory] or panics, respectively.
//
// The generation counter wraps around after 4,096 alloc/free cycles on
// the same slot. A stale handle whose generation has wrapped to the
// current value will appear valid — this is an accepted trade-off for
// compact handles. In practice, 4,096 cycles per slot is sufficient
// for most workloads.
//
// The zero value is invalid and safe to compare:
//
//	var h slabix.Handle[Node]
//	if h.IsZero() { ... }
type Handle[T any] struct {
	chunk uint32
	index uint32
}

// IsZero reports whether h is the zero handle (never allocated).
func (h Handle[T]) IsZero() bool {
	return h.chunk == 0 && h.index == 0
}
