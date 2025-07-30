package sharded

import (
	"sync"
	"sync/atomic"
)

// Shard is a single partition of the sharded map.
// Each shard is an independent concurrent map with its own lock and refCounted pool for releasers.
type Shard[V Value] struct {
	*sync.RWMutex              // Shard-level RWMutex for concurrency
	items         map[uint64]V // Actual storage: key -> Value
	id            uint64       // Shard ID (index)
	mem           int64        // Weight usage in bytes (atomic)
	len           int64        // Length as int64 for use it as atomic
	releasersPool *sync.Pool
}

// NewShard creates a new shard with its own lock, value map, and releaser pool.
func NewShard[V Value](id uint64, defaultLen int) *Shard[V] {
	return &Shard[V]{
		id:      id,
		RWMutex: &sync.RWMutex{},
		items:   make(map[uint64]V, defaultLen),
	}
}

func (shard *Shard[V]) Clear() {
	shard.Lock()
	for id := range shard.items {
		delete(shard.items, id)
	}
	atomic.StoreInt64(&shard.mem, 0)
	atomic.StoreInt64(&shard.len, 0)
	shard.Unlock()
}

// ID returns the numeric index of this shard.
func (shard *Shard[V]) ID() uint64 {
	return shard.id
}

// Weight returns an approximate total memory usage for this shard (including overhead).
func (shard *Shard[V]) Weight() int64 {
	return atomic.LoadInt64(&shard.mem)
}

func (shard *Shard[V]) Len() int64 {
	return atomic.LoadInt64(&shard.len)
}

// Set inserts or updates a value by key, resets refCount, and updates counters.
// Returns a releaser for the inserted value.
func (shard *Shard[V]) Set(key uint64, new V) {
	shard.Lock()
	old, found := shard.items[key]
	shard.items[key] = new
	shard.Unlock()

	if found {
		atomic.AddInt64(&shard.mem, new.Weight()-old.Weight())
	} else {
		atomic.AddInt64(&shard.len, 1)
		atomic.AddInt64(&shard.mem, new.Weight())
	}
}

// Get retrieves a value and returns a releaser for it, incrementing its refCount.
// Returns (value, releaser, true) if found; otherwise (zero, nil, false).
func (shard *Shard[V]) Get(key uint64) (val V, ok bool) {
	shard.RLock()
	val, ok = shard.items[key]
	shard.RUnlock()
	return
}

func (shard *Shard[V]) GetRand() (val V, ok bool) {
	shard.RLock()
	defer shard.RUnlock()
	for _, item := range shard.items {
		return item, true
	}
	return val, false
}

// Remove removes a value from the shard, decrements counters, and may trigger full resource cleanup.
// Returns (memory_freed, pointer_to_list_element, was_found).
func (shard *Shard[V]) Remove(key uint64) (freedBytes int64, hit bool) {
	shard.Lock()
	entry, found := shard.items[key]
	if found {
		freed := entry.Weight()
		delete(shard.items, key)
		shard.Unlock()

		atomic.AddInt64(&shard.len, -1)
		atomic.AddInt64(&shard.mem, -freed)

		return freed, true
	}
	shard.Unlock()
	return 0, false
}
