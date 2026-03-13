package slabix

import (
	"runtime"
	"sync"
	"testing"
)

type testNode struct {
	Key   int64
	Value int64
	Left  Handle[testNode]
	Right Handle[testNode]
}

func TestSlabAllocFree(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64))

	h, err := pool.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if h.IsZero() {
		t.Fatal("got zero handle")
	}

	node := pool.Get(h)
	if node == nil {
		t.Fatal("Get returned nil")
	}

	node.Key = 42
	node.Value = 99

	got := pool.Get(h)
	if got.Key != 42 || got.Value != 99 {
		t.Fatalf("got Key=%d Value=%d, want 42, 99", got.Key, got.Value)
	}

	if err := pool.Free(h); err != nil {
		t.Fatalf("Free: %v", err)
	}

	if pool.Get(h) != nil {
		t.Fatal("Get after Free should return nil")
	}
}

func TestSlabDoubleFree(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64))
	h, _ := pool.Alloc()
	pool.Free(h)

	err := pool.Free(h)
	if err == nil {
		t.Fatal("expected error on double free")
	}
}

func TestSlabInvalidHandle(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64))

	err := pool.Free(Handle[testNode]{})
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle", err)
	}

	if pool.Get(Handle[testNode]{}) != nil {
		t.Fatal("Get with zero handle should return nil")
	}
}

func TestSlabGrowth(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4))

	handles := make([]Handle[testNode], 0, 100)
	for i := 0; i < 100; i++ {
		h, err := pool.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		pool.Get(h).Key = int64(i)
		handles = append(handles, h)
	}

	for i, h := range handles {
		node := pool.Get(h)
		if node == nil {
			t.Fatalf("Get(%d) returned nil", i)
		}
		if node.Key != int64(i) {
			t.Fatalf("node[%d].Key = %d, want %d", i, node.Key, i)
		}
	}

	stats := pool.Stats()
	if stats.Allocs != 100 {
		t.Fatalf("got %d allocs, want 100", stats.Allocs)
	}
}

func TestSlabNotGrowable(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4), WithSlabGrowable(false))

	for i := 0; i < 4; i++ {
		_, err := pool.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
	}

	_, err := pool.Alloc()
	if err != ErrOutOfMemory {
		t.Fatalf("got %v, want ErrOutOfMemory", err)
	}
}

func TestSlabReuseAfterFree(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4), WithSlabGrowable(false))

	h1, _ := pool.Alloc()
	pool.Get(h1).Key = 1
	pool.Free(h1)

	h2, err := pool.Alloc()
	if err != nil {
		t.Fatalf("Alloc after Free: %v", err)
	}

	node := pool.Get(h2)
	if node == nil {
		t.Fatal("Get returned nil after reuse")
	}
	if node.Key != 0 {
		t.Fatalf("reused slot not zeroed: Key=%d", node.Key)
	}
}

func TestSlabBatchAllocFree(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(256))

	handles, err := pool.BatchAlloc(50)
	if err != nil {
		t.Fatalf("BatchAlloc: %v", err)
	}
	if len(handles) != 50 {
		t.Fatalf("got %d handles, want 50", len(handles))
	}

	for i, h := range handles {
		pool.Get(h).Key = int64(i)
	}

	if err := pool.BatchFree(handles); err != nil {
		t.Fatalf("BatchFree: %v", err)
	}

	stats := pool.Stats()
	if stats.Allocs != 50 {
		t.Fatalf("got %d allocs, want 50", stats.Allocs)
	}
	if stats.Frees != 50 {
		t.Fatalf("got %d frees, want 50", stats.Frees)
	}
}

func TestSlabSharding(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64), WithShards(4))

	handles := make([]Handle[testNode], 0, 100)
	for i := 0; i < 100; i++ {
		h, err := pool.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		pool.Get(h).Key = int64(i)
		handles = append(handles, h)
	}

	for i, h := range handles {
		node := pool.Get(h)
		if node == nil {
			t.Fatalf("Get(%d) returned nil", i)
		}
		if node.Key != int64(i) {
			t.Fatalf("node[%d].Key = %d, want %d", i, node.Key, i)
		}
	}

	for _, h := range handles {
		pool.Free(h)
	}
}

func TestSlabRelease(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64))
	pool.Alloc()
	pool.Release()

	_, err := pool.Alloc()
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}

	// Double release is safe.
	pool.Release()
}

func TestSlabCap(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(128))
	if pool.Cap() < 128 {
		t.Fatalf("Cap = %d, want >= 128", pool.Cap())
	}
}

func TestSlabGrowthCapped(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(4),
		WithGrowthPolicy(GrowAdaptive),
	)

	for i := 0; i < 10000; i++ {
		h, err := pool.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		pool.Get(h).Key = int64(i)
	}

	stats := pool.Stats()
	if stats.Allocs != 10000 {
		t.Fatalf("got %d allocs, want 10000", stats.Allocs)
	}

	cap := pool.Cap()
	if cap > 20000 {
		t.Fatalf("Cap = %d, suspiciously large (growth should be capped)", cap)
	}
}

