package slabix_test

import (
	"fmt"
	"runtime"

	"github.com/aasyanov/slabix"
)

func ExampleNewArena() {
	arena := slabix.NewArena[int64](
		slabix.WithBlockSize(4096),
	)
	defer arena.Release()

	val, _ := arena.Alloc()
	*val = 42
	fmt.Println(*val)

	arena.Reset()
	fmt.Println("arena reset")

	// Output:
	// 42
	// arena reset
}

func ExampleArena_AllocSlice() {
	arena := slabix.NewArena[float64](
		slabix.WithBlockSize(4096),
	)
	defer arena.Release()

	batch, _ := arena.AllocSlice(3)
	batch[0] = 1.1
	batch[1] = 2.2
	batch[2] = 3.3
	fmt.Println(len(batch))

	// Output:
	// 3
}

func ExampleArena_EnsureCap() {
	arena := slabix.NewArena[int64](
		slabix.WithBlockSize(4096),
	)
	defer arena.Release()

	for cycle := 0; cycle < 3; cycle++ {
		arena.EnsureCap(100)
		for i := 0; i < 100; i++ {
			v, _ := arena.Alloc()
			*v = int64(cycle*100 + i)
		}
	}

	fmt.Println("allocs:", arena.Stats().Allocs)
	fmt.Println("active:", arena.Stats().ActiveObjects)

	// Output:
	// allocs: 300
	// active: 100
}

type CacheEntry struct {
	Key   int64
	Value int64
}

func ExampleNewSlab() {
	pool := slabix.NewSlab[CacheEntry](
		slabix.WithSlabCapacity(1024),
		slabix.WithShards(runtime.GOMAXPROCS(0)),
	)
	defer pool.Release()

	h, _ := pool.Alloc()
	entry := pool.Get(h)
	entry.Key = 1
	entry.Value = 100

	fmt.Println(pool.Get(h).Value)
	pool.Free(h)
	fmt.Println(pool.Get(h) == nil)

	// Output:
	// 100
	// true
}

func ExampleSlab_BatchAlloc() {
	pool := slabix.NewSlab[int64](
		slabix.WithSlabCapacity(256),
	)
	defer pool.Release()

	handles, _ := pool.BatchAlloc(5)
	for i, h := range handles {
		*pool.Get(h) = int64(i * 10)
	}
	fmt.Println(*pool.Get(handles[2]))

	pool.BatchFree(handles)
	fmt.Println("batch freed")

	// Output:
	// 20
	// batch freed
}

func ExampleNewHuge() {
	h := slabix.NewHuge(
		slabix.WithAlignment(64),
	)
	defer h.Release()

	buf, _ := h.Alloc(128 * 1024)
	buf[0] = 0xFF
	fmt.Println(len(buf))

	h.Free(buf)
	fmt.Println(h.Len())

	// Output:
	// 131072
	// 0
}
