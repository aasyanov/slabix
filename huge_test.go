package slabix

import (
	"runtime"
	"sync"
	"testing"
)

func TestHugeAllocFree(t *testing.T) {
	h := NewHuge()

	buf, err := h.Alloc(128 * 1024)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if len(buf) != 128*1024 {
		t.Fatalf("got len %d, want %d", len(buf), 128*1024)
	}

	buf[0] = 0xAA
	buf[len(buf)-1] = 0xBB

	if err := h.Free(buf); err != nil {
		t.Fatalf("Free: %v", err)
	}

	stats := h.Stats()
	if stats.Allocs != 1 {
		t.Fatalf("got %d allocs, want 1", stats.Allocs)
	}
	if stats.Frees != 1 {
		t.Fatalf("got %d frees, want 1", stats.Frees)
	}
}

func TestHugeDoubleFree(t *testing.T) {
	h := NewHuge()
	buf, _ := h.Alloc(1024)
	h.Free(buf)

	err := h.Free(buf)
	if err != ErrDoubleFree {
		t.Fatalf("got %v, want ErrDoubleFree", err)
	}
}

func TestHugeNilFree(t *testing.T) {
	h := NewHuge()
	err := h.Free(nil)
	if err != ErrInvalidHandle {
		t.Fatalf("got %v, want ErrInvalidHandle", err)
	}
}

func TestHugeZeroAlloc(t *testing.T) {
	h := NewHuge()
	buf, err := h.Alloc(0)
	if err != nil {
		t.Fatalf("Alloc(0): %v", err)
	}
	if buf != nil {
		t.Fatalf("expected nil for size 0")
	}
}

func TestHugeRelease(t *testing.T) {
	h := NewHuge()
	h.Alloc(1024)
	h.Release()

	_, err := h.Alloc(1024)
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}

	// Double release is safe.
	h.Release()
}

func TestHugeAlignment(t *testing.T) {
	h := NewHuge(WithAlignment(128))
	buf, err := h.Alloc(100)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}

	// The backing capacity should be aligned to 128 bytes.
	if cap(buf) < 128 {
		t.Fatalf("cap = %d, want >= 128", cap(buf))
	}
	h.Free(buf)
}

func TestHugeLen(t *testing.T) {
	h := NewHuge()
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0", h.Len())
	}

	bufs := make([][]byte, 5)
	for i := range bufs {
		bufs[i], _ = h.Alloc(1024)
	}
	if h.Len() != 5 {
		t.Fatalf("Len = %d, want 5", h.Len())
	}

	for _, b := range bufs {
		h.Free(b)
	}
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0", h.Len())
	}
}

func TestHugeMultiple(t *testing.T) {
	h := NewHuge()
	sizes := []int{1024, 64 * 1024, 256 * 1024, 1024 * 1024}
	bufs := make([][]byte, len(sizes))

	for i, sz := range sizes {
		var err error
		bufs[i], err = h.Alloc(sz)
		if err != nil {
			t.Fatalf("Alloc(%d): %v", sz, err)
		}
		if len(bufs[i]) != sz {
			t.Fatalf("len = %d, want %d", len(bufs[i]), sz)
		}
	}

	for _, b := range bufs {
		if err := h.Free(b); err != nil {
			t.Fatalf("Free: %v", err)
		}
	}
}

func TestHugePoolReuse(t *testing.T) {
	h := NewHuge(WithPoolReuse(true))

	buf1, _ := h.Alloc(128 * 1024)
	buf1[0] = 0xAA
	h.Free(buf1)

	buf2, _ := h.Alloc(128 * 1024)
	if buf2[0] != 0 {
		t.Fatalf("reused buffer not zeroed: got %x", buf2[0])
	}
	h.Free(buf2)
}

func TestHugePoolReuseDisabled(t *testing.T) {
	h := NewHuge(WithPoolReuse(false))

	buf, _ := h.Alloc(128 * 1024)
	h.Free(buf)

	buf2, err := h.Alloc(128 * 1024)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if len(buf2) != 128*1024 {
		t.Fatalf("got len %d, want %d", len(buf2), 128*1024)
	}
	h.Free(buf2)
}

func TestHugeLargeBypassesPool(t *testing.T) {
	h := NewHuge()

	buf, _ := h.Alloc(32 * 1024 * 1024) // 32 MB, beyond pool classes
	if len(buf) != 32*1024*1024 {
		t.Fatalf("got len %d, want %d", len(buf), 32*1024*1024)
	}
	h.Free(buf)
}

func TestHugeSizeClasses(t *testing.T) {
	tests := []struct {
		size  int
		class int
	}{
		{1024, 0},              // < 64KB → class 0
		{64 * 1024, 0},         // == 64KB → class 0
		{65 * 1024, 1},         // > 64KB → class 1 (128KB)
		{128 * 1024, 1},        // == 128KB → class 1
		{256 * 1024, 2},        // == 256KB → class 2
		{1024 * 1024, 4},       // == 1MB → class 4
		{16 * 1024 * 1024, 8},  // == 16MB → class 8
		{32 * 1024 * 1024, -1}, // > 16MB → no class
	}

	for _, tt := range tests {
		got := sizeClass(tt.size)
		if got != tt.class {
			t.Errorf("sizeClass(%d) = %d, want %d", tt.size, got, tt.class)
		}
	}
}

func TestHugeConcurrent(t *testing.T) {
	h := NewHuge()
	const goroutines = 8
	const perG = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				buf, err := h.Alloc(4096)
				if err != nil {
					t.Errorf("Alloc: %v", err)
					return
				}
				buf[0] = byte(i)
				if err := h.Free(buf); err != nil {
					t.Errorf("Free: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	stats := h.Stats()
	if stats.Allocs != goroutines*perG {
		t.Fatalf("got %d allocs, want %d", stats.Allocs, goroutines*perG)
	}
}

func TestHugeFreeClosed(t *testing.T) {
	h := NewHuge()
	buf, _ := h.Alloc(1024)
	h.Release()
	err := h.Free(buf)
	if err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}

func TestHugeAllocNegative(t *testing.T) {
	h := NewHuge()
	buf, err := h.Alloc(-1)
	if err != nil {
		t.Fatalf("Alloc(-1): %v", err)
	}
	if buf != nil {
		t.Fatal("expected nil for negative size")
	}
}

func TestHugeEmptySliceFree(t *testing.T) {
	h := NewHuge()
	err := h.Free([]byte{})
	if err != ErrDoubleFree {
		t.Fatalf("got %v, want ErrDoubleFree for empty non-nil slice", err)
	}
}

func TestHugeAllocStatsActive(t *testing.T) {
	h := NewHuge()
	buf1, _ := h.Alloc(1024)
	buf2, _ := h.Alloc(2048)
	s := h.Stats()
	if s.ActiveObjects != 2 {
		t.Fatalf("ActiveObjects = %d, want 2", s.ActiveObjects)
	}
	h.Free(buf1)
	s = h.Stats()
	if s.ActiveObjects != 1 {
		t.Fatalf("ActiveObjects = %d, want 1", s.ActiveObjects)
	}
	h.Free(buf2)
}

// --- Benchmarks ---

func BenchmarkHugeAlloc(b *testing.B) {
	h := NewHuge()
	b.ResetTimer()
	for b.Loop() {
		buf, _ := h.Alloc(128 * 1024)
		h.Free(buf)
	}
}

func BenchmarkHugeAllocParallel(b *testing.B) {
	h := NewHuge()
	b.SetParallelism(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf, _ := h.Alloc(128 * 1024)
			h.Free(buf)
		}
	})
}