func TestSlabGrowthFixed(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(16),
		WithGrowthPolicy(GrowFixed),
	)

	for i := 0; i < 100; i++ {
		pool.Alloc()
	}

	cap := pool.Cap()
	expectedChunks := (100 / 16) + 1
	if cap > expectedChunks*16+16 {
		t.Fatalf("GrowFixed: Cap = %d, expected <= %d", cap, expectedChunks*16+16)
	}
}

func TestSlabGrowthLinear(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(8),
		WithGrowthPolicy(GrowLinear),
	)

	for i := 0; i < 100; i++ {
		pool.Alloc()
	}

	stats := pool.Stats()
	if stats.Allocs != 100 {
		t.Fatalf("got %d allocs, want 100", stats.Allocs)
	}
}

func TestSlabMaxChunks(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(4),
		WithMaxChunks(3),
		WithGrowthPolicy(GrowFixed),
	)

	allocated := 0
	for i := 0; i < 100; i++ {
		_, err := pool.Alloc()
		if err != nil {
			if err != ErrOutOfMemory {
				t.Fatalf("got %v, want ErrOutOfMemory", err)
			}
			break
		}
		allocated++
	}

	if allocated > 12 {
		t.Fatalf("allocated %d with maxChunks=3 and capacity=4, expected <= 12", allocated)
	}
	if allocated == 100 {
		t.Fatal("expected OOM before 100 allocations")
	}
}

func TestSlabMaxChunksWithShards(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(8),
		WithShards(2),
		WithMaxChunks(2),
		WithGrowthPolicy(GrowFixed),
	)

	allocated := 0
	for i := 0; i < 100; i++ {
		_, err := pool.Alloc()
		if err != nil {
			break
		}
		allocated++
	}

	if allocated == 100 {
		t.Fatal("expected OOM before 100 allocations with maxChunks=2")
	}
}

func TestSlabConcurrent(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(256),
		WithShards(runtime.GOMAXPROCS(0)),
	)
	const goroutines = 8
	const perG = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				h, err := pool.Alloc()
				if err != nil {
					t.Errorf("Alloc: %v", err)
					return
				}
				pool.Get(h).Key = int64(i)
				if err := pool.Free(h); err != nil {
					t.Errorf("Free: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	stats := pool.Stats()
	if stats.Allocs != goroutines*perG {
		t.Fatalf("got %d allocs, want %d", stats.Allocs, goroutines*perG)
	}
	if stats.Frees != goroutines*perG {
		t.Fatalf("got %d frees, want %d", stats.Frees, goroutines*perG)
	}
}

func TestSlabGetOutOfRangeShard(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0xFF << 16, index: 0}
	if pool.Get(h) != nil {
		t.Fatal("expected nil for out-of-range shard")
	}
}

func TestSlabGetOutOfRangeChunk(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0x00FF, index: 0}
	if pool.Get(h) != nil {
		t.Fatal("expected nil for out-of-range chunk")
	}
}

func TestSlabGetOutOfRangeSlot(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0, index: 0xFFFFF}
	if pool.Get(h) != nil {
		t.Fatal("expected nil for out-of-range slot")
	}
}

func TestSlabFreeOutOfRangeShard(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0xFF << 16, index: 1}
	err := pool.Free(h)
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle", err)
	}
}

func TestSlabFreeOutOfRangeChunk(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0x00FF, index: 1}
	err := pool.Free(h)
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle", err)
	}
}

func TestSlabFreeOutOfRangeSlot(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h := Handle[testNode]{chunk: 0, index: 0xFFFFF | (1 << 20)}
	err := pool.Free(h)
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle", err)
	}
}

func TestSlabFreeClosed(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h, _ := pool.Alloc()
	pool.Release()
	err := pool.Free(h)
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}

func TestSlabGetClosed(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h, _ := pool.Alloc()
	pool.Release()
	if pool.Get(h) != nil {
		t.Fatal("expected nil for closed pool")
	}
}

func TestSlabBatchAllocClosed(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	pool.Release()
	_, err := pool.BatchAlloc(5)
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}

func TestSlabBatchAllocZero(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	handles, err := pool.BatchAlloc(0)
	if err != nil {
		t.Fatalf("BatchAlloc(0): %v", err)
	}
	if handles != nil {
		t.Fatal("expected nil handles for n=0")
	}
}

func TestSlabBatchHintOption(t *testing.T) {
	pool := NewSlab[testNode](
		WithSlabCapacity(64),
		WithBatchHint(128),
	)
	handles, err := pool.BatchAlloc(10)
	if err != nil {
		t.Fatalf("BatchAlloc: %v", err)
	}
	if len(handles) != 10 {
		t.Fatalf("got %d handles, want 10", len(handles))
	}
	pool.BatchFree(handles)
}

