package slabix

import "errors"

// Sentinel errors returned by slabix allocators.
//
// All errors are created via [errors.New] and can be compared directly
// with == or used with [errors.Is].
var (
	// ErrClosed is returned when an operation is attempted on a closed
	// or released allocator.
	ErrClosed = errors.New("slabix: allocator is closed")

	// ErrOutOfMemory is returned when an allocation cannot be satisfied
	// because growth is disabled ([WithGrowable]/[WithSlabGrowable]),
	// or because the configured [WithMaxBlocks] or [WithMaxChunks]
	// limit has been reached.
	ErrOutOfMemory = errors.New("slabix: out of memory")

	// ErrDoubleFree is returned when [Slab.Free] or [Huge.Free] is
	// called with a handle or buffer that has already been freed.
	ErrDoubleFree = errors.New("slabix: double free")

	// ErrInvalidHandle is returned when [Slab.Get], [Slab.Free], or
	// [Huge.Free] receives a handle or buffer that does not belong to
	// the allocator, has been invalidated by a generation increment,
	// or is nil.
	ErrInvalidHandle = errors.New("slabix: invalid handle")

	// ErrTooLarge is returned when the requested allocation size exceeds
	// the allocator's maximum object size. Reserved for future use.
	ErrTooLarge = errors.New("slabix: allocation too large")
)
