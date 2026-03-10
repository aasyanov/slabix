package slabix

import (
	"runtime"
	"sync"
	"testing"
)

type testEntry struct {
	A int64
	B int64
	C float64
}

func TestArenaAlloc(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(1024))

	ptr, err := arena.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if ptr == nil {
		t.Fatal("Alloc returned nil")
	}

	ptr.A = 42
	ptr.B = 99
	if ptr.A != 42 || ptr.B != 99 {
		t.Fatalf("got A=%d B=%d, want 42, 99", ptr.A, ptr.B)
	}
}

func TestArenaAllocZeroed(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(4096))

	p1, _ := arena.Alloc()
	p1.A = 100
	p1.B = 200

	arena.Reset()

	p2, _ := arena.Alloc()
	if p2.A != 0 || p2.B != 0 {
		t.Fatalf("after Reset, got A=%d B=%d, want 0, 0", p2.A, p2.B)
	}
}

func TestArenaMultipleBlocks(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 4)) // ~4 objects per block

	for i := 0; i < 20; i++ {
		ptr, err := arena.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		ptr.A = int64(i)
	}

	stats := arena.Stats()
	if stats.Allocs != 20 {
		t.Fatalf("got %d allocs, want 20", stats.Allocs)
	}
	if stats.BlockCount < 2 {
		t.Fatalf("got %d blocks, want >= 2", stats.BlockCount)
	}
}

func TestArenaMaxBlocks(t *testing.T) {
	arena := NewArena[testEntry](
		WithBlockSize(24*2),
		WithMaxBlocks(2),
	)

	allocated := 0
	for i := 0; i < 100; i++ {
		_, err := arena.Alloc()
		if err != nil {
			break
		}
		allocated++
	}

	if allocated >= 100 {
		t.Fatal("expected OOM before 100 allocations")
	}
}

func TestArenaNotGrowable(t *testing.T) {
	arena := NewArena[testEntry](
		WithBlockSize(24*2),
		WithGrowable(false),
	)

	allocated := 0
	for i := 0; i < 100; i++ {
		_, err := arena.Alloc()
		if err != nil {
			if err != ErrOutOfMemory {
				t.Fatalf("got %v, want ErrOutOfMemory", err)
			}
			break
		}
		allocated++
	}

	if allocated >= 100 {
		t.Fatal("expected OOM before 100 allocations")
	}
}

func TestArenaReset(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(4096))

	for i := 0; i < 50; i++ {
		arena.Alloc()
	}

	arena.Reset()

	stats := arena.Stats()
	if stats.BytesInUse != 0 {
		t.Fatalf("after Reset, BytesInUse = %d, want 0", stats.BytesInUse)
	}
	if stats.Resets != 1 {
		t.Fatalf("got %d resets, want 1", stats.Resets)
	}
	if arena.Len() != 0 {
		t.Fatalf("after Reset, Len = %d, want 0", arena.Len())
	}

	// Can allocate again after reset.
	_, err := arena.Alloc()
	if err != nil {
		t.Fatalf("Alloc after Reset: %v", err)
	}
}

func TestArenaRelease(t *testing.T) {
	arena := NewArena[testEntry]()
	arena.Alloc()
	arena.Release()

	_, err := arena.Alloc()
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}

	// Double release is safe.
	arena.Release()
}

func TestArenaAllocSlice(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(4096))

	s, err := arena.AllocSlice(10)
	if err != nil {
		t.Fatalf("AllocSlice: %v", err)
	}
	if len(s) != 10 {
		t.Fatalf("got len %d, want 10", len(s))
	}

	for i := range s {
		if s[i].A != 0 || s[i].B != 0 {
			t.Fatalf("slice[%d] not zeroed", i)
		}
		s[i].A = int64(i)
	}

	// Verify contiguity: indices are sequential.
	for i := range s {
		if s[i].A != int64(i) {
			t.Fatalf("slice[%d].A = %d, want %d", i, s[i].A, i)
		}
	}
}

func TestArenaAllocSliceZero(t *testing.T) {
	arena := NewArena[testEntry]()
	s, err := arena.AllocSlice(0)
	if err != nil {
		t.Fatalf("AllocSlice(0): %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil slice for n=0")
	}
}

func TestArenaAllocSliceLargerThanBlock(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 4)) // ~4 objects
	s, err := arena.AllocSlice(20)
	if err != nil {
		t.Fatalf("AllocSlice: %v", err)
	}
	if len(s) != 20 {
		t.Fatalf("got len %d, want 20", len(s))
	}
}

func TestArenaConcurrent(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(4096))
	const goroutines = 8
	const perG = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ptr, err := arena.Alloc()
				if err != nil {
					t.Errorf("Alloc: %v", err)
					return
				}
				ptr.A = int64(i)
			}
		}()
	}
	wg.Wait()

	stats := arena.Stats()
	if stats.Allocs != goroutines*perG {
		t.Fatalf("got %d allocs, want %d", stats.Allocs, goroutines*perG)
	}
}

