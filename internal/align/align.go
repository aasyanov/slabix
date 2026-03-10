// Package align provides low-level memory alignment helpers for slabix
// allocators. This package is internal and not part of the public API.
package align

import "unsafe"

// CacheLine is the typical CPU cache line size in bytes.
const CacheLine = 64

// Of returns the smallest multiple of alignment that is >= n.
// alignment must be a power of two.
func Of(n, alignment uintptr) uintptr {
	return (n + alignment - 1) &^ (alignment - 1)
}

// SizeOf returns the aligned size of T, rounded up to the given alignment.
func SizeOf[T any](alignment uintptr) uintptr {
	sz := unsafe.Sizeof(*new(T))
	return Of(sz, alignment)
}

// IsPowerOfTwo reports whether n is a power of two.
func IsPowerOfTwo(n uintptr) bool {
	return n > 0 && n&(n-1) == 0
}
