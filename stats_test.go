package slabix

import "testing"

func TestStatsCollectorSnapshot(t *testing.T) {
	var sc statsCollector
	sc.addAlloc()
	sc.addAlloc()
	sc.addAlloc()
	sc.addFree()
	sc.addBytes(4096)
	sc.addInUse(100)
	sc.addBlock()
	sc.addReset()

	s := sc.snapshot()
	if s.Allocs != 3 {
		t.Fatalf("Allocs = %d, want 3", s.Allocs)
	}
	if s.Frees != 1 {
		t.Fatalf("Frees = %d, want 1", s.Frees)
	}
	if s.ActiveObjects != 2 {
		t.Fatalf("ActiveObjects = %d, want 2", s.ActiveObjects)
	}
	if s.BytesAllocated != 4096 {
		t.Fatalf("BytesAllocated = %d, want 4096", s.BytesAllocated)
	}
	if s.BytesInUse != 100 {
		t.Fatalf("BytesInUse = %d, want 100", s.BytesInUse)
	}
	if s.BlockCount != 1 {
		t.Fatalf("BlockCount = %d, want 1", s.BlockCount)
	}
	if s.Resets != 1 {
		t.Fatalf("Resets = %d, want 1", s.Resets)
	}
}

func TestStatsCollectorSubInUse(t *testing.T) {
	var sc statsCollector
	sc.addInUse(100)
	sc.subInUse(40)

	s := sc.snapshot()
	if s.BytesInUse != 60 {
		t.Fatalf("BytesInUse = %d, want 60", s.BytesInUse)
	}
}

func TestStatsCollectorSubInUseZero(t *testing.T) {
	var sc statsCollector
	sc.addInUse(100)
	sc.subInUse(0)
	sc.addInUse(0)

	s := sc.snapshot()
	if s.BytesInUse != 100 {
		t.Fatalf("BytesInUse = %d, want 100 after sub/add zero", s.BytesInUse)
	}
}

func TestStatsCollectorBatchMethods(t *testing.T) {
	var sc statsCollector
	sc.addAllocs(10)
	sc.addFrees(3)

	s := sc.snapshot()
	if s.Allocs != 10 {
		t.Fatalf("Allocs = %d, want 10", s.Allocs)
	}
	if s.Frees != 3 {
		t.Fatalf("Frees = %d, want 3", s.Frees)
	}
	if s.ActiveObjects != 7 {
		t.Fatalf("ActiveObjects = %d, want 7", s.ActiveObjects)
	}
}