func TestSlabFreeStaleGeneration(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4), WithSlabGrowable(false))
	h1, _ := pool.Alloc()
	pool.Free(h1)
	h2, _ := pool.Alloc()
	_ = h2
	err := pool.Free(h1)
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle (stale generation)", err)
	}
}

func TestSlabLen(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(64))
	if pool.Len() != 0 {
		t.Fatalf("Len = %d, want 0", pool.Len())
	}

	handles := make([]Handle[testNode], 0, 10)
	for i := 0; i < 10; i++ {
		h, _ := pool.Alloc()
		handles = append(handles, h)
	}
	if pool.Len() != 10 {
		t.Fatalf("Len = %d, want 10", pool.Len())
	}

	for _, h := range handles {
		pool.Free(h)
	}
	if pool.Len() != 0 {
		t.Fatalf("after free all, Len = %d, want 0", pool.Len())
	}
}

func TestSlabBatchAllocNegative(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	handles, err := pool.BatchAlloc(-1)
	if err != nil {
		t.Fatalf("BatchAlloc(-1): %v", err)
	}
	if handles != nil {
		t.Fatal("expected nil handles for negative n")
	}
}

func TestSlabOOMStats(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4), WithSlabGrowable(false))

	for i := 0; i < 4; i++ {
		pool.Alloc()
	}

	pool.Alloc()
	pool.Alloc()

	s := pool.Stats()
	if s.OOMs != 2 {
		t.Fatalf("OOMs = %d, want 2", s.OOMs)
	}
}

func TestSlabGrowEventsStats(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(4))

	for i := 0; i < 20; i++ {
		pool.Alloc()
	}

	s := pool.Stats()
	if s.GrowEvents == 0 {
		t.Fatal("GrowEvents = 0 after exceeding initial capacity, want > 0")
	}
	if s.GrowEvents >= s.BlockCount {
		t.Fatalf("GrowEvents (%d) >= BlockCount (%d), want GrowEvents < BlockCount (initial chunks are not grows)",
			s.GrowEvents, s.BlockCount)
	}
}

func TestSlabBatchFreeMixed(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(8))
	h1, _ := pool.Alloc()
	h2, _ := pool.Alloc()
	pool.Free(h1)

	err := pool.BatchFree([]Handle[testNode]{h1, h2})
	if err == nil {
		t.Fatal("expected error from BatchFree with already-freed handle")
	}

	if pool.Get(h2) != nil {
		t.Fatal("h2 should have been freed despite h1 error")
	}
}

func TestSlabLastFreeChunkHintPreserved(t *testing.T) {
	pool := NewSlab[testNode](WithSlabCapacity(2), WithGrowthPolicy(GrowFixed))

	h1, _ := pool.Alloc()
	h2, _ := pool.Alloc()
	h3, _ := pool.Alloc()
	_ = h3

	pool.Free(h1)

	h4, err := pool.Alloc()
	if err != nil {
		t.Fatalf("Alloc after free: %v", err)
	}
	if h4.IsZero() {
		t.Fatal("got zero handle")
	}

	pool.Free(h2)
	pool.Free(h4)
}

// --- Benchmarks ---

func BenchmarkSlabAlloc(b *testing.B) {
	pool := NewSlab[testNode](WithSlabCapacity(1 << 16))
	b.ResetTimer()
	for b.Loop() {
		pool.Alloc()
	}
}

func BenchmarkSlabAllocFree(b *testing.B) {
	pool := NewSlab[testNode](WithSlabCapacity(1 << 16))
	b.ResetTimer()
	for b.Loop() {
		h, _ := pool.Alloc()
		pool.Free(h)
	}
}

func BenchmarkSlabBatchAlloc(b *testing.B) {
	pool := NewSlab[testNode](WithSlabCapacity(1 << 16))
	b.ResetTimer()
	for b.Loop() {
		handles, _ := pool.BatchAlloc(128)
		pool.BatchFree(handles)
	}
}

func BenchmarkSlabAllocParallel(b *testing.B) {
	pool := NewSlab[testNode](
		WithSlabCapacity(1<<16),
		WithShards(runtime.GOMAXPROCS(0)),
	)
	b.SetParallelism(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h, _ := pool.Alloc()
			pool.Free(h)
		}
	})
}

func BenchmarkSlabAllocFreeParallel(b *testing.B) {
	pool := NewSlab[testNode](
		WithSlabCapacity(1<<16),
		WithShards(runtime.GOMAXPROCS(0)),
	)
	b.SetParallelism(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h, _ := pool.Alloc()
			_ = pool.Get(h)
			pool.Free(h)
		}
	})
}
