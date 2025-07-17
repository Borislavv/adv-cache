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
		releasersPool: &sync.Pool{
			New: func() interface{} {
				return new(Releaser[V])
			},
		},
	}
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
func (shard *Shard[V]) Set(key uint64, value V) (takenMem int64, releaser *Releaser[V]) {
	shard.RLock()
	entry, isHit := shard.items[key]
	shard.RUnlock()

	if isHit {
		if entry.Acquire() {
			if entry.IsSameFingerprint(value.Fingerprint()) {
				return 0, newReleaser(entry, shard.releasersPool)
			} else {
				// hash collision was found
				entry.Remove()

				// prepare new value for insert
				if !value.Acquire() {
					//return 0, func() {}
				}
			}
		}

		takenMem = value.Weight() - entry.Weight()
		atomic.AddInt64(&shard.mem, takenMem)

		// release the entry which we are rewriting
		entry.Remove()
	} else {
		takenMem = value.Weight()
		atomic.AddInt64(&shard.len, 1)
		atomic.AddInt64(&shard.mem, value.Weight())
	}

	value.Acquire()
	shard.Lock()
	shard.items[key] = value
	shard.Unlock()

	return takenMem, newReleaser(value, shard.releasersPool)
}

// Get retrieves a value and returns a releaser for it, incrementing its refCount.
// Returns (value, releaser, true) if found; otherwise (zero, nil, false).
func (shard *Shard[V]) Get(entry V) (val V, rel *Releaser[V], isHit bool) {
	shard.RLock()
	value, ok := shard.items[entry.MapKey()]
	shard.RUnlock()

	if ok && value.IsSameFingerprint(entry.Fingerprint()) && value.Acquire() {
		return value, newReleaser(value, shard.releasersPool), ok
	}

	// not found or hash collision or already removed
	return val, nil, false
}

// Remove removes a value from the shard, decrements counters, and may trigger full resource cleanup.
// Returns (memory_freed, pointer_to_list_element, was_found).
func (shard *Shard[V]) Remove(key uint64) (freed int64, removed bool) {
	shard.Lock()
	v, ok := shard.items[key]
	if ok {
		delete(shard.items, key)
		shard.Unlock()

		freed = v.Weight()
		atomic.AddInt64(&shard.len, -1)
		atomic.AddInt64(&shard.mem, -freed)

		return freed, v.Remove()
	}
	shard.Unlock()
	return 0, false
}
