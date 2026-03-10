package slabix

// Handle is a typed, opaque reference to an object managed by a [Slab]
// allocator. Handles are the primary way to interact with slab-allocated
// objects without exposing raw pointers.
//
// A Handle is valid only within the [Slab] that created it. Passing
// a handle to a different slab or using it after [Slab.Free] results
// in [ErrInvalidHandle].
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
