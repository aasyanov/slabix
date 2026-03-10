package slabix

// GrowthPolicy controls how allocators expand when capacity is exhausted.
type GrowthPolicy int

const (
	// GrowFixed allocates every new chunk at the same size as the
	// initial chunk. No growth. Predictable memory footprint.
	GrowFixed GrowthPolicy = iota

	// GrowLinear adds chunks of a fixed additional size (the initial
	// capacity). Memory grows linearly: cap, 2*cap, 3*cap, ...
	GrowLinear

	// GrowAdaptive doubles chunk size until a ceiling (initial capacity
	// or 4096, whichever is larger), then switches to fixed-size chunks.
	// This is the default — fast ramp-up, bounded steady-state.
	GrowAdaptive
)

// arenaConfig holds all Arena configuration. Assembled via functional
// [ArenaOption] values passed to [NewArena].
type arenaConfig struct {
	blockSize int
	maxBlocks int
	growable  bool
}

func defaultArenaConfig() arenaConfig {
	return arenaConfig{
		blockSize: 1 << 22, // 4 MB
		maxBlocks: 0,       // unlimited
		growable:  true,
	}
}

// ArenaOption configures an [Arena]. Options are passed to [NewArena]
// and applied in order; the last value wins for each setting.
type ArenaOption func(*arenaConfig)

// WithBlockSize sets the size in bytes of each arena block. The arena
// allocates memory in contiguous blocks of this size. Must be positive.
// Default: 4 MB.
func WithBlockSize(n int) ArenaOption {
	return func(c *arenaConfig) {
		if n > 0 {
			c.blockSize = n
		}
	}
}

// WithMaxBlocks sets the maximum number of blocks the arena may
// allocate. Zero means unlimited. When the limit is reached and the
// arena is not growable, [Arena.Alloc] returns [ErrOutOfMemory].
func WithMaxBlocks(n int) ArenaOption {
	return func(c *arenaConfig) {
		if n >= 0 {
			c.maxBlocks = n
		}
	}
}

// WithGrowable controls whether the arena allocates new blocks when
// the current block is exhausted. Default: true.
func WithGrowable(v bool) ArenaOption {
	return func(c *arenaConfig) {
		c.growable = v
	}
}

// slabConfig holds all Slab configuration. Assembled via functional
// [SlabOption] values passed to [NewSlab].
type slabConfig struct {
	capacity   int
	shards     int
	growable   bool
	growth     GrowthPolicy
	maxChunks  int
	batchHint  int
}

func defaultSlabConfig() slabConfig {
	return slabConfig{
		capacity:  4096,
		shards:    1,
		growable:  true,
		growth:    GrowAdaptive,
		maxChunks: 0, // unlimited
		batchHint: 64,
	}
}

// SlabOption configures a [Slab]. Options are passed to [NewSlab]
// and applied in order; the last value wins for each setting.
type SlabOption func(*slabConfig)

// WithSlabCapacity sets the initial number of objects per slab chunk.
// Must be positive. Default: 4096.
func WithSlabCapacity(n int) SlabOption {
	return func(c *slabConfig) {
		if n > 0 {
			c.capacity = n
		}
	}
}

// WithShards sets the number of independent slab shards for reduced
// contention in concurrent workloads. Each shard maintains its own
// freelist and backing storage. Must be positive. Default: 1.
func WithShards(n int) SlabOption {
	return func(c *slabConfig) {
		if n > 0 {
			c.shards = n
		}
	}
}

// WithSlabGrowable controls whether the slab allocates additional
// chunks when the current chunk's freelist is exhausted. Default: true.
func WithSlabGrowable(v bool) SlabOption {
	return func(c *slabConfig) {
		c.growable = v
	}
}

// WithGrowthPolicy sets how new chunks are sized when the slab grows.
// Default: [GrowAdaptive].
//
//   - [GrowFixed]: every chunk is the same size as the initial capacity.
//   - [GrowLinear]: each new chunk adds the initial capacity.
//   - [GrowAdaptive]: doubles until a ceiling, then fixed.
func WithGrowthPolicy(p GrowthPolicy) SlabOption {
	return func(c *slabConfig) {
		c.growth = p
	}
}

// WithMaxChunks sets the maximum number of chunks a single shard may
// hold. Zero means unlimited. When the limit is reached, [Slab.Alloc]
// returns [ErrOutOfMemory]. This provides hard backpressure to prevent
// unbounded memory growth. Default: 0 (unlimited).
func WithMaxChunks(n int) SlabOption {
	return func(c *slabConfig) {
		if n >= 0 {
			c.maxChunks = n
		}
	}
}

// WithBatchHint sets the expected batch size for [Slab.BatchAlloc].
// The slab may pre-size internal structures accordingly. Default: 64.
func WithBatchHint(n int) SlabOption {
	return func(c *slabConfig) {
		if n > 0 {
			c.batchHint = n
		}
	}
}

// hugeConfig holds all Huge allocator configuration.
type hugeConfig struct {
	alignment int
	poolReuse bool
}

func defaultHugeConfig() hugeConfig {
	return hugeConfig{
		alignment: 64, // cache-line
		poolReuse: true,
	}
}

// HugeOption configures a [Huge] allocator. Options are passed to
// [NewHuge] and applied in order.
type HugeOption func(*hugeConfig)

// WithAlignment sets the byte alignment for huge allocations.
// Must be a power of two. Default: 64 (cache-line).
func WithAlignment(n int) HugeOption {
	return func(c *hugeConfig) {
		if n > 0 && n&(n-1) == 0 {
			c.alignment = n
		}
	}
}

// WithPoolReuse controls whether freed buffers are returned to an
// internal size-class pool for reuse instead of being released to
// the garbage collector. Default: true.
func WithPoolReuse(v bool) HugeOption {
	return func(c *hugeConfig) {
		c.poolReuse = v
	}
}