func TestArenaCapLen(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 10))
	if arena.Cap() == 0 {
		t.Fatal("Cap should be > 0 after construction")
	}
	if arena.Len() != 0 {
		t.Fatalf("Len = %d, want 0", arena.Len())
	}

	for i := 0; i < 5; i++ {
		arena.Alloc()
	}
	if arena.Len() != 5 {
		t.Fatalf("Len = %d, want 5", arena.Len())
	}
}

func TestArenaEnsureCapReuse(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 10))

	for i := 0; i < 5; i++ {
		arena.Alloc()
	}

	arena.EnsureCap(10)

	if arena.Len() != 0 {
		t.Fatalf("after EnsureCap, Len = %d, want 0", arena.Len())
	}
	if arena.Cap() < 10 {
		t.Fatalf("after EnsureCap(10), Cap = %d, want >= 10", arena.Cap())
	}

	for i := 0; i < 10; i++ {
		_, err := arena.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d after EnsureCap: %v", i, err)
		}
	}
}

func TestArenaEnsureCapGrow(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 4))

	arena.EnsureCap(100)

	if arena.Cap() < 100 {
		t.Fatalf("after EnsureCap(100), Cap = %d, want >= 100", arena.Cap())
	}
	if arena.Len() != 0 {
		t.Fatalf("after EnsureCap, Len = %d, want 0", arena.Len())
	}

	for i := 0; i < 100; i++ {
		_, err := arena.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
	}
}

func TestArenaEnsureCapCycle(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 50))

	for cycle := 0; cycle < 5; cycle++ {
		arena.EnsureCap(30)
		for i := 0; i < 30; i++ {
			ptr, err := arena.Alloc()
			if err != nil {
				t.Fatalf("cycle %d, Alloc %d: %v", cycle, i, err)
			}
			ptr.A = int64(cycle*30 + i)
		}
	}

	stats := arena.Stats()
	if stats.Resets < 5 {
		t.Fatalf("expected >= 5 resets, got %d", stats.Resets)
	}
}

func TestArenaEnsureCapZero(t *testing.T) {
	arena := NewArena[testEntry]()
	arena.EnsureCap(0)  // should be a no-op
	arena.EnsureCap(-1) // should be a no-op
	if arena.Len() != 0 {
		t.Fatalf("Len = %d after zero EnsureCap", arena.Len())
	}
}

func TestArenaAllocSliceClosed(t *testing.T) {
	arena := NewArena[testEntry]()
	arena.Release()
	_, err := arena.AllocSlice(10)
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}

func TestArenaAllocSliceNotGrowable(t *testing.T) {
	arena := NewArena[testEntry](
		WithBlockSize(24*4),
		WithGrowable(false),
	)
	_, err := arena.AllocSlice(100)
	if err != ErrOutOfMemory {
		t.Fatalf("got %v, want ErrOutOfMemory", err)
	}
}

func TestArenaAllocSliceMaxBlocks(t *testing.T) {
	arena := NewArena[testEntry](
		WithBlockSize(24*4),
		WithMaxBlocks(1),
	)
	_, err := arena.AllocSlice(100)
	if err != ErrOutOfMemory {
		t.Fatalf("got %v, want ErrOutOfMemory", err)
	}
}

func TestArenaResetClosed(t *testing.T) {
	arena := NewArena[testEntry]()
	arena.Release()
	arena.Reset() // should be a no-op, not panic
}

func TestArenaEnsureCapClosed(t *testing.T) {
	arena := NewArena[testEntry]()
	arena.Release()
	arena.EnsureCap(100) // should be a no-op
}

func TestArenaLenMultiBlock(t *testing.T) {
	arena := NewArena[testEntry](WithBlockSize(24 * 4))
	for i := 0; i < 20; i++ {
		arena.Alloc()
	}
	l := arena.Len()
	if l != 20 {
		t.Fatalf("Len = %d, want 20", l)
	}
}

// --- Benchmarks ---

func BenchmarkArenaAlloc(b *testing.B) {
	arena := NewArena[testEntry](WithBlockSize(1 << 22))
	b.ResetTimer()
	for b.Loop() {
		arena.Alloc()
	}
}

func BenchmarkArenaAllocSlice(b *testing.B) {
	arena := NewArena[testEntry](WithBlockSize(1 << 22))
	b.ResetTimer()
	for b.Loop() {
		arena.AllocSlice(64)
	}
}

func BenchmarkArenaAllocResetCycle(b *testing.B) {
	arena := NewArena[testEntry](WithBlockSize(1 << 22))
	b.ResetTimer()
	for b.Loop() {
		for j := 0; j < 1000; j++ {
			arena.Alloc()
		}
		arena.Reset()
	}
}

func BenchmarkArenaAllocParallel(b *testing.B) {
	arena := NewArena[testEntry](WithBlockSize(1 << 22))
	b.SetParallelism(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			arena.Alloc()
		}
	})
}
